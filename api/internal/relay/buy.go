package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
	"wongkito/api/internal/tmpl"
)

// BuyOpts is a manual purchase. Empty SKU means the primary SKU; empty
// CustomerNo means render the number from that SKU's seller template.
type BuyOpts struct {
	SKU        string
	CustomerNo string
	NoCap      bool
}

type buyOutcome int

const (
	buySuccess buyOutcome = iota
	buyOpen
	buyFailed
)

// BuyAuto buys every missing unit with the primary SKU, then tries the
// alternate SKUs for fast-class products when the primary fails.
func (e *Engine) BuyAuto(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return err
	}
	if o == nil {
		return fmt.Errorf("relay: order %s not found", id)
	}
	// Route checks for new outside the lock; a callback and the poll can both
	// get here. Only the first caller may buy.
	if o.State != StateNew || o.Source != "itemku" {
		return nil
	}
	ok, err := e.preBuyCheck(ctx, *o)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !o.ProductID.Valid {
		return e.toQueue(ctx, id, ReasonNoMapping, "order has no product")
	}
	p, err := e.loadProduct(ctx, o.ProductID.Int64)
	if err != nil {
		return err
	}
	if p == nil {
		return e.toQueue(ctx, id, ReasonNoMapping, "product is not synced")
	}
	sku, err := e.primarySKU(ctx, p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return e.toQueue(ctx, id, ReasonNoMapping, "product has no linked SKU")
	}
	if err != nil {
		return err
	}
	customerNo, err := e.customerNoFor(ctx, *o, p, sku)
	if err != nil {
		return e.toQueue(ctx, id, ReasonTemplateError, err.Error())
	}
	maxPrice := e.maxPrice(*o)

	if err := e.setOrderState(ctx, id, StateBuying, "", ""); err != nil {
		return err
	}
	if _, err := e.buyUnits(ctx, id, sku, customerNo, maxPrice); err != nil {
		return err
	}
	outcome, err := e.settlePurchases(ctx, id, false, "")
	if err != nil {
		return err
	}
	switch outcome {
	case buySuccess:
		return e.deliverLocked(ctx, id)
	case buyOpen:
		return nil
	}

	// Fast classes get one chance per alternate SKU before the owner is
	// interrupted. Regular orders never try alternates automatically.
	class := e.deadlineClass(ctx, p.ID)
	if class != "ten_min" && class != "instant" {
		_, err = e.settlePurchases(ctx, id, true, "")
		return err
	}
	alternates, err := e.alternateSKUs(ctx, p.ID)
	if err != nil {
		return err
	}
	for _, alt := range alternates {
		if alt == sku {
			continue
		}
		template, _, err := e.primaryTemplate(ctx, p.ID, alt)
		if err != nil {
			return err
		}
		if template == "" {
			continue
		}
		price, found, err := e.skuPrice(ctx, alt)
		if err != nil {
			return err
		}
		if !found || (maxPrice > 0 && price > maxPrice) {
			continue
		}
		customerNo, err := e.customerNoFor(ctx, *o, p, alt)
		if err != nil {
			continue
		}
		if _, err := e.buyUnits(ctx, id, alt, customerNo, maxPrice); err != nil {
			return err
		}
		outcome, err = e.settlePurchases(ctx, id, false, "")
		if err != nil {
			return err
		}
		if outcome == buySuccess {
			return e.deliverLocked(ctx, id)
		}
		if outcome == buyOpen {
			return nil
		}
	}
	_, err = e.settlePurchases(ctx, id, true, "")
	return err
}

