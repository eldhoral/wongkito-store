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

	if err := e.MarkCancelledIfRefunded(context.Background(), itemku.Order{OrderID: 46, Status: "REFUNDED"}); err != nil {
		t.Fatalf("MarkCancelledIfRefunded: %v", err)
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
