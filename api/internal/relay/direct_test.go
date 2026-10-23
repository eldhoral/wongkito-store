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

	if err := e.Cancel(context.Background(), id); err != nil {
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
