package relay

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
)

func TestAutoSuccessDelivers(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 30)

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	if df.topupCount() != 1 {
		t.Fatalf("Topup calls = %d, want 1", df.topupCount())
	}
	if ik.deliverCount() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", ik.deliverCount())
	}
	state, _ := orderState(t, e, id)
	if state != StateDelivered {
		t.Fatalf("state = %s, want delivered", state)
	}
	var status string
	if err := e.DB.DB.QueryRow(`SELECT status FROM attempts WHERE order_id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if status != "Sukses" {
		t.Fatalf("attempt status = %s, want Sukses", status)
	}
}

func TestNoSecondBuyWhilePending(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Pending"}, nil
	}
	id := setupAutoOrder(t, e, "regular", 31)

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateProcessing {
		t.Fatalf("state = %s, want processing", state)
	}
	ik.orders[31] = itemku.Order{OrderID: 31, Status: "REQUIRE_PROCESS"}
	err := e.BuyManual(context.Background(), id, BuyOpts{})
	if err == nil || !strings.Contains(err.Error(), "already being processed") {
		t.Fatalf("BuyManual error = %v, want already being processed", err)
	}
	if state, _ := orderState(t, e, id); state != StateProcessing {
		t.Fatalf("state after refused buy = %s, want processing", state)
	}
	if df.topupCount() != 1 {
		t.Fatalf("Topup calls = %d, want 1", df.topupCount())
	}
}

func TestTransportErrorLeavesUnknownAndBlocks(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		return digiflazz.TxResult{}, context.DeadlineExceeded
	}
	id := setupAutoOrder(t, e, "regular", 32)

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateProcessing {
		t.Fatalf("state = %s, want processing", state)
	}
	var status string
	if err := e.DB.DB.QueryRow(`SELECT status FROM attempts WHERE order_id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if status != "unknown" {
		t.Fatalf("attempt status = %s, want unknown", status)
	}
	ik.orders[32] = itemku.Order{OrderID: 32, Status: "REQUIRE_PROCESS"}
	if err := e.BuyManual(context.Background(), id, BuyOpts{}); err == nil {
		t.Fatal("BuyManual error = nil, want refusal while processing")
	}
	if df.topupCount() != 1 {
		t.Fatalf("Topup calls = %d, want 1", df.topupCount())
	}
}

func TestConcurrentBuyOneTopup(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 33)
	ik.orders[33] = itemku.Order{OrderID: 33, Status: "REQUIRE_PROCESS"}

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.BuyManual(context.Background(), id, BuyOpts{})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("BuyManual: %v", err)
		}
	}
	if df.topupCount() != 1 {
		t.Fatalf("Topup calls = %d, want 1", df.topupCount())
	}
	if ik.deliverCount() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", ik.deliverCount())
	}
}

func TestMaxPriceFromIncome(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 34)
	mustExec(t, e, `UPDATE orders SET quantity = 2, order_income = 90000 WHERE id = ?`, id)
	mustExec(t, e, `INSERT INTO settings (key, value) VALUES ('min_margin', '200')`)

	o, err := e.loadOrder(context.Background(), id)
	if err != nil {
		t.Fatalf("loadOrder: %v", err)
	}
	if got := e.maxPrice(*o); got != 44800 {
		t.Fatalf("maxPrice = %d, want 44800", got)
	}

	ik.orders[34] = itemku.Order{OrderID: 34, Status: "REQUIRE_PROCESS"}
	if err := e.BuyManual(context.Background(), id, BuyOpts{NoCap: true}); err != nil {
		t.Fatalf("BuyManual: %v", err)
	}
	for _, r := range df.requests() {
		if r.MaxPrice != 0 {
			t.Fatalf("NoCap request MaxPrice = %d, want 0", r.MaxPrice)
		}
	}
}

func TestQuantityTwoPartial(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	var calls int32
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 2 {
			return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Gagal", RC: "03", Message: "product unavailable"}, nil
		}
		return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Sukses", SN: "SN1"}, nil
	}
	id := setupAutoOrder(t, e, "regular", 35)
	mustExec(t, e, `UPDATE orders SET quantity = 2 WHERE id = ?`, id)

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	state, reason := orderState(t, e, id)
	if state != StateNeedsYou || reason != ReasonPartial {
		t.Fatalf("state/reason = %s/%s, want needs_you/partial", state, reason)
	}
	if df.topupCount() != 2 {
		t.Fatalf("Topup calls = %d, want 2", df.topupCount())
	}

	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Sukses", SN: "SN2"}, nil
	}
	ik.orders[35] = itemku.Order{OrderID: 35, Status: "REQUIRE_PROCESS"}
	if err := e.BuyManual(context.Background(), id, BuyOpts{}); err != nil {
		t.Fatalf("BuyManual retry: %v", err)
	}
	reqs := df.requests()
	if len(reqs) != 3 {
		t.Fatalf("Topup requests = %d, want 3", len(reqs))
	}
	if !strings.HasSuffix(reqs[2].RefID, "-1-2") {
		t.Fatalf("retry ref_id = %q, want suffix -1-2", reqs[2].RefID)
	}
	state, _ = orderState(t, e, id)
	if state != StateDelivered {
		t.Fatalf("state after retry = %s, want delivered", state)
	}
}

