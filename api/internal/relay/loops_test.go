package relay

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
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

func TestRunWaitsForInFlightTicks(t *testing.T) {
	e, _, _, _ := newTestEngine(t)
	var finished atomic.Bool
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	e.run(ctx, []loop{{"slow", "", time.Hour, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
		return nil
	}}})
	if !finished.Load() {
		t.Fatal("run returned before the in-flight tick finished")
	}
}

func TestFailingPollRecordsLastErrorAndAlerts(t *testing.T) {
	e, ik, _, tg := newTestEngine(t)
	ik.listOpenErr = errors.New("itemku: HTTP 502: bad gateway")
	poll := loop{name: "poll orders", service: "itemku", fn: e.PollOrders}

	// Last success 1 minute ago: record the error, no alert yet.
	mustExec(t, e, `INSERT INTO settings (key, value) VALUES ('last_poll_at', ?)`, testNow.Unix()-60)
	e.runTick(context.Background(), poll)
	var got string
	if err := e.DB.DB.QueryRow(`SELECT value FROM settings WHERE key = 'last_error_itemku'`).Scan(&got); err != nil {
		t.Fatalf("last_error_itemku: %v", err)
	}
	if !strings.Contains(got, "bad gateway") || !strings.Contains(got, "WIB") {
		t.Fatalf("last_error_itemku = %q, want time and error text", got)
	}
	if n := len(tg.messages()); n != 0 {
		t.Fatalf("alerts after 1 min = %d, want 0", n)
	}

	// Failing for over 5 minutes: alert once.
	mustExec(t, e, `UPDATE settings SET value = ? WHERE key = 'last_poll_at'`, testNow.Unix()-6*60)
	e.runTick(context.Background(), poll)
	e.runTick(context.Background(), poll)
	if msgs := tg.messages(); len(msgs) != 1 || !strings.Contains(msgs[0].text, "poll has been failing") {
		t.Fatalf("messages = %#v, want one poll-failing alert", msgs)
	}
}

func TestCheckBalanceStoresBalance(t *testing.T) {
	e, _, df, _ := newTestEngine(t)
	df.balance = 250000
	if err := e.CheckBalance(context.Background()); err != nil {
		t.Fatalf("CheckBalance: %v", err)
	}
	if got := e.settingInt(context.Background(), "balance"); got != 250000 {
		t.Fatalf("stored balance = %d, want 250000", got)
	}
	if got := e.settingInt(context.Background(), "balance_at"); got != testNow.Unix() {
		t.Fatalf("balance_at = %d, want %d", got, testNow.Unix())
	}
}

func TestPriceListFetchedAtMostOncePerInterval(t *testing.T) {
	e, _, df, _ := newTestEngine(t)
	df.priceListErr = errors.New("rc 83 limit")
	now := testNow
	e.Now = func() time.Time { return now }

	_ = e.syncPriceListIfDue(context.Background()) // startup: fetches, even though it fails
	_ = e.syncPriceListIfDue(context.Background()) // restart right after: skipped
	if df.priceListCalls != 1 {
		t.Fatalf("price list calls = %d, want 1 within the interval", df.priceListCalls)
	}
	now = now.Add(catalogInterval)
	_ = e.syncPriceListIfDue(context.Background())
	if df.priceListCalls != 2 {
		t.Fatalf("price list calls = %d, want 2 after the interval", df.priceListCalls)
	}
}
