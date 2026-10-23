package relay

import (
	"context"
	"errors"
	"strings"
	"testing"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
)

func TestWebhookSuksesDelivers(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 40)
	mustExec(t, e, `UPDATE orders SET state = 'processing' WHERE id = ?`, id)
	insertAttemptRow(t, e, "r1", id, 0, 1, "primary", "123", "Pending", testNow.Unix()-120)

	if err := e.ApplyTx(context.Background(), digiflazz.TxResult{
		RefID: "r1", Status: "Sukses", SN: "SN1", Price: 12000,
	}); err != nil {
		t.Fatalf("ApplyTx: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateDelivered {
		t.Fatalf("state = %s, want delivered", state)
	}
	if ik.deliverCount() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", ik.deliverCount())
	}
	var status, sn string
	if err := e.DB.DB.QueryRow(`SELECT status, sn FROM attempts WHERE ref_id = 'r1'`).Scan(&status, &sn); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if status != "Sukses" || sn != "SN1" {
		t.Fatalf("attempt status/sn = %s/%s", status, sn)
	}
}

func TestApplyTxUnknownRefIgnored(t *testing.T) {
	e, _, _, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 41)
	mustExec(t, e, `UPDATE orders SET state = 'processing' WHERE id = ?`, id)

	if err := e.ApplyTx(context.Background(), digiflazz.TxResult{RefID: "nope", Status: "Sukses"}); err != nil {
		t.Fatalf("ApplyTx: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateProcessing {
		t.Fatalf("state = %s, want processing", state)
	}
}

func TestApplyTxReplayAfterDelivered(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 42)
	mustExec(t, e, `UPDATE orders SET state = 'processing' WHERE id = ?`, id)
	insertAttemptRow(t, e, "r1", id, 0, 1, "primary", "123", "Pending", testNow.Unix()-120)

	tx := digiflazz.TxResult{RefID: "r1", Status: "Sukses", SN: "SN1"}
	if err := e.ApplyTx(context.Background(), tx); err != nil {
		t.Fatalf("first ApplyTx: %v", err)
	}
	if err := e.ApplyTx(context.Background(), tx); err != nil {
		t.Fatalf("second ApplyTx: %v", err)
	}
	if ik.deliverCount() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", ik.deliverCount())
	}
}

func TestDeliverTimeoutButDelivered(t *testing.T) {
	e, ik, _, tg := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 43)
	mustExec(t, e, `UPDATE orders SET state = 'delivering' WHERE id = ?`, id)
	ik.deliverErr = errors.New("timeout")
	ik.orders[43] = itemku.Order{OrderID: 43, Status: "DELIVERED"}

	if err := e.Deliver(context.Background(), id); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateDelivered {
		t.Fatalf("state = %s, want delivered", state)
	}
	if got := len(tg.messages()); got != 0 {
		t.Fatalf("Telegram messages = %d, want 0", got)
	}
}

func TestDeliverTimeoutStillOpenRetries(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 44)
	mustExec(t, e, `UPDATE orders SET state = 'delivering' WHERE id = ?`, id)
	ik.deliverErr = errors.New("timeout")
	ik.orders[44] = itemku.Order{OrderID: 44, Status: "REQUIRE_PROCESS"}

	if err := e.Deliver(context.Background(), id); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateDelivering {
		t.Fatalf("state = %s, want delivering", state)
	}
	if ik.deliverCount() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", ik.deliverCount())
	}

	ik.deliverErr = nil
	if err := e.RecheckPending(context.Background()); err != nil {
		t.Fatalf("RecheckPending: %v", err)
	}
	if ik.deliverCount() != 2 {
		t.Fatalf("Deliver calls after re-check = %d, want 2", ik.deliverCount())
	}
	state, _ = orderState(t, e, id)
	if state != StateDelivered {
		t.Fatalf("state = %s, want delivered", state)
	}
}

func TestRejectBlockedWhenSukses(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 45)
	mustExec(t, e, `UPDATE orders SET state = 'needs_you', reason = 'auto_failed' WHERE id = ?`, id)
	insertAttemptRow(t, e, "r1", id, 0, 1, "primary", "123", "Sukses", testNow.Unix()-120)

	if err := e.Reject(context.Background(), id, "NO_STOCK", false); err == nil {
		t.Fatal("Reject() error = nil, want refusal")
	}
	if len(ik.rejectCalls) != 0 {
		t.Fatalf("Reject calls = %d, want 0", len(ik.rejectCalls))
	}
	if err := e.Reject(context.Background(), id, "NO_STOCK", true); err != nil {
		t.Fatalf("forced Reject: %v", err)
	}
	if len(ik.rejectCalls) != 1 {
		t.Fatalf("Reject calls = %d, want 1", len(ik.rejectCalls))
	}
	if ik.rejectReasons[0] != "NO_STOCK" {
		t.Fatalf("refund reason = %q, want NO_STOCK", ik.rejectReasons[0])
	}
	state, _ := orderState(t, e, id)
	if state != StateRejected {
		t.Fatalf("state = %s, want rejected", state)
	}
}

