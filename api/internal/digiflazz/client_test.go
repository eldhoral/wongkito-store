package digiflazz_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wongkito/api/internal/digiflazz"
)

func TestSign(t *testing.T) {
	if got, want := digiflazz.Sign("user", "key123", "ref-1"), "e88d9302efc81d70626daabbd2bfb5d0"; got != want {
		t.Fatalf("Sign(ref) = %q, want %q", got, want)
	}
	if got, want := digiflazz.Sign("user", "key123", "pricelist"), "c10f1cd8a9dd682fd26628048b489ec6"; got != want {
		t.Fatalf("Sign(pricelist) = %q, want %q", got, want)
	}
}

func TestVerifyWebhook(t *testing.T) {
	body := []byte(`{"data":{"ref_id":"r1","status":"Sukses"}}`)
	if !digiflazz.VerifyWebhook("whsecret", body, "sha1=9a030568429a73918423bfa7e6e2a1ae33064ba4") {
		t.Fatal("valid signature rejected")
	}
	altered := []byte(`{"data":{"ref_id":"r1","status":"Gagal"}}`)
	if digiflazz.VerifyWebhook("whsecret", altered, "sha1=9a030568429a73918423bfa7e6e2a1ae33064ba4") {
		t.Fatal("altered body accepted")
	}
	if digiflazz.VerifyWebhook("whsecret", body, "9a030568429a73918423bfa7e6e2a1ae33064ba4") {
		t.Fatal("signature without sha1= accepted")
	}
}