// BuyManual is the owner's retry. For itemku orders it re-reads the order
// from itemku immediately before buying and refuses if it is no longer open.
func (e *Engine) BuyManual(ctx context.Context, id string, opts BuyOpts) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return err
	}
	if o == nil {
		return fmt.Errorf("relay: order %s not found", id)
	}
	if isFinal(o.State) {
		return nil
	}
	// A stale screen can send Buy for an order the relay already moved on.
	// The attempts guard stops a second purchase; this keeps the state from regressing.
	switch o.State {
	case StateBuying, StateProcessing, StateDelivering:
		return fmt.Errorf("relay: order %s is already being processed", id)
	}
	if o.Source == "itemku" {
		if !o.ItemkuOrderID.Valid {
			return fmt.Errorf("relay: itemku order %s has no itemku id", id)
		}
		fresh, err := e.IK.GetOrder(ctx, o.ItemkuOrderID.Int64)
		if err != nil {
			return err
		}
		if fresh == nil || fresh.Status != "REQUIRE_PROCESS" {
			if fresh != nil {
				if _, err := e.closeFromItemku(ctx, id, fresh.Status); err != nil {
					return err
				}
			}
			return fmt.Errorf("relay: order %s is no longer REQUIRE_PROCESS", id)
		}
	}

	var p *productRow
	if o.ProductID.Valid {
		p, err = e.loadProduct(ctx, o.ProductID.Int64)
		if err != nil {
			return err
		}
	}
	sku := opts.SKU
	if sku == "" {
		if o.ProductID.Valid {
			sku, err = e.primarySKU(ctx, o.ProductID.Int64)
			if err != nil {
				return err
			}
		} else if o.SKUOverride.Valid {
			sku = o.SKUOverride.String
		}
	}
	if sku == "" {
		return fmt.Errorf("relay: order %s has no SKU", id)
	}
	customerNo := strings.TrimSpace(opts.CustomerNo)
	if customerNo == "" {
		customerNo, err = e.customerNoFor(ctx, *o, p, sku)
		if err != nil {
			return err
		}
	}
	maxPrice := int64(0)
	if !opts.NoCap {
		maxPrice = e.maxPrice(*o)
	}

	if err := e.setOrderState(ctx, id, StateBuying, "", ""); err != nil {
		return err
	}
	if _, err := e.buyUnits(ctx, id, sku, customerNo, maxPrice); err != nil {
		return err
	}
	outcome, err := e.settlePurchases(ctx, id, true, "")
	if err != nil {
		return err
	}
	if outcome == buySuccess {
		return e.deliverLocked(ctx, id)
	}
	return nil
}

// buyUnits performs the sequential per-unit purchases. It inserts the attempt
// row before calling Digiflazz; the partial unique index is the double-buy
// guard. A transport error leaves the attempt unknown and stops the loop.
func (e *Engine) buyUnits(ctx context.Context, id, sku, customerNo string, maxPrice int64) (bool, error) {
	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return false, err
	}
	if o == nil {
		return false, fmt.Errorf("relay: order %s not found", id)
	}
	quantity := o.Quantity
	if quantity < 1 {
		quantity = 1
	}
	for unit := int64(0); unit < quantity; unit++ {
		var attemptNo int64
		if err := e.DB.DB.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(attempt_no), 0) + 1 FROM attempts WHERE order_id = ? AND unit = ?`,
			id, unit).Scan(&attemptNo); err != nil {
			return false, err
		}
		refID := fmt.Sprintf("%s-%d-%d", id, unit, attemptNo)
		result, err := e.DB.DB.ExecContext(ctx,
			`INSERT INTO attempts (ref_id, order_id, unit, attempt_no, buyer_sku_code, customer_no,
			 max_price, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?)
			 ON CONFLICT DO NOTHING`,
			refID, id, unit, attemptNo, sku, customerNo, maxPrice, e.now().Unix(), e.now().Unix())
		if err != nil {
			return false, err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if inserted == 0 {
			continue // another attempt for this unit is open or succeeded
		}

		tx, txErr := e.DF.Topup(ctx, digiflazz.TopupReq{
			SKU:        sku,
			CustomerNo: customerNo,
			RefID:      refID,
			MaxPrice:   maxPrice,
			Testing:    e.DryRun,
		})
		var resErr *digiflazz.ResultError
		if errors.As(txErr, &resErr) {
			// First send of a new ref_id: a definitive non-2xx result is the
			// transaction's outcome, so settle it.
			tx, txErr = resErr.Result, nil
		}
		if txErr != nil {
			break // transport error: leave the attempt unknown for the re-check loop
		}
		status := normalizeTxStatus(tx.Status)
		if _, err := e.DB.DB.ExecContext(ctx,
			`UPDATE attempts SET status = ?, rc = ?, message = ?, sn = ?, price = ?, updated_at = ?
			 WHERE ref_id = ?`,
			status, tx.RC, tx.Message, tx.SN, int64(tx.Price), e.now().Unix(), refID); err != nil {
			return false, err
		}
		if status != "Sukses" {
			break
		}
	}
	return e.unitsAllSukses(ctx, id, quantity)
}

func normalizeTxStatus(status string) string {
	switch status {
	case "Sukses", "Pending", "Gagal":
		return status
	default:
		return "unknown"
	}
}

func (e *Engine) unitsAllSukses(ctx context.Context, id string, quantity int64) (bool, error) {
	var successes int64
	if err := e.DB.DB.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT unit) FROM attempts WHERE order_id = ? AND status = 'Sukses'`,
		id).Scan(&successes); err != nil {
		return false, err
	}
	return successes >= quantity, nil
}

