package relay

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"wongkito/api/internal/itemku"
)

func TestIntakeIdempotent(t *testing.T) {
	e, _, _, tg := newTestEngine(t)
	o := itemku.Order{
		OrderID:             1001,
		OrderNumber:         "OD1001",
		ProductID:           999,
		Price:               15000,
		Quantity:            1,
		OrderIncome:         13500,
		GameName:            "MLBB",
		ProductName:         "Product",
		Status:              "REQUIRE_PROCESS",
		RequiredInformation: `{"player_id":"123"}`,
		OrderCreatedEpoch:   testNow.Unix() - 60,
	}

	created, err := e.Intake(context.Background(), o)
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if !created {
		t.Fatal("first Intake created = false")
	}
	created, err = e.Intake(context.Background(), o)
	if err != nil {
		t.Fatalf("second Intake: %v", err)
	}
	if created {
		t.Fatal("second Intake created = true")
	}

	var createdCount int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := e.Intake(context.Background(), o)
			if err != nil {
				t.Errorf("concurrent Intake: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&createdCount, 1)
			}
		}()
	}
	wg.Wait()
	if createdCount != 0 {
		t.Fatalf("concurrent Intake created = %d extra rows, want 0", createdCount)
	}

	var rows int
	if err := e.DB.DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE itemku_order_id = 1001`).Scan(&rows); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if rows != 1 {
		t.Fatalf("orders rows = %d, want 1", rows)
	}
	if got := len(tg.alerts()); got != 1 {
		t.Fatalf("telegram alerts = %d, want 1", got)
	}
}

func TestVerifyAndIntakeSkipsNonOpen(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	ik.orders[5] = itemku.Order{OrderID: 5, Status: "DELIVERED"}

	if err := e.VerifyAndIntake(context.Background(), 5); err != nil {
		t.Fatalf("VerifyAndIntake: %v", err)
	}
	var rows int
	if err := e.DB.DB.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&rows); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if rows != 0 {
		t.Fatalf("orders rows = %d, want 0", rows)
	}
}

func TestRouteManual(t *testing.T) {
	e, _, df, tg := newTestEngine(t)
	seedProduct(t, e, 10, "manual", "regular")
	seedSKU(t, e, 10, 0, "sku-a")
	seedPrice(t, e, "sku-a", "seller-a", 12000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	insertTestOrder(t, e, "ik-10", 10, 10, 1, `{"player_id":"123"}`, testNow.Unix()-60)

	if err := e.Route(context.Background(), "ik-10"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	state, reason := orderState(t, e, "ik-10")
	if state != StateNeedsYou || reason != ReasonManualProduct {
		t.Fatalf("state/reason = %s/%s, want needs_you/manual_product", state, reason)
	}
	if df.topupCount() != 0 {
		t.Fatalf("Topup calls = %d, want 0", df.topupCount())
	}
	if got := len(tg.alerts()); got != 1 {
		t.Fatalf("telegram alerts = %d, want 1", got)
	}
}

func TestRouteTemplateErrorMissingZone(t *testing.T) {
	e, _, df, _ := newTestEngine(t)
	seedProduct(t, e, 11, "auto", "regular")
	seedSKU(t, e, 11, 0, "sku-a")
	seedPrice(t, e, "sku-a", "seller-a", 12000, true)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}|{zone_id}")
	insertTestOrder(t, e, "ik-11", 11, 11, 1, `{"player_id":"123"}`, testNow.Unix()-60)

	if err := e.Route(context.Background(), "ik-11"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	state, reason := orderState(t, e, "ik-11")
	if state != StateNeedsYou || reason != ReasonTemplateError {
		t.Fatalf("state/reason = %s/%s, want needs_you/template_error", state, reason)
	}
	if df.topupCount() != 0 {
		t.Fatalf("Topup calls = %d, want 0", df.topupCount())
	}
}

func TestRouteMultiBlocked(t *testing.T) {
	e, _, df, _ := newTestEngine(t)
	seedProduct(t, e, 12, "auto", "regular")
	seedSKU(t, e, 12, 0, "sku-a")
	seedPrice(t, e, "sku-a", "seller-a", 12000, false)
	seedTemplate(t, e, "seller-a", "MLBB", "{player_id}")
	insertTestOrder(t, e, "ik-12", 12, 12, 2, `{"player_id":"123"}`, testNow.Unix()-60)

	if err := e.Route(context.Background(), "ik-12"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	state, reason := orderState(t, e, "ik-12")
	if state != StateNeedsYou || reason != ReasonMultiBlocked {
		t.Fatalf("state/reason = %s/%s, want needs_you/multi_blocked", state, reason)
	}
	if df.topupCount() != 0 {
		t.Fatalf("Topup calls = %d, want 0", df.topupCount())
	}
}

func TestDeadlineFromClass(t *testing.T) {
	e, _, _, _ := newTestEngine(t)
	seedProduct(t, e, 20, "manual", "instant")
	o := itemku.Order{
		OrderID:             20,
		OrderNumber:         "OD20",
		ProductID:           20,
		Quantity:            1,
		GameName:            "MLBB",
		ProductName:         "Product",
		Status:              "REQUIRE_PROCESS",
		RequiredInformation: `{"player_id":"123"}`,
		OrderCreatedEpoch:   testNow.Unix(),
	}
	if _, err := e.Intake(context.Background(), o); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	var deadline int64
	if err := e.DB.DB.QueryRow(`SELECT deadline_at FROM orders WHERE id = 'ik-20'`).Scan(&deadline); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if want := testNow.Unix() + 120; deadline != want {
		t.Fatalf("deadline_at = %d, want %d", deadline, want)
	}
}

func orderState(t *testing.T, e *Engine, id string) (string, string) {
	t.Helper()
	var state, reason string
	if err := e.DB.DB.QueryRow(`SELECT state, reason FROM orders WHERE id = ?`, id).Scan(&state, &reason); err != nil {
		t.Fatalf("load order state: %v", err)
	}
	return state, reason
}

func TestIntakeAnnouncesNewOrderOnce(t *testing.T) {
	e, _, _, tg := newTestEngine(t)
	o := itemku.Order{
		OrderID: 3001, OrderNumber: "OD3001", ProductID: 999, Price: 23000, Quantity: 2,
		OrderIncome: 41400, AdsFee: 5000, GameName: "MLBB", ProductName: "86 Diamonds",
		Status: "REQUIRE_PROCESS", RequiredInformation: `{"zone_id":"1234","player_id":"12345678"}`,
		OrderCreatedEpoch: testNow.Unix() - 60,
	}
	for i := 0; i < 2; i++ {
		if _, err := e.Intake(context.Background(), o); err != nil {
			t.Fatalf("Intake: %v", err)
		}
	}
	var announced []string
	for _, m := range tg.messages() {
		if strings.HasPrefix(m.text, "New order") {
			announced = append(announced, m.text)
		}
	}
	if len(announced) != 1 {
		t.Fatalf("new-order messages = %q, want exactly 1", announced)
	}
	for _, want := range []string{"OD3001", "MLBB", "86 Diamonds", "x2", "Rp 36400", "player_id 12345678 · zone_id 1234"} {
		if !strings.Contains(announced[0], want) {
			t.Fatalf("message %q missing %q", announced[0], want)
		}
	}
}