func TestNumAcceptsStringAndNumber(t *testing.T) {
	var v struct {
		Price digiflazz.Num `json:"price"`
		Stock digiflazz.Num `json:"stock"`
	}
	if err := json.Unmarshal([]byte(`{"price":"44210","stock":7}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Price != 44210 || v.Stock != 7 {
		t.Fatalf("got price=%d stock=%d, want 44210/7", v.Price, v.Stock)
	}
	if err := json.Unmarshal([]byte(`{"price":"","stock":""}`), &v); err != nil {
		t.Fatalf("empty strings: %v", err)
	}
	if v.Price != 0 || v.Stock != 0 {
		t.Fatalf("empty strings = %d/%d, want 0/0", v.Price, v.Stock)
	}
}

func TestTopupBody(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		bodies = append(bodies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"ref_id": "ref-1", "status": "Sukses", "sn": "SN1", "price": "10000",
			},
		})
	}))
	defer srv.Close()

	c := &digiflazz.Client{Username: "user", APIKey: "key", BaseURL: srv.URL}
	if _, err := c.Topup(context.Background(), digiflazz.TopupReq{
		SKU: "sku-1", CustomerNo: "cust", RefID: "ref-1", MaxPrice: 50000, Testing: true,
	}); err != nil {
		t.Fatalf("Topup: %v", err)
	}
	if _, err := c.Topup(context.Background(), digiflazz.TopupReq{
		SKU: "sku-1", CustomerNo: "cust", RefID: "ref-2",
	}); err != nil {
		t.Fatalf("Topup without optional fields: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(bodies))
	}
	first := bodies[0]
	if first["username"] != "user" || first["buyer_sku_code"] != "sku-1" || first["customer_no"] != "cust" {
		t.Fatalf("first body = %#v", first)
	}
	if first["sign"] != digiflazz.Sign("user", "key", "ref-1") {
		t.Fatalf("first sign = %v", first["sign"])
	}
	if first["max_price"] != float64(50000) {
		t.Fatalf("max_price = %v, want 50000", first["max_price"])
	}
	if first["testing"] != true {
		t.Fatalf("testing = %v, want true", first["testing"])
	}
	second := bodies[1]
	if _, ok := second["max_price"]; ok {
		t.Fatal("max_price present when zero")
	}
	if _, ok := second["testing"]; ok {
		t.Fatal("testing present when false")
	}
}

func TestBalanceUsesDepoSign(t *testing.T) {
	var gotSign string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotSign, _ = body["sign"].(string)
		if body["cmd"] != "deposit" {
			t.Errorf("cmd = %v, want deposit", body["cmd"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"deposit": 125000}})
	}))
	defer srv.Close()

	c := &digiflazz.Client{Username: "user", APIKey: "key", BaseURL: srv.URL}
	got, err := c.Balance(context.Background())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if got != 125000 {
		t.Fatalf("Balance = %d, want 125000", got)
	}
	if want := digiflazz.Sign("user", "key", "depo"); gotSign != want {
		t.Fatalf("balance sign = %q, want %q", gotSign, want)
	}
}

func TestBalanceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &digiflazz.Client{Username: "user", APIKey: "key", BaseURL: srv.URL}
	if _, err := c.Balance(context.Background()); err == nil {
		t.Fatal("Balance() error = nil, want error")
	}
}

func TestParseWebhook(t *testing.T) {
	body := []byte(`{"data":{"ref_id":"r1","status":"Sukses","sn":"SN1","price":"10000"}}`)
	got, err := digiflazz.ParseWebhook(body)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if got.RefID != "r1" || got.Status != "Sukses" || got.SN != "SN1" || got.Price != 10000 {
		t.Fatalf("ParseWebhook = %#v", got)
	}
	if _, err := digiflazz.ParseWebhook([]byte(`not json`)); err == nil {
		t.Fatal("invalid webhook accepted")
	}
}

func TestTransportErrorReturnsEmptyResult(t *testing.T) {
	c := &digiflazz.Client{Username: "u", APIKey: "k", BaseURL: "http://127.0.0.1:1"}
	got, err := c.Topup(context.Background(), digiflazz.TopupReq{SKU: "s", CustomerNo: "c", RefID: "r"})
	if err == nil {
		t.Fatal("Topup() error = nil, want transport error")
	}
	if got != (digiflazz.TxResult{}) {
		t.Fatalf("TxResult = %#v, want zero value", got)
	}
}

func TestTopupNon2xxWithStatusIsResultError(t *testing.T) {
	body := `{"data":{"ref_id":"r1","status":"Gagal","rc":"44","message":"bad","buyer_last_saldo":"10000.50"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	c := &digiflazz.Client{Username: "u", APIKey: "k", BaseURL: srv.URL}
	_, err := c.Topup(context.Background(), digiflazz.TopupReq{SKU: "A", CustomerNo: "1", RefID: "r1"})
	var resErr *digiflazz.ResultError
	if !errors.As(err, &resErr) {
		t.Fatalf("Topup error = %v, want *ResultError", err)
	}
	got := resErr.Result
	if resErr.HTTPStatus != http.StatusBadRequest || got.Status != "Gagal" || got.RC != "44" || got.BuyerLastSaldo != 10000.50 {
		t.Fatalf("ResultError = %d %#v, want 400 Gagal rc 44 saldo 10000.50", resErr.HTTPStatus, got)
	}
	if msg := err.Error(); !strings.Contains(msg, "400") || !strings.Contains(msg, "44") || !strings.Contains(msg, "bad") {
		t.Fatalf("Error() = %q, want HTTP status, rc and message", msg)
	}
}

func TestTopupNon2xxWithoutStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"data":{"message":"upstream down"}}`)
	}))
	defer srv.Close()

	c := &digiflazz.Client{Username: "u", APIKey: "k", BaseURL: srv.URL}
	_, err := c.Topup(context.Background(), digiflazz.TopupReq{SKU: "A", CustomerNo: "1", RefID: "r1"})
	if err == nil {
		t.Fatal("Topup() error = nil, want transport error when no status is given")
	}
	var resErr *digiflazz.ResultError
	if errors.As(err, &resErr) {
		t.Fatalf("Topup error = %v, want a plain error, not *ResultError", err)
	}
}

func TestPriceListErrorObjectShowsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"rc":"83","message":"Anda sudah mencapai limitasi pengecekan pricelist"}}`))
	}))
	defer srv.Close()

	c := &digiflazz.Client{Username: "user", APIKey: "key", BaseURL: srv.URL}
	_, err := c.PriceList(context.Background())
	if err == nil {
		t.Fatal("PriceList error = nil, want error")
	}
	if got := err.Error(); !strings.Contains(got, "rc 83") || !strings.Contains(got, "limitasi pengecekan pricelist") {
		t.Fatalf("error = %q, want Digiflazz rc and message", got)
	}
}
