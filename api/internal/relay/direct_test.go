package relay

import (
	"context"
	"strings"
	"testing"

	"wongkito/api/internal/digiflazz"
)

func TestDirectFromProductRendersTemplate(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	seedProduct(t, e, 60, "auto", "regular")
	seedSKU(t, e, 60, 0, "primary")
	seedPrice(t, e, "primary", "seller-a", 10000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}|{zone_id}")

	id, err := e.CreateDirect(context.Background(), DirectReq{
		ProductID: 60,
		Fields:    map[string]string{"player_id": "12345678", "zone_id": "1234"},
		Quantity:  1,
		BuyerName: "Budi",
		BuyerWA:   "0812-3456-7890",
		PaidPrice: 20000,
	})
	if err != nil {
		t.Fatalf("CreateDirect: %v", err)
	}

	o, err := e.loadOrder(context.Background(), id)
	if err != nil {
		t.Fatalf("loadOrder: %v", err)
	}
	if o.State != StateDelivered {
		t.Fatalf("state = %s, want delivered", o.State)
	}
	if o.CustomerNo != "12345678|1234" {
		t.Fatalf("customer_no = %q, want template result", o.CustomerNo)
	}
	if o.Source != "direct" || o.DeadlineAt.Valid {
		t.Fatalf("source/deadline = %s/%v, want direct/NULL", o.Source, o.DeadlineAt)
	}
	if o.SKUOverride.String != "primary" {
		t.Fatalf("sku_override = %q, want primary", o.SKUOverride.String)
	}
	reqs := df.requests()
	if len(reqs) != 1 || reqs[0].CustomerNo != "12345678|1234" || reqs[0].SKU != "primary" {
		t.Fatalf("topups = %#v, want one rendered primary", reqs)
	}
	if reqs[0].MaxPrice != 20000 {
		t.Fatalf("max_price = %d, want paid price 20000", reqs[0].MaxPrice)
	}
	if ik.deliverCount() != 0 {
		t.Fatalf("itemku Deliver calls = %d, want 0", ik.deliverCount())
	}
}

func TestDirectIgnoresOff(t *testing.T) {
	e, _, df, _ := newTestEngine(t)
	seedProduct(t, e, 61, "off", "regular")
	seedSKU(t, e, 61, 0, "primary")
	seedPrice(t, e, "primary", "seller-a", 10000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")

	id, err := e.CreateDirect(context.Background(), DirectReq{
		ProductID: 61,
		Fields:    map[string]string{"player_id": "999"},
	})
	if err != nil {
		t.Fatalf("CreateDirect: %v", err)
	}
	o, err := e.loadOrder(context.Background(), id)
	if err != nil {
		t.Fatalf("loadOrder: %v", err)
	}
	if o.State != StateDelivered {
		t.Fatalf("state = %s, want delivered", o.State)
	}
	if df.topupCount() != 1 {
		t.Fatalf("topups = %d, want 1", df.topupCount())
	}
}

func TestDirectFailCancel(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	seedProduct(t, e, 62, "auto", "regular")
	seedSKU(t, e, 62, 0, "primary")
	seedPrice(t, e, "primary", "seller-a", 10000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	df.topupFn = func(r digiflazz.TopupReq) (digiflazz.TxResult, error) {
		return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, Status: "Gagal", RC: "44", Message: "wrong number"}, nil
	}

	id, err := e.CreateDirect(context.Background(), DirectReq{
		ProductID: 62,
		Fields:    map[string]string{"player_id": "999"},
	})
	if err != nil {
		t.Fatalf("CreateDirect: %v", err)
	}
	o, err := e.loadOrder(context.Background(), id)
	if err != nil {
		t.Fatalf("loadOrder: %v", err)
	}
	if o.State != StateNeedsYou {
		t.Fatalf("state = %s, want needs_you", o.State)
	}

	if err := e.Cancel(context.Background(), id, false); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	o, err = e.loadOrder(context.Background(), id)
	if err != nil {
		t.Fatalf("loadOrder: %v", err)
	}
	if o.State != StateCancelled {
		t.Fatalf("state after cancel = %s, want cancelled", o.State)
	}
	if len(ik.rejectCalls) != 0 || ik.deliverCount() != 0 {
		t.Fatalf("itemku calls = reject %v / deliver %v, want none", ik.rejectCalls, ik.deliverCalls)
	}
}

func TestWhatsAppLink(t *testing.T) {
	got := WhatsAppLink("0812-3456-7890", "Halo Budi")
	wantPrefix := "https://wa.me/6281234567890?text="
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("WhatsAppLink = %q, want prefix %q", got, wantPrefix)
	}
	for _, number := range []string{"+62 812-3456-7890", "62 812 3456 7890"} {
		if got := WhatsAppLink(number, "x"); !strings.HasPrefix(got, wantPrefix) {
			t.Fatalf("WhatsAppLink(%q) = %q, want prefix %q", number, got, wantPrefix)
		}
	}
	if got := WhatsAppLink("0812", "Halo Budi"); !strings.HasSuffix(got, "?text=Halo%20Budi") {
		t.Fatalf("WhatsAppLink = %q, want spaces as %%20", got)
	}
	if got := WhatsAppLink("", "x"); got != "" {
		t.Fatalf("WhatsAppLink(empty) = %q, want empty", got)
	}
}

