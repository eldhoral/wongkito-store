package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
)

// ApplyTx records a Digiflazz transaction update (webhook or status re-check)
// and recomputes the order from its attempts. Unknown reference ids and
// replays after a final attempt are ignored.
func (e *Engine) ApplyTx(ctx context.Context, r digiflazz.TxResult) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var orderID, currentStatus string
	err := e.DB.DB.QueryRowContext(ctx,
		`SELECT order_id, status FROM attempts WHERE ref_id = ?`, r.RefID).Scan(&orderID, &currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if currentStatus == "Sukses" || currentStatus == "Gagal" {
		return nil
	}
	status := normalizeTxStatus(r.Status)
	if status == currentStatus {
		return nil
	}
	if _, err := e.DB.DB.ExecContext(ctx,
		`UPDATE attempts SET status = ?, rc = ?, message = ?, sn = ?, price = ?, updated_at = ?
		 WHERE ref_id = ?`,
		status, r.RC, r.Message, r.SN, int64(r.Price), e.now().Unix(), r.RefID); err != nil {
		return err
	}

	o, err := e.loadOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if o == nil || isFinal(o.State) {
		return nil
	}
	outcome, err := e.settlePurchases(ctx, orderID, true, "")
	if err != nil {
		return err
	}
	if outcome == buySuccess {
		return e.deliverLocked(ctx, orderID)
	}
	return nil
}

// RecheckPending re-sends every Pending/unknown transaction older than a
// minute to Digiflazz (same ref_id, so it is a status check, not a new buy),
// then re-drives orders stuck in delivering.
func (e *Engine) RecheckPending(ctx context.Context) error {
	type pendingAttempt struct {
		refID      string
		orderID    string
		sku        string
		customerNo string
		maxPrice   int64
	}
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT ref_id, order_id, buyer_sku_code, customer_no, max_price FROM attempts
		 WHERE status IN ('Pending', 'unknown') AND updated_at < ? LIMIT 100`,
		e.now().Unix()-60)
	if err != nil {
		return err
	}
	var pending []pendingAttempt
	for rows.Next() {
		var p pendingAttempt
		if err := rows.Scan(&p.refID, &p.orderID, &p.sku, &p.customerNo, &p.maxPrice); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var firstErr error
	for _, p := range pending {
		o, err := e.loadOrder(ctx, p.orderID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if o == nil || isFinal(o.State) {
			continue
		}
		r, err := e.DF.Topup(ctx, digiflazz.TopupReq{
			SKU:        p.sku,
			CustomerNo: p.customerNo,
			RefID:      p.refID,
			MaxPrice:   p.maxPrice,
			Testing:    e.DryRun,
		})
		if err != nil {
			continue // still unknown; try again next tick
		}
		if err := e.ApplyTx(ctx, r); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if err := e.alertLongOpen(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := e.settleStuckBuying(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	rows, err = e.DB.DB.QueryContext(ctx, `SELECT id FROM orders WHERE state = ?`, StateDelivering)
	if err != nil {
		return err
	}
	var delivering []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		delivering = append(delivering, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range delivering {
		if err := e.Deliver(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// alertLongOpen tells the owner once about every attempt Digiflazz has left
// open (unknown or Pending) for over 30 minutes: its order is stuck in processing.
func (e *Engine) alertLongOpen(ctx context.Context) error {
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT a.ref_id, o.order_number, o.game_name, o.product_name
		 FROM attempts a JOIN orders o ON o.id = a.order_id
		 WHERE a.status IN ('Pending', 'unknown') AND a.created_at < ?
		   AND o.state NOT IN (?, ?, ?)`,
		e.now().Unix()-30*60, StateDelivered, StateRejected, StateCancelled)
	if err != nil {
		return err
	}
	type open struct{ ref, number, game, product string }
	var list []open
	for rows.Next() {
		var a open
		if err := rows.Scan(&a.ref, &a.number, &a.game, &a.product); err != nil {
			rows.Close()
			return err
		}
		list = append(list, a)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, a := range list {
		if err := e.alertOnce(ctx, "open30:"+a.ref, 0, fmt.Sprintf(
			"%s — %s — order %s: Digiflazz transaction %s still has no final status after 30 minutes; check it in the Digiflazz dashboard",
			a.game, a.product, a.number, a.ref), false); err != nil {
			return err
		}
	}
	return nil
}

