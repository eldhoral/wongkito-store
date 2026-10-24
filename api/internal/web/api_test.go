package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"wongkito/api/internal/config"
	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
	"wongkito/api/internal/relay"
	"wongkito/api/internal/store"
)

var apiNow = time.Unix(1_700_000_000, 0)

type testDF struct {
	balance int64
}

func (f *testDF) Topup(_ context.Context, r digiflazz.TopupReq) (digiflazz.TxResult, error) {
	return digiflazz.TxResult{RefID: r.RefID, SKU: r.SKU, CustomerNo: r.CustomerNo, Status: "Sukses", SN: "SN", Price: 10000}, nil
}

func (f *testDF) PriceList(context.Context) ([]digiflazz.PriceItem, error) { return nil, nil }

func (f *testDF) Balance(context.Context) (int64, error) { return f.balance, nil }

type testIK struct {
	updates []int64
}

func (f *testIK) GetOrder(context.Context, int64) (*itemku.Order, error) { return nil, nil }
func (f *testIK) ListOpen(context.Context, time.Time, int) ([]itemku.Order, error) {
	return nil, nil
}
func (f *testIK) Deliver(context.Context, int64) error        { return nil }
func (f *testIK) Reject(context.Context, int64, string) error { return nil }
func (f *testIK) ListProducts(context.Context, int) ([]itemku.Product, int, error) {
	return nil, 0, nil
}
func (f *testIK) UpdateStock(_ context.Context, productID, _ int64) error {
	f.updates = append(f.updates, productID)
	return nil
}

func newAPITestServer(t *testing.T) (*server, *testIK, *testDF) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ik := &testIK{}
	df := &testDF{balance: 123000}
	e := &relay.Engine{DB: db, IK: ik, DF: df, Now: func() time.Time { return apiNow }}
	s := newServer(config.Config{SessionSecret: "test-secret"}, e, nil)
	s.relay = e
	return s, ik, df
}

func apiDo(t *testing.T, s *server, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "s", Value: s.sessionToken(time.Now().Add(time.Hour).Unix())})
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, req)
	return rr
}

func apiExec(t *testing.T, s *server, query string, args ...any) {
	t.Helper()
	if _, err := s.relay.DB.DB.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func seedAPIProduct(t *testing.T, s *server, id int64, game, mode string) {
	t.Helper()
	apiExec(t, s, `INSERT INTO products (id, name, game_name, itemku_price, itemku_stock, mode, deadline_class)
		VALUES (?, 'Product', ?, 23000, 10, ?, 'regular')`, id, game, mode)
}

func seedAPISKU(t *testing.T, s *server, productID int64, position int, sku string) {
	t.Helper()
	apiExec(t, s, `INSERT INTO product_skus (product_id, position, buyer_sku_code) VALUES (?, ?, ?)`,
		productID, position, sku)
}

func seedAPIPrice(t *testing.T, s *server, sku, seller string, price int64) {
	t.Helper()
	apiExec(t, s, `INSERT INTO pricelist (buyer_sku_code, product_name, seller_name, price,
		buyer_product_status, seller_product_status, unlimited_stock, stock, multi)
		VALUES (?, 'Product', ?, ?, 1, 1, 1, 100, 1)`, sku, seller, price)
}

func seedAPITemplate(t *testing.T, s *server, seller, game, template string) {
	t.Helper()
	apiExec(t, s, `INSERT INTO templates (seller_name, game_name, template) VALUES (?, ?, ?)`,
		seller, game, template)
}

func TestQueueSortedByDeadline(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	for _, row := range []struct {
		id       string
		state    string
		source   string
		deadline any
	}{
		{"later", "needs_you", "itemku", apiNow.Unix() + 500},
		{"sooner", "needs_you", "itemku", apiNow.Unix() + 100},
		{"direct", "needs_you", "direct", nil},
	} {
		apiExec(t, s, `INSERT INTO orders (id, source, order_number, game_name, product_name, price,
			quantity, order_income, state, deadline_at, created_at, updated_at)
			VALUES (?, ?, ?, 'MLBB', 'Product', 23000, 1, 21000, ?, ?, ?, ?)`,
			row.id, row.source, row.id, row.state, row.deadline, apiNow.Unix(), apiNow.Unix())
	}

	apiExec(t, s, `INSERT INTO settings (key, value) VALUES ('balance', '123000')`)

	rr := apiDo(t, s, http.MethodGet, "/api/queue", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got QueueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Needs) != 3 {
		t.Fatalf("needs = %d, want 3", len(got.Needs))
	}
	order := []string{got.Needs[0].ID, got.Needs[1].ID, got.Needs[2].ID}
	want := []string{"sooner", "later", "direct"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("needs order = %v, want %v", order, want)
		}
	}
	if got.Stats.Balance == nil || *got.Stats.Balance != 123000 {
		t.Fatalf("balance = %v, want stored 123000", got.Stats.Balance)
	}
}