func TestDeliveryText(t *testing.T) {
	got := DeliveryText(OrderView{
		Product:    "MLBB 100 Diamonds",
		CustomerNo: "12345678|1234",
		Units:      []OrderUnit{{SN: "SN-1"}, {SN: "SN-2"}},
	})
	want := "MLBB 100 Diamonds\nID: 12345678|1234\nSN: SN-1\nSN: SN-2"
	if got != want {
		t.Fatalf("DeliveryText = %q, want %q", got, want)
	}
}

func TestDirectBuyErrorStillReturnsID(t *testing.T) {
	e, _, _, _ := newTestEngine(t)
	seedProduct(t, e, 63, "auto", "regular")
	seedSKU(t, e, 63, 0, "primary")
	seedPrice(t, e, "primary", "seller-a", 10000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	// Break the buy step (after the order insert) so BuyManual errors.
	mustExec(t, e, `DROP TABLE attempts`)

	id, err := e.CreateDirect(context.Background(), DirectReq{
		ProductID: 63,
		Fields:    map[string]string{"player_id": "999"},
	})
	if err == nil {
		t.Fatal("CreateDirect error = nil, want the buy error")
	}
	if id == "" {
		t.Fatalf("id is empty, want the inserted order id (err = %v)", err)
	}
	if o, lerr := e.loadOrder(context.Background(), id); lerr != nil || o == nil {
		t.Fatalf("order %s not stored: %v", id, lerr)
	}
}

func TestDirectSameClientIDBuysOnce(t *testing.T) {
	e, _, df, _ := newTestEngine(t)
	seedProduct(t, e, 64, "auto", "regular")
	seedSKU(t, e, 64, 0, "primary")
	seedPrice(t, e, "primary", "seller-a", 10000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	req := DirectReq{ProductID: 64, Fields: map[string]string{"player_id": "999"}, ClientID: "4f1c2b9e-0d6a-4c1e-9a55-1b2c3d4e5f60"}

	first, err := e.CreateDirect(context.Background(), req)
	if err != nil {
		t.Fatalf("first CreateDirect: %v", err)
	}
	second, err := e.CreateDirect(context.Background(), req)
	if err != nil {
		t.Fatalf("second CreateDirect: %v", err)
	}
	if first != second || first != "dir-"+req.ClientID {
		t.Fatalf("ids = %q / %q, want both dir-<clientId>", first, second)
	}
	var n int
	if err := e.DB.DB.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("orders = %d (%v), want 1", n, err)
	}
	if df.topupCount() != 1 {
		t.Fatalf("Topup calls = %d, want 1", df.topupCount())
	}

	if _, err := e.CreateDirect(context.Background(), DirectReq{ProductID: 64, Fields: req.Fields, ClientID: "bad id!"}); err == nil {
		t.Fatal("CreateDirect(bad clientId) error = nil, want refusal")
	}
}

func TestCancelForce(t *testing.T) {
	e, _, _, _ := newTestEngine(t)
	mustExec(t, e, `INSERT INTO orders (id, source, order_number, game_name, product_name, price, quantity,
		order_income, required_info, state, created_at, updated_at)
		VALUES ('dir-f', 'direct', 'dir-f', 'MLBB', 'Product', 0, 1, 0, '{}', 'needs_you', 1, 1)`)
	insertAttemptRow(t, e, "rf", "dir-f", 0, 1, "primary", "1", "Sukses", 1)

	if err := e.Cancel(context.Background(), "dir-f", false); err == nil {
		t.Fatal("Cancel() error = nil, want refusal while a unit is Sukses")
	}
	if err := e.Cancel(context.Background(), "dir-f", true); err != nil {
		t.Fatalf("forced Cancel: %v", err)
	}
	if state, _ := orderState(t, e, "dir-f"); state != StateCancelled {
		t.Fatalf("state = %s, want cancelled", state)
	}
}