type unitAggregate struct {
	sukses bool
	open   bool
	failed bool
}

// settlePurchases derives the order state from its attempts. It can defer the
// needs_you transition so BuyAuto can try alternates without a premature
// Telegram alert.
func (e *Engine) settlePurchases(ctx context.Context, id string, queueOnFailure bool, detail string) (buyOutcome, error) {
	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return buyFailed, err
	}
	if o == nil {
		return buyFailed, fmt.Errorf("relay: order %s not found", id)
	}
	quantity := o.Quantity
	if quantity < 1 {
		quantity = 1
	}
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT unit, status FROM attempts WHERE order_id = ? ORDER BY unit, attempt_no`, id)
	if err != nil {
		return buyFailed, err
	}
	defer rows.Close()

	units := make(map[int64]*unitAggregate)
	for rows.Next() {
		var unit int64
		var status string
		if err := rows.Scan(&unit, &status); err != nil {
			return buyFailed, err
		}
		a := units[unit]
		if a == nil {
			a = &unitAggregate{}
			units[unit] = a
		}
		switch status {
		case "Sukses":
			a.sukses = true
			a.open = false
			a.failed = false
		case "Pending", "unknown":
			if !a.sukses {
				a.open = true
			}
		case "Gagal":
			if !a.sukses {
				a.failed = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return buyFailed, err
	}

	var successes, open, failed int64
	for unit := int64(0); unit < quantity; unit++ {
		a := units[unit]
		switch {
		case a != nil && a.sukses:
			successes++
		case a != nil && a.open:
			open++
		case a != nil && a.failed:
			failed++
		}
	}
	if successes >= quantity {
		if err := e.setOrderState(ctx, id, StateDelivering, "", ""); err != nil {
			return buyFailed, err
		}
		return buySuccess, nil
	}
	if open > 0 {
		if err := e.setOrderState(ctx, id, StateProcessing, "", ""); err != nil {
			return buyFailed, err
		}
		return buyOpen, nil
	}
	if queueOnFailure {
		reason := ReasonAutoFailed
		if successes > 0 {
			reason = ReasonPartial
		}
		if detail == "" {
			detail = e.failureDetail(ctx, id)
		}
		if err := e.toQueue(ctx, id, reason, detail); err != nil {
			return buyFailed, err
		}
	}
	return buyFailed, nil
}

// maxPrice is the Digiflazz price cap for one unit. Itemku orders use income
// after itemku's fee minus min_margin; direct orders use the price the buyer
// paid, or no cap when none was entered.
func (e *Engine) maxPrice(o order) int64 {
	quantity := o.Quantity
	if quantity < 1 {
		quantity = 1
	}
	if o.Source == "itemku" {
		cap := o.OrderIncome/quantity - e.settingInt(context.Background(), "min_margin")
		if cap < 1 {
			cap = 1 // never turn a non-positive cap into "no cap"
		}
		return cap
	}
	if o.PaidPrice > 0 {
		return o.PaidPrice / quantity
	}
	return 0
}

// preBuyCheck re-reads itemku when the deadline window is nearly gone, and
// cancels rather than buying for an order itemku no longer considers open.
func (e *Engine) preBuyCheck(ctx context.Context, o order) (bool, error) {
	if o.Source != "itemku" || !o.DeadlineAt.Valid || !o.ItemkuOrderID.Valid {
		return true, nil
	}
	if o.DeadlineAt.Int64-e.now().Unix() > 30 {
		return true, nil
	}
	fresh, err := e.IK.GetOrder(ctx, o.ItemkuOrderID.Int64)
	if err != nil {
		return false, err
	}
	if fresh == nil || fresh.Status != "REQUIRE_PROCESS" {
		if fresh != nil {
			closed, err := e.closeFromItemku(ctx, o.ID, fresh.Status)
			if err != nil || closed {
				return false, err
			}
		}
		if err := e.setOrderState(ctx, o.ID, StateCancelled, "", ""); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (e *Engine) customerNoFor(ctx context.Context, o order, p *productRow, sku string) (string, error) {
	if o.CustomerNo != "" && (!o.SKUOverride.Valid || o.SKUOverride.String == sku) {
		return o.CustomerNo, nil
	}
	if p == nil {
		if o.CustomerNo != "" {
			return o.CustomerNo, nil
		}
		return "", fmt.Errorf("relay: no product to render a customer number for SKU %s", sku)
	}
	template, seller, err := e.primaryTemplate(ctx, p.ID, sku)
	if err != nil {
		return "", err
	}
	if template == "" {
		return "", fmt.Errorf("relay: no template for seller %q and game %q", seller, p.GameName)
	}
	info, err := (itemku.Order{RequiredInformation: o.RequiredInfo}).Info()
	if err != nil {
		return "", err
	}
	return tmpl.Render(template, info)
}

func (e *Engine) alternateSKUs(ctx context.Context, productID int64) ([]string, error) {
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT buyer_sku_code FROM product_skus WHERE product_id = ? AND position > 0 ORDER BY position`,
		productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var skus []string
	for rows.Next() {
		var sku string
		if err := rows.Scan(&sku); err != nil {
			return nil, err
		}
		skus = append(skus, sku)
	}
	return skus, rows.Err()
}