func TestFastClassTriesAlternates(t *testing.T) {
	e, ik, df, tg := newTestEngine(t)
	id := setupAlternateOrder(t, e, "instant", 36)
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		if r.SKU == "primary" {
			return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Gagal", RC: "03"}, nil
		}
		return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Sukses", SN: "SN-ALT"}, nil
	}

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	if df.topupCount() != 2 {
		t.Fatalf("Topup calls = %d, want 2 (primary + alt2)", df.topupCount())
	}
	reqs := df.requests()
	if reqs[1].SKU != "alt2" {
		t.Fatalf("second SKU = %s, want alt2 (alt1 had no template)", reqs[1].SKU)
	}
	if ik.deliverCount() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", ik.deliverCount())
	}
	state, _ := orderState(t, e, id)
	if state != StateDelivered {
		t.Fatalf("state = %s, want delivered", state)
	}
	if got := len(tg.messages()); got != 0 {
		t.Fatalf("Telegram messages = %d, want 0", got)
	}
}

func TestRegularDoesNotTryAlternates(t *testing.T) {
	e, _, df, tg := newTestEngine(t)
	id := setupAlternateOrder(t, e, "regular", 37)
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Gagal", RC: "03", Message: "down"}, nil
	}

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	if df.topupCount() != 1 {
		t.Fatalf("Topup calls = %d, want 1", df.topupCount())
	}
	state, reason := orderState(t, e, id)
	if state != StateNeedsYou || reason != ReasonAutoFailed {
		t.Fatalf("state/reason = %s/%s, want needs_you/auto_failed", state, reason)
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("Telegram messages = %d, want 1", got)
	}
}

func TestPreBuyCheckCancelled(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "instant", 38)
	mustExec(t, e, `UPDATE orders SET deadline_at = ? WHERE id = ?`, testNow.Unix()+10, id)
	ik.orders[38] = itemku.Order{OrderID: 38, Status: "REFUNDED"}

	if err := e.BuyAuto(context.Background(), id); err != nil {
		t.Fatalf("BuyAuto: %v", err)
	}
	state, _ := orderState(t, e, id)
	if state != StateCancelled {
		t.Fatalf("state = %s, want cancelled", state)
	}
	if df.topupCount() != 0 {
		t.Fatalf("Topup calls = %d, want 0", df.topupCount())
	}
}

func TestManualRetryChecksItemkuFirst(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	id := setupAutoOrder(t, e, "regular", 39)
	ik.orders[39] = itemku.Order{OrderID: 39, Status: "DELIVERED"}

	err := e.BuyManual(context.Background(), id, BuyOpts{})
	if err == nil {
		t.Fatal("BuyManual() error = nil, want refusal")
	}
	if df.topupCount() != 0 {
		t.Fatalf("Topup calls = %d, want 0", df.topupCount())
	}
}

func setupAutoOrder(t *testing.T, e *Engine, class string, productID int64) string {
	t.Helper()
	seedProduct(t, e, productID, "auto", class)
	seedSKU(t, e, productID, 0, "primary")
	seedPrice(t, e, "primary", "seller-a", 12000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	id := "ik-" + itoa(productID)
	insertTestOrder(t, e, id, productID, productID, 1, `{"player_id":"123"}`, testNow.Unix()-60)
	mustExec(t, e, `UPDATE orders SET customer_no = '123', sku_override = 'primary' WHERE id = ?`, id)
	return id
}

func setupAlternateOrder(t *testing.T, e *Engine, class string, productID int64) string {
	t.Helper()
	seedProduct(t, e, productID, "auto", class)
	seedSKU(t, e, productID, 0, "primary")
	seedSKU(t, e, productID, 1, "alt1")
	seedSKU(t, e, productID, 2, "alt2")
	seedPrice(t, e, "primary", "seller-a", 12000, true)
	seedPrice(t, e, "alt1", "seller-b", 12500, true)
	seedPrice(t, e, "alt2", "seller-c", 13000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	seedTemplate(t, e, "seller-c", "MLBB", "{player_id}")
	id := "ik-" + itoa(productID)
	insertTestOrder(t, e, id, productID, productID, 1, `{"player_id":"123"}`, testNow.Unix()-60)
	mustExec(t, e, `UPDATE orders SET customer_no = '123', sku_override = 'primary', order_income = 50000 WHERE id = ?`, id)
	return id
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