func TestRefundedWithPendingAlerts(t *testing.T) {
	e, _, _, tg := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 46)
	mustExec(t, e, `UPDATE orders SET state = 'processing' WHERE id = ?`, id)
	insertAttemptRow(t, e, "r1", id, 0, 1, "primary", "123", "Pending", testNow.Unix()-120)

	if err := e.CloseIfClosedOnItemku(context.Background(), itemku.Order{OrderID: 46, Status: "REFUNDED"}); err != nil {
		t.Fatalf("CloseIfClosedOnItemku: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateCancelled {
		t.Fatalf("state = %s, want cancelled", state)
	}
	msgs := tg.messages()
	if len(msgs) != 1 || !msgs[0].urgent {
		t.Fatalf("messages = %#v, want one urgent", msgs)
	}
	if !strings.Contains(msgs[0].text, "cancelled") {
		t.Fatalf("message = %q, want cancelled-order warning", msgs[0].text)
	}
}

func insertAttemptRow(t *testing.T, e *Engine, ref, orderID string, unit, attemptNo int64, sku, customer, status string, updated int64) {
	t.Helper()
	mustExec(t, e,
		`INSERT INTO attempts (ref_id, order_id, unit, attempt_no, buyer_sku_code, customer_no,
		 max_price, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		ref, orderID, unit, attemptNo, sku, customer, status, updated, updated)
}

func TestDeliveredOnItemkuWithSuksesAlerts(t *testing.T) {
	e, _, _, tg := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 47)
	mustExec(t, e, `UPDATE orders SET state = 'needs_you' WHERE id = ?`, id)
	insertAttemptRow(t, e, "r1", id, 0, 1, "primary", "123", "Sukses", testNow.Unix()-120)

	if err := e.CloseIfClosedOnItemku(context.Background(), itemku.Order{OrderID: 47, Status: "DELIVERED"}); err != nil {
		t.Fatalf("CloseIfClosedOnItemku: %v", err)
	}
	o, _ := e.loadOrder(context.Background(), id)
	if o.State != StateDelivered || o.Detail != "closed on itemku" || !o.DeliveredAt.Valid {
		t.Fatalf("order = %s/%q/%v, want delivered, closed on itemku, delivered_at set", o.State, o.Detail, o.DeliveredAt)
	}
	if msgs := tg.messages(); len(msgs) != 1 || !msgs[0].urgent {
		t.Fatalf("messages = %#v, want one urgent money-spent warning", msgs)
	}
}

func TestStuckBuyingSettlesToQueue(t *testing.T) {
	e, _, df, tg := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 48)
	// A cancelled context between "set buying" and the attempt insert.
	mustExec(t, e, `UPDATE orders SET state = 'buying', updated_at = ? WHERE id = ?`, testNow.Unix()-300, id)
	fresh := setupAutoOrderNoSeed(t, e, 48, 148)
	mustExec(t, e, `UPDATE orders SET state = 'buying', updated_at = ? WHERE id = ?`, testNow.Unix()-30, fresh)

	if err := e.RecheckPending(context.Background()); err != nil {
		t.Fatalf("RecheckPending: %v", err)
	}
	if state, reason := orderState(t, e, id); state != StateNeedsYou || reason != ReasonAutoFailed {
		t.Fatalf("stuck order = %s/%s, want needs_you/auto_failed", state, reason)
	}
	if state, _ := orderState(t, e, fresh); state != StateBuying {
		t.Fatalf("recent order = %s, want buying (younger than 2 min)", state)
	}
	if df.topupCount() != 0 {
		t.Fatalf("Topup calls = %d, want 0", df.topupCount())
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("alerts = %d, want 1", got)
	}
}

func TestLongOpenAttemptAlertsOnce(t *testing.T) {
	e, _, df, tg := newTestEngine(t)
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		return digiflazz.TxResult{RefID: r.RefID, Status: "Pending"}, nil
	}
	id := setupAutoOrder(t, e, "regular", 49)
	mustExec(t, e, `UPDATE orders SET state = 'processing' WHERE id = ?`, id)
	insertAttemptRow(t, e, "old", id, 0, 1, "primary", "123", "unknown", testNow.Unix()-31*60)

	for i := 0; i < 2; i++ {
		if err := e.RecheckPending(context.Background()); err != nil {
			t.Fatalf("RecheckPending %d: %v", i, err)
		}
	}
	msgs := tg.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].text, "30 minutes") {
		t.Fatalf("messages = %#v, want one 30-minute warning", msgs)
	}
}