func (e *Engine) skuPrice(ctx context.Context, sku string) (int64, bool, error) {
	var price int64
	err := e.DB.DB.QueryRowContext(ctx, `SELECT price FROM pricelist WHERE buyer_sku_code = ?`, sku).Scan(&price)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return price, true, nil
}

func (e *Engine) settingInt(ctx context.Context, key string) int64 {
	var value string
	err := e.DB.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (e *Engine) failureDetail(ctx context.Context, id string) string {
	var sku, rc, message string
	err := e.DB.DB.QueryRowContext(ctx,
		`SELECT buyer_sku_code, rc, message FROM attempts
		 WHERE order_id = ? AND status = 'Gagal'
		 ORDER BY updated_at DESC, ref_id DESC LIMIT 1`, id).Scan(&sku, &rc, &message)
	if err != nil {
		return "purchase failed"
	}
	detail := fmt.Sprintf("%s: %s (rc %s)", sku, message, rc)
	var seller string
	if err := e.DB.DB.QueryRowContext(ctx,
		`SELECT seller_name FROM pricelist WHERE buyer_sku_code = ?`, sku).Scan(&seller); err == nil && seller != "" {
		detail += " seller " + seller
	}
	return detail
}

func (e *Engine) setOrderState(ctx context.Context, id, state, reason, detail string) error {
	_, err := e.DB.DB.ExecContext(ctx,
		`UPDATE orders SET state = ?, reason = ?, detail = ?, updated_at = ? WHERE id = ?`,
		state, reason, detail, e.now().Unix(), id)
	return err
}