func TestQueueBalanceUnknownUntilFetched(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	rr := apiDo(t, s, http.MethodGet, "/api/queue", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"balance":null`)) {
		t.Fatalf("body = %s, want balance null (never fetched; the queue must not call cek-saldo)", rr.Body)
	}
}

func TestOrderOptionsRenderPerSellerTemplate(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	seedAPIProduct(t, s, 80, "MLBB", "manual")
	seedAPISKU(t, s, 80, 0, "sku-a")
	seedAPISKU(t, s, 80, 1, "sku-b")
	seedAPIPrice(t, s, "sku-a", "seller-a", 10000)
	seedAPIPrice(t, s, "sku-b", "seller-b", 11000)
	seedAPITemplate(t, s, "seller-a", "MLBB", "{player_id}|{zone_id}")
	seedAPITemplate(t, s, "seller-b", "MLBB", "{player_id}{zone_id}")
	apiExec(t, s, `INSERT INTO orders (id, source, order_number, product_id, game_name, product_name,
		price, quantity, order_income, required_info, state, deadline_at, created_at, updated_at)
		VALUES ('ik-opt', 'itemku', 'OD-OPT', 80, 'MLBB', 'Product', 23000, 1, 21000,
		'{"player_id":"12345678","zone_id":"1234"}', 'needs_you', ?, ?, ?)`,
		apiNow.Unix()+3600, apiNow.Unix(), apiNow.Unix())

	rr := apiDo(t, s, http.MethodGet, "/api/orders/ik-opt", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got OrderDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(got.Options))
	}
	if got.Options[0].CustomerNo == nil || *got.Options[0].CustomerNo != "12345678|1234" {
		t.Fatalf("option A customerNo = %v", got.Options[0].CustomerNo)
	}
	if got.Options[1].CustomerNo == nil || *got.Options[1].CustomerNo != "123456781234" {
		t.Fatalf("option B customerNo = %v", got.Options[1].CustomerNo)
	}
	for _, option := range got.Options {
		if option.TemplateError != nil {
			t.Fatalf("option %s templateError = %v", option.SKU, *option.TemplateError)
		}
	}
}

func TestGroupModeBulk(t *testing.T) {
	s, ik, _ := newAPITestServer(t)
	for i, id := range []int64{81, 82} {
		seedAPIProduct(t, s, id, "MLBB", "auto")
		seedAPISKU(t, s, id, 0, fmt.Sprintf("sku-a%d", i+1))
		seedAPIPrice(t, s, fmt.Sprintf("sku-a%d", i+1), "seller-a", 20000)
	}

	rr := apiDo(t, s, http.MethodPost, "/api/groups/mode", map[string]any{
		"game": "MLBB", "seller": "seller-a", "mode": "off",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	for _, id := range []int64{81, 82} {
		var mode, reason string
		if err := s.relay.DB.DB.QueryRow(`SELECT mode, off_reason FROM products WHERE id = ?`, id).Scan(&mode, &reason); err != nil {
			t.Fatalf("read product %d: %v", id, err)
		}
		if mode != "off" || reason != "manual" {
			t.Fatalf("product %d mode/reason = %s/%s, want off/manual", id, mode, reason)
		}
	}
	if len(ik.updates) != 2 {
		t.Fatalf("stock updates = %v, want both products", ik.updates)
	}
}

func TestTemplateRejectsNoPlaceholder(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	rr := apiDo(t, s, http.MethodPut, "/api/templates", map[string]any{
		"seller": "seller-a", "game": "MLBB", "template": "static text",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	var count int
	if err := s.relay.DB.DB.QueryRow(`SELECT COUNT(*) FROM templates`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("templates = %d, want none", count)
	}
}

func TestCreateDirectBuyErrorKeepsID(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	seedAPIProduct(t, s, 70, "MLBB", "auto")
	seedAPISKU(t, s, 70, 0, "primary")
	seedAPIPrice(t, s, "primary", "seller-a", 10000)
	seedAPITemplate(t, s, "seller-a", "MLBB", "{player_id}")
	apiExec(t, s, `DROP TABLE attempts`) // the buy step fails after the order is inserted

	rr := apiDo(t, s, "POST", "/api/orders/direct", relay.DirectReq{ProductID: 70, Fields: map[string]string{"player_id": "999"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] == "" || got["error"] == "" {
		t.Fatalf("body = %v, want id and error", got)
	}

	rr = apiDo(t, s, "POST", "/api/orders/direct", relay.DirectReq{ProductID: 999})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("pre-insert failure status = %d, want 400", rr.Code)
	}
}

func TestTemplateRejectsStrayBrace(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	for _, template := range []string{"{player_id}|{zone_id", "{player_id}}", "{x|upper}"} {
		rr := apiDo(t, s, http.MethodPut, "/api/templates", map[string]any{
			"seller": "seller-a", "game": "MLBB", "template": template,
		})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("template %q: status = %d, want 400", template, rr.Code)
		}
	}
	rr := apiDo(t, s, http.MethodPut, "/api/templates", map[string]any{
		"seller": "seller-a", "game": "MLBB", "template": "{player_id}|{zone_id}",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("valid template: status = %d, want 200: %s", rr.Code, rr.Body)
	}
}

func TestCancelForceOverridesSpentRefusal(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	apiExec(t, s, `INSERT INTO orders (id, source, order_number, game_name, product_name, price,
		quantity, order_income, state, created_at, updated_at)
		VALUES ('dir-x', 'direct', 'dir-x', 'MLBB', 'Product', 0, 1, 0, 'needs_you', 1, 1)`)
	apiExec(t, s, `INSERT INTO attempts (ref_id, order_id, unit, attempt_no, buyer_sku_code, customer_no,
		max_price, status, created_at, updated_at) VALUES ('r', 'dir-x', 0, 1, 'A', '1', 0, 'Sukses', 1, 1)`)

	if rr := apiDo(t, s, "POST", "/api/orders/dir-x/cancel", nil); rr.Code != http.StatusBadRequest {
		t.Fatalf("unforced cancel = %d, want 400", rr.Code)
	}
	if rr := apiDo(t, s, "POST", "/api/orders/dir-x/cancel", map[string]bool{"force": true}); rr.Code != http.StatusOK {
		t.Fatalf("forced cancel = %d, want 200: %s", rr.Code, rr.Body)
	}
	var state string
	if err := s.relay.DB.DB.QueryRow(`SELECT state FROM orders WHERE id = 'dir-x'`).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("state = %q (%v), want cancelled", state, err)
	}
}

func TestOrderViewSubtractsAdsFee(t *testing.T) {
	s, _, _ := newAPITestServer(t)
	seedAPIProduct(t, s, 85, "MLBB", "manual")
	seedAPISKU(t, s, 85, 0, "sku-a")
	seedAPIPrice(t, s, "sku-a", "seller-a", 10000)
	seedAPITemplate(t, s, "seller-a", "MLBB", "{player_id}")
	apiExec(t, s, `INSERT INTO orders (id, source, order_number, product_id, game_name, product_name,
		price, quantity, order_income, ads_fee, required_info, state, deadline_at, created_at, updated_at)
		VALUES ('ik-ads', 'itemku', 'OD-ADS', 85, 'MLBB', 'Product', 23000, 1, 21000, 5000,
		'{"player_id":"123"}', 'needs_you', ?, ?, ?)`,
		apiNow.Unix()+3600, apiNow.Unix(), apiNow.Unix())

	rr := apiDo(t, s, http.MethodGet, "/api/orders/ik-ads", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got OrderDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AdsFee != 5000 || got.Income != 16000 {
		t.Fatalf("adsFee/income = %d/%d, want 5000/16000", got.AdsFee, got.Income)
	}
	if got.Margin != 6000 {
		t.Fatalf("margin = %d, want 6000 (16000 - 10000)", got.Margin)
	}
	if len(got.Options) != 1 || got.Options[0].Margin == nil || *got.Options[0].Margin != 6000 {
		t.Fatalf("option margin = %v, want 6000", got.Options)
	}
}
