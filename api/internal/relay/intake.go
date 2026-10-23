package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"wongkito/api/internal/itemku"
	"wongkito/api/internal/tmpl"
)

// Intake inserts an itemku order once and routes it. It is safe to call from
// the callback, the backup poll, and concurrently.
func (e *Engine) Intake(ctx context.Context, o itemku.Order) (bool, error) {
	quantity := o.Quantity
	if quantity <= 0 {
		quantity = 1
	}
	created := o.OrderCreatedEpoch
	if created == 0 {
		created = e.now().Unix()
	}
	class := e.deadlineClass(ctx, o.ProductID)
	deadline := created + int64(deadlineDuration(class).Seconds())
	id := fmt.Sprintf("ik-%d", o.OrderID)

	result, err := e.DB.DB.ExecContext(ctx,
		`INSERT INTO orders (id, source, itemku_order_id, order_number, product_id, game_name, product_name,
			price, quantity, order_income, required_info, using_delivery_info, customer_no, buyer_name,
			paid_price, state, reason, detail, deadline_at, created_at, updated_at)
		 VALUES (?, 'itemku', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, 0, 'new', '', '', ?, ?, ?)
		 ON CONFLICT(itemku_order_id) DO NOTHING`,
		id, o.OrderID, o.OrderNumber, o.ProductID, o.GameName, o.ProductName,
		o.Price, quantity, o.OrderIncome, o.RequiredInformation, boolInt(o.UsingDeliveryInfo),
		o.BuyerName, deadline, created, e.now().Unix())
	if err != nil {
		return false, fmt.Errorf("relay: insert order %d: %w", o.OrderID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	createdRow := affected > 0
	if createdRow {
		if err := e.LearnFee(ctx, o); err != nil {
			return true, err
		}
	}

	if err := e.Route(ctx, id); err != nil {
		return createdRow, err
	}
	return createdRow, nil
}

// VerifyAndIntake re-reads an order named by the callback and intakes it only
// while it is still REQUIRE_PROCESS. Its error is recorded as itemku's last error.
func (e *Engine) VerifyAndIntake(ctx context.Context, orderID int64) error {
	err := e.verifyAndIntake(ctx, orderID)
	e.NoteError("itemku", err)
	return err
}

func (e *Engine) verifyAndIntake(ctx context.Context, orderID int64) error {
	o, err := e.IK.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if o == nil {
		return nil
	}
	if o.Status != "REQUIRE_PROCESS" {
		return e.CloseIfClosedOnItemku(ctx, *o)
	}
	_, err = e.Intake(ctx, *o)
	return err
}

// Route applies the routing rules in order. It only acts on an order in the
// new state, so duplicate callbacks and polls are harmless.
func (e *Engine) Route(ctx context.Context, id string) error {
	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return err
	}
	if o == nil || o.Source != "itemku" || o.State != StateNew {
		return nil
	}
	if o.UsingDeliveryInfo {
		return e.toQueue(ctx, id, ReasonUnsupportedType, "itemku requires delivery_info")
	}
	if !o.ProductID.Valid {
		return e.toQueue(ctx, id, ReasonNoMapping, "order has no product id")
	}
	p, err := e.loadProduct(ctx, o.ProductID.Int64)
	if err != nil {
		return err
	}
	if p == nil {
		return e.toQueue(ctx, id, ReasonNoMapping, "product is not synced yet")
	}
	sku, err := e.primarySKU(ctx, o.ProductID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return e.toQueue(ctx, id, ReasonNoMapping, "product has no linked Digiflazz SKU")
	}
	if err != nil {
		return err
	}
	template, seller, err := e.primaryTemplate(ctx, o.ProductID.Int64, sku)
	if err != nil {
		return err
	}
	if template == "" {
		return e.toQueue(ctx, id, ReasonTemplateError,
			fmt.Sprintf("no template for seller %q and game %q", seller, p.GameName))
	}
	info, err := (itemku.Order{RequiredInformation: o.RequiredInfo}).Info()
	if err != nil {
		return e.toQueue(ctx, id, ReasonTemplateError, err.Error())
	}
	customerNo, err := tmpl.Render(template, info)
	if err != nil {
		return e.toQueue(ctx, id, ReasonTemplateError, err.Error())
	}
	if _, err := e.DB.DB.ExecContext(ctx,
		`UPDATE orders SET customer_no = ?, sku_override = ?, updated_at = ? WHERE id = ?`,
		customerNo, sku, e.now().Unix(), id); err != nil {
		return err
	}

	multi, err := e.skuIsMulti(ctx, sku)
	if err != nil {
		return err
	}
	if o.Quantity > 1 && !multi {
		return e.toQueue(ctx, id, ReasonMultiBlocked,
			fmt.Sprintf("quantity %d but SKU %s is single-use", o.Quantity, sku))
	}
	if p.Mode == "manual" || p.Mode == "off" {
		return e.toQueue(ctx, id, ReasonManualProduct, "product mode is "+p.Mode)
	}
	return e.BuyAuto(ctx, id)
}

// toQueue moves a non-final order to needs_you and alerts once per transition.
func (e *Engine) toQueue(ctx context.Context, id, reason, detail string) error {
	result, err := e.DB.DB.ExecContext(ctx,
		`UPDATE orders SET state = ?, reason = ?, detail = ?, updated_at = ?
		 WHERE id = ? AND state NOT IN (?, ?, ?, ?)`,
		StateNeedsYou, reason, detail, e.now().Unix(), id,
		StateNeedsYou, StateDelivered, StateRejected, StateCancelled)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 || e.TG == nil {
		return nil
	}
	o, err := e.loadOrder(ctx, id)
	if err != nil || o == nil {
		return err
	}
	class := "regular"
	if o.ProductID.Valid {
		class = e.deadlineClass(ctx, o.ProductID.Int64)
	}
	urgent := class == "ten_min" || class == "instant"
	text := fmt.Sprintf("%s — %s — order %s needs you: %s", o.GameName, o.ProductName, o.OrderNumber, reason)
	if detail != "" {
		text += " (" + detail + ")"
	}
	e.alert(ctx, text, urgent)
	return nil
}

func (e *Engine) primarySKU(ctx context.Context, productID int64) (string, error) {
	var sku string
	err := e.DB.DB.QueryRowContext(ctx,
		`SELECT buyer_sku_code FROM product_skus WHERE product_id = ? ORDER BY position LIMIT 1`,
		productID).Scan(&sku)
	return sku, err
}

// primaryTemplate returns the template for the SKU's seller and the product's
// itemku game. An empty template with a nil error means "not configured".
func (e *Engine) primaryTemplate(ctx context.Context, productID int64, sku string) (template, seller string, err error) {
	var game string
	err = e.DB.DB.QueryRowContext(ctx, `SELECT game_name FROM products WHERE id = ?`, productID).Scan(&game)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	err = e.DB.DB.QueryRowContext(ctx, `SELECT seller_name FROM pricelist WHERE buyer_sku_code = ?`, sku).Scan(&seller)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	err = e.DB.DB.QueryRowContext(ctx,
		`SELECT template FROM templates WHERE seller_name = ? AND game_name = ?`, seller, game).Scan(&template)
	if errors.Is(err, sql.ErrNoRows) {
		return "", seller, nil
	}
	if err != nil {
		return "", seller, err
	}
	return template, seller, nil
}

func (e *Engine) skuIsMulti(ctx context.Context, sku string) (bool, error) {
	var multi int
	err := e.DB.DB.QueryRowContext(ctx, `SELECT multi FROM pricelist WHERE buyer_sku_code = ?`, sku).Scan(&multi)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return multi != 0, nil
}

// BuyAuto is implemented in buy.go.
