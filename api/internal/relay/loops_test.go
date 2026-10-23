package relay

import (
	"context"
	"strings"
	"testing"
	"time"

	"wongkito/api/internal/itemku"
)

func TestPollIntakesMissing(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	ik.listOpen = []itemku.Order{
		{OrderID: 9001, OrderNumber: "OD-9001", ProductID: 70, GameName: "MLBB", ProductName: "Product",
			Price: 15000, Quantity: 1, OrderIncome: 13500, Status: "REQUIRE_PROCESS",
			OrderCreatedEpoch: testNow.Unix()},
		{OrderID: 9002, OrderNumber: "OD-9002", ProductID: 70, GameName: "MLBB", ProductName: "Product",
			Price: 15000, Quantity: 1, OrderIncome: 13500, Status: "REQUIRE_PROCESS",
			OrderCreatedEpoch: testNow.Unix()},
	}

	if err := e.PollOrders(context.Background()); err != nil {
		t.Fatalf("PollOrders: %v", err)
	}
	var count int
	if err := e.DB.DB.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&count); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if count != 2 {
		t.Fatalf("orders = %d, want 2", count)
	}
}

func TestReminderOnceEach(t *testing.T) {
	e, _, _, tg := newTestEngine(t)
	seedProduct(t, e, 71, "manual", "regular")
	deadline := testNow.Unix() + 24*3600
	insertTestOrder(t, e, "ik-remind", 9101, 71, 1, "{}", testNow.Unix())
	mustExec(t, e, `UPDATE orders SET state = ?, deadline_at = ? WHERE id = ?`,
		StateNeedsYou, deadline, "ik-remind")

	now := time.Unix(deadline-3*3600-59*60, 0)
	e.Now = func() time.Time { return now }

	if err := e.Reminders(context.Background()); err != nil {
		t.Fatalf("Reminders at 3h59m: %v", err)
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("alerts at 3h59m = %d, want 1", got)
	}
	if err := e.Reminders(context.Background()); err != nil {
		t.Fatalf("Reminders repeat: %v", err)
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("alerts after repeat = %d, want still 1", got)
	}

	now = time.Unix(deadline-59*60, 0)
	if err := e.Reminders(context.Background()); err != nil {
		t.Fatalf("Reminders at 59m: %v", err)
	}
	if got := len(tg.messages()); got != 2 {
		t.Fatalf("alerts at 59m = %d, want 2", got)
	}
}

func TestBalanceAlertDeduped(t *testing.T) {
	e, _, df, tg := newTestEngine(t)
	df.balance = 50000
	mustExec(t, e, `INSERT INTO settings (key, value) VALUES ('balance_alert', '100000')`)

	for i := 0; i < 3; i++ {
		if err := e.CheckBalance(context.Background()); err != nil {
			t.Fatalf("CheckBalance %d: %v", i, err)
		}
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("balance alerts = %d, want 1", got)
	}
}

func TestCallbackSilent(t *testing.T) {
	e, ik, _, tg := newTestEngine(t)
	last := testNow.Add(-20 * time.Minute).Unix()
	mustExec(t, e, `INSERT INTO settings (key, value) VALUES ('last_callback_at', ?)`, last)
	ik.listOpen = []itemku.Order{{
		OrderID: 9201, OrderNumber: "OD-9201", ProductID: 72, GameName: "MLBB",
		ProductName: "Product", Price: 15000, Quantity: 1, OrderIncome: 13500,
		Status: "REQUIRE_PROCESS", OrderCreatedEpoch: testNow.Unix(),
	}}

	if err := e.PollOrders(context.Background()); err != nil {
		t.Fatalf("PollOrders: %v", err)
	}
	var silent int
	for _, msg := range tg.messages() {
		if strings.Contains(msg.text, "callback") {
			silent++
		}
	}
	if silent != 1 {
		t.Fatalf("callback_silent alerts = %d, want 1 (messages %#v)", silent, tg.messages())
	}
}

func TestPollCancelsRefundedOverdueOrder(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	seedProduct(t, e, 73, "auto", "regular")
	created := testNow.Add(-25 * time.Hour).Unix()
	insertTestOrder(t, e, "ik-refunded", 9301, 73, 1, "{}", created)
	mustExec(t, e, `UPDATE orders SET state = ?, deadline_at = ? WHERE id = ?`,
		StateProcessing, testNow.Unix()-3600, "ik-refunded")
	ik.orders[9301] = itemku.Order{OrderID: 9301, Status: "REFUNDED"}

	if err := e.PollOrders(context.Background()); err != nil {
		t.Fatalf("PollOrders: %v", err)
	}
	o, err := e.loadOrder(context.Background(), "ik-refunded")
	if err != nil {
		t.Fatalf("loadOrder: %v", err)
	}
	if o.State != StateCancelled {
		t.Fatalf("state = %s, want cancelled", o.State)
	}
}
