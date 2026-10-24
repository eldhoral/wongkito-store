package relay

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
	"wongkito/api/internal/store"
)

var testNow = time.Unix(1_700_000_000, 0)

type fakeIK struct {
	mu             sync.Mutex
	orders         map[int64]itemku.Order
	getOrderErr    error
	listOpen       []itemku.Order
	listOpenErr    error
	deliverCalls   []int64
	deliverErr     error
	rejectCalls    []int64
	rejectReasons  []string
	rejectErr      error
	products       []itemku.Product
	updateStockIDs []int64
	updateStocks   []int64
	updateStockErr error
}

func (f *fakeIK) GetOrder(_ context.Context, orderID int64) (*itemku.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getOrderErr != nil {
		return nil, f.getOrderErr
	}
	o, ok := f.orders[orderID]
	if !ok {
		return nil, nil
	}
	return &o, nil
}

func (f *fakeIK) ListOpen(_ context.Context, _ time.Time, _ int) ([]itemku.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listOpenErr != nil {
		return nil, f.listOpenErr
	}
	return append([]itemku.Order(nil), f.listOpen...), nil
}

func (f *fakeIK) Deliver(_ context.Context, orderID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliverCalls = append(f.deliverCalls, orderID)
	return f.deliverErr
}

func (f *fakeIK) Reject(_ context.Context, orderID int64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectCalls = append(f.rejectCalls, orderID)
	f.rejectReasons = append(f.rejectReasons, reason)
	return f.rejectErr
}

func (f *fakeIK) ListProducts(_ context.Context, _ int) ([]itemku.Product, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]itemku.Product(nil), f.products...), 0, nil
}

func (f *fakeIK) UpdateStock(_ context.Context, productID, stock int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateStockIDs = append(f.updateStockIDs, productID)
	f.updateStocks = append(f.updateStocks, stock)
	return f.updateStockErr
}

func (f *fakeIK) deliverCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deliverCalls)
}

type fakeDF struct {
	mu             sync.Mutex
	topupReqs      []digiflazz.TopupReq
	topupFn        func(digiflazz.TopupReq) (digiflazz.TxResult, error)
	priceList      []digiflazz.PriceItem
	priceListCalls int
	priceListErr   error
	balance        int64
	balanceErr     error
}

func (f *fakeDF) Topup(_ context.Context, r digiflazz.TopupReq) (digiflazz.TxResult, error) {
	f.mu.Lock()
	f.topupReqs = append(f.topupReqs, r)
	fn := f.topupFn
	f.mu.Unlock()
	if fn != nil {
		return fn(r)
	}
	return digiflazz.TxResult{
		RefID: r.RefID, SKU: r.SKU, CustomerNo: r.CustomerNo,
		Status: "Sukses", SN: "SN-" + r.RefID, Price: 10000,
	}, nil
}

func (f *fakeDF) PriceList(_ context.Context) ([]digiflazz.PriceItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.priceListCalls++
	if f.priceListErr != nil {
		return nil, f.priceListErr
	}
	return append([]digiflazz.PriceItem(nil), f.priceList...), nil
}

func (f *fakeDF) Balance(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balance, f.balanceErr
}

func (f *fakeDF) topupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.topupReqs)
}

func (f *fakeDF) requests() []digiflazz.TopupReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]digiflazz.TopupReq(nil), f.topupReqs...)
}

type sentMessage struct {
	text   string
	urgent bool
}

type fakeTG struct {
	mu   sync.Mutex
	sent []sentMessage
	err  error
}

func (f *fakeTG) Send(_ context.Context, text string, urgent bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{text: text, urgent: urgent})
	return f.err
}

func (f *fakeTG) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

// alerts is messages without the informational "New order" notices.
func (f *fakeTG) alerts() []sentMessage {
	var out []sentMessage
	for _, m := range f.messages() {
		if !strings.HasPrefix(m.text, "New order") {
			out = append(out, m)
		}
	}
	return out
}

func newTestEngine(t *testing.T) (*Engine, *fakeIK, *fakeDF, *fakeTG) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ik := &fakeIK{orders: make(map[int64]itemku.Order)}
	df := &fakeDF{}
	tg := &fakeTG{}
	e := &Engine{
		DB:  db,
		IK:  ik,
		DF:  df,
		TG:  tg,
		Now: func() time.Time { return testNow },
	}
	return e, ik, df, tg
}

func mustExec(t *testing.T, e *Engine, query string, args ...any) {
	t.Helper()
	if _, err := e.DB.DB.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func seedProduct(t *testing.T, e *Engine, id int64, mode, class string) {
	t.Helper()
	mustExec(t, e,
		`INSERT INTO products (id, name, game_name, itemku_price, itemku_stock, mode, deadline_class)
		 VALUES (?, 'Product', 'MLBB', 15000, 10, ?, ?)`, id, mode, class)
}

func seedSKU(t *testing.T, e *Engine, productID int64, position int, sku string) {
	t.Helper()
	mustExec(t, e, `INSERT INTO product_skus (product_id, position, buyer_sku_code) VALUES (?, ?, ?)`,
		productID, position, sku)
}

func seedPrice(t *testing.T, e *Engine, sku, seller string, price int64, multi bool) {
	t.Helper()
	mustExec(t, e,
		`INSERT INTO pricelist (buyer_sku_code, product_name, seller_name, price, buyer_product_status,
		 seller_product_status, unlimited_stock, stock, multi)
		 VALUES (?, 'Product', ?, ?, 1, 1, 1, 100, ?)`,
		sku, seller, price, boolInt(multi))
}

func seedTemplate(t *testing.T, e *Engine, seller, game, template string) {
	t.Helper()
	mustExec(t, e, `INSERT INTO templates (seller_name, game_name, template) VALUES (?, ?, ?)`,
		seller, game, template)
}

func insertTestOrder(t *testing.T, e *Engine, id string, itemkuID, productID, quantity int64, info string, created int64) {
	t.Helper()
	mustExec(t, e,
		`INSERT INTO orders (id, source, itemku_order_id, order_number, product_id, game_name, product_name,
		 price, quantity, order_income, required_info, state, created_at, updated_at)
		 VALUES (?, 'itemku', ?, 'OD-1', ?, 'MLBB', 'Product', 15000, ?, 13500, ?, 'new', ?, ?)`,
		id, itemkuID, productID, quantity, info, created, created)
}