// settleStuckBuying recovers orders left in buying (a DB error or a cancelled
// context between setting buying and recording the attempts). The attempts
// decide: all Sukses delivers, an open one waits in processing, and none or
// only Gagal goes to the owner with an alert.
func (e *Engine) settleStuckBuying(ctx context.Context) error {
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT id FROM orders WHERE state = ? AND updated_at < ?`, StateBuying, e.now().Unix()-120)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		if err := e.settleIfBuying(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (e *Engine) settleIfBuying(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, err := e.loadOrder(ctx, id)
	if err != nil || o == nil || o.State != StateBuying {
		return err // a buy that held the lock has moved it on
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

// Deliver marks the order delivered on itemku. A timeout or non-200 response
// is never assumed: itemku is re-read first, and ORDER_DELIVERED is success.
func (e *Engine) Deliver(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deliverLocked(ctx, id)
}

func (e *Engine) deliverLocked(ctx context.Context, id string) error {
	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return err
	}
	if o == nil || o.State == StateDelivered || o.State == StateRejected || o.State == StateCancelled {
		return nil
	}
	if o.Source == "direct" {
		return e.markDelivered(ctx, id)
	}
	if e.DryRun {
		// Spec: in dry run the itemku DELIVER is logged, not sent.
		log.Printf("relay: dry run: skipped itemku DELIVER for order %s (itemku id %d)", id, o.ItemkuOrderID.Int64)
		return e.markDelivered(ctx, id)
	}
	if !o.ItemkuOrderID.Valid {
		return fmt.Errorf("relay: itemku order %s has no itemku id", id)
	}
	if err := e.setOrderState(ctx, id, StateDelivering, "", ""); err != nil {
		return err
	}

	err = e.IK.Deliver(ctx, o.ItemkuOrderID.Int64)
	if err == nil {
		return e.markDelivered(ctx, id)
	}
	var apiErr *itemku.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "ORDER_DELIVERED" {
		return e.markDelivered(ctx, id)
	}

	fresh, getErr := e.IK.GetOrder(ctx, o.ItemkuOrderID.Int64)
	if getErr != nil || fresh == nil {
		_ = e.setOrderState(ctx, id, StateDelivering, "", "")
		return err
	}
	switch fresh.Status {
	case "DELIVERED":
		return e.markDelivered(ctx, id)
	case "REQUIRE_PROCESS":
		_ = e.setOrderState(ctx, id, StateDelivering, "", "")
		return nil
	}

	detail := "itemku status " + fresh.Status
	if err := e.toQueue(ctx, id, ReasonDeliverFailed, detail); err != nil {
		return err
	}
	e.alert(ctx, fmt.Sprintf(
		"%s — %s — order %s: deliver failed (%s); money may be spent",
		o.GameName, o.ProductName, o.OrderNumber, fresh.Status), true)
	return nil
}

func (e *Engine) markDelivered(ctx context.Context, id string) error {
	now := e.now().Unix()
	_, err := e.DB.DB.ExecContext(ctx,
		`UPDATE orders SET state = ?, reason = '', detail = '', delivered_at = ?, updated_at = ? WHERE id = ?`,
		StateDelivered, now, now, id)
	return err
}

// Reject rejects and refunds an itemku order. It refuses while a unit is
// successful or still open, unless force is set.
func (e *Engine) Reject(ctx context.Context, id, reason string, force bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return err
	}
	if o == nil {
		return fmt.Errorf("relay: order %s not found", id)
	}
	if o.State == StateRejected {
		return nil
	}
	if isFinal(o.State) {
		return fmt.Errorf("relay: order %s is already %s", id, o.State)
	}
	if !force {
		open, err := e.hasAttemptsIn(ctx, id, "Sukses", "Pending", "unknown")
		if err != nil {
			return err
		}
		if open {
			return fmt.Errorf("relay: order %s has a successful or open attempt", id)
		}
	}
	if o.Source == "itemku" && o.ItemkuOrderID.Valid {
		if err := e.IK.Reject(ctx, o.ItemkuOrderID.Int64, reason); err != nil {
			var apiErr *itemku.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "ORDER_REJECTED" {
				return err
			}
		}
	}
	return e.setOrderState(ctx, id, StateRejected, "", "")
}

// Cancel closes a direct order without any external call. Money is refunded
// to the buyer outside the app. Like Reject, it refuses while a unit is
// successful or still open, unless force is set (the owner confirmed).
func (e *Engine) Cancel(ctx context.Context, id string, force bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	o, err := e.loadOrder(ctx, id)
	if err != nil {
		return err
	}
	if o == nil {
		return fmt.Errorf("relay: order %s not found", id)
	}
	if o.Source != "direct" {
		return fmt.Errorf("relay: order %s is not a direct order", id)
	}
	if isFinal(o.State) {
		return nil
	}
	if !force {
		open, err := e.hasAttemptsIn(ctx, id, "Sukses", "Pending", "unknown")
		if err != nil {
			return err
		}
		if open {
			return fmt.Errorf("relay: order %s has a successful or open attempt", id)
		}
	}
	return e.setOrderState(ctx, id, StateCancelled, "", "")
}

// CloseIfClosedOnItemku is called with a fresh itemku read (poll, callback).
// It records an order the owner or itemku closed outside the app.
func (e *Engine) CloseIfClosedOnItemku(ctx context.Context, o itemku.Order) error {
	if o.Status != "REFUNDED" && o.Status != "DELIVERED" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	var id string
	err := e.DB.DB.QueryRowContext(ctx,
		`SELECT id FROM orders WHERE itemku_order_id = ?`, o.OrderID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = e.closeFromItemku(ctx, id, o.Status)
	return err
}

// closeFromItemku maps itemku's DELIVERED/REFUNDED onto a non-final order and
// warns loudly when money may already have been spent here. It reports whether
// the status was one it handles. The caller holds e.mu.
func (e *Engine) closeFromItemku(ctx context.Context, id, itemkuStatus string) (bool, error) {
	var state, detail, warn string
	switch itemkuStatus {
	case "DELIVERED":
		state, detail = StateDelivered, "closed on itemku"
		warn = "order %s was delivered on itemku outside the app, but a purchase here succeeded or is still open; money may be spent twice"
	case "REFUNDED":
		state, detail = StateCancelled, "itemku refunded the order"
		warn = "order %s was refunded by itemku but money may be spent on a cancelled order"
	default:
		return false, nil
	}
	o, err := e.loadOrder(ctx, id)
	if err != nil || o == nil || isFinal(o.State) {
		return true, err
	}
	spent, err := e.hasAttemptsIn(ctx, id, "Sukses", "Pending", "unknown")
	if err != nil {
		return true, err
	}
	if spent {
		e.alert(ctx, fmt.Sprintf(warn, id), true)
	}
	now := e.now().Unix()
	var deliveredAt any
	if state == StateDelivered {
		deliveredAt = now
	}
	_, err = e.DB.DB.ExecContext(ctx,
		`UPDATE orders SET state = ?, reason = '', detail = ?, delivered_at = COALESCE(?, delivered_at), updated_at = ?
		 WHERE id = ?`, state, detail, deliveredAt, now, id)
	return true, err
}

func (e *Engine) hasAttemptsIn(ctx context.Context, orderID string, statuses ...string) (bool, error) {
	if len(statuses) == 0 {
		return false, nil
	}
	query := `SELECT COUNT(*) FROM attempts WHERE order_id = ? AND status IN (?` +
		strings.Repeat(", ?", len(statuses)-1) + `)`
	args := make([]any, 0, len(statuses)+1)
	args = append(args, orderID)
	for _, status := range statuses {
		args = append(args, status)
	}
	var count int
	if err := e.DB.DB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
