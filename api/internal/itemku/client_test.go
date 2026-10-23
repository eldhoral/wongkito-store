package itemku_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"wongkito/api/internal/itemku"
)

func TestTokenMatchesKnownVector(t *testing.T) {
	body := []byte(`{"order_id":1234,"date_start_at":"2023-11-01"}`)
	got := itemku.Token("YourApiKeyProvidedByItemku", "YourApiSecretProvidedByItemku", "1707969441", body)
	want := "eyJYLUFwaS1LZXkiOiJZb3VyQXBpS2V5UHJvdmlkZWRCeUl0ZW1rdSIsIk5vbmNlIjoiMTcwNzk2OTQ0MSIsImFsZyI6IkhTMjU2In0.eyJvcmRlcl9pZCI6MTIzNCwiZGF0ZV9zdGFydF9hdCI6IjIwMjMtMTEtMDEifQ.esZb-pATbYL51BPJf-NP7UvLj46KwauABSRtEQkhyLI"
	if got != want {
		t.Fatalf("Token() = %q, want %q", got, want)
	}
}

func TestRequestIsSignedOverExactBody(t *testing.T) {
	var handlerErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			handlerErr = err
			return
		}
		if r.Header.Get("X-Api-Key") != "key" {
			handlerErr = errors.New("missing X-Api-Key")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			handlerErr = errors.New("wrong content type")
		}
		nonce := r.Header.Get("Nonce")
		want := "Bearer " + itemku.Token("key", "secret", nonce, body)
		if r.Header.Get("Authorization") != want {
			handlerErr = errors.New("authorization does not match body")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	c := &itemku.Client{
		BaseURL: srv.URL,
		APIKey:  "key",
		Secret:  "secret",
		Now:     func() time.Time { return time.Unix(1707969441, 0) },
	}
	if err := c.Deliver(context.Background(), 1234); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
}

func TestUpdateStockLocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":    false,
			"statusCode": "UPDATE_STOCK_REJECTED",
			"message":    "auto delivery product",
		})
	}))
	defer srv.Close()

	c := &itemku.Client{BaseURL: srv.URL, APIKey: "key", Secret: "secret"}
	err := c.UpdateStock(context.Background(), 12, 0)
	if !errors.Is(err, itemku.ErrStockLocked) {
		t.Fatalf("UpdateStock() error = %v, want ErrStockLocked", err)
	}
}

func TestOrderInfo(t *testing.T) {
	o := itemku.Order{RequiredInformation: `{"player_id":"123","zone_id":4567}`}
	got, err := o.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	want := map[string]string{"player_id": "123", "zone_id": "4567"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Info() = %#v, want %#v", got, want)
	}
}

func TestGetOrderNoFallbackToOtherID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    []map[string]any{{"order_id": 999, "status": "REQUIRE_PROCESS"}},
		})
	}))
	defer srv.Close()

	c := &itemku.Client{BaseURL: srv.URL, APIKey: "key", Secret: "secret"}
	got, err := c.GetOrder(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got != nil {
		t.Fatalf("GetOrder(123) = order %d, want nil", got.OrderID)
	}
}

func TestOrderDecodesLenientTypes(t *testing.T) {
	var got []itemku.Order
	raw := `[
		{"order_id":1,"price":15000,"quantity":1,"order_income":13500,"order_created_at":1700000000},
		{"order_id":2,"price":"15000.00","quantity":"2","order_income":"13500","order_created_at":"1700000000"},
		{"order_id":3,"price":"15000","order_income":13500.0,"order_created_at":"2023-11-15 05:13:20"}
	]`
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, o := range got {
		if o.Price != 15000 || o.OrderIncome != 13500 || o.OrderCreatedEpoch != 1700000000 {
			t.Fatalf("order %d = price %d income %d created %d, want 15000/13500/1700000000",
				o.OrderID, o.Price, o.OrderIncome, o.OrderCreatedEpoch)
		}
	}
	if got[1].Quantity != 2 {
		t.Fatalf("quantity = %d, want 2", got[1].Quantity)
	}
	var bad itemku.Order
	if err := json.Unmarshal([]byte(`{"price":"abc"}`), &bad); err == nil {
		t.Fatal("unmarshal(price abc) error = nil, want error")
	}
}
