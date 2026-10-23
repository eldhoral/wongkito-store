package itemku

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Token builds the itemku JWT-like authorization token. The payload is the
// exact request body bytes; changing the body invalidates the token.
func Token(apiKey, secret, nonce string, body []byte) string {
	header := fmt.Sprintf(`{"X-Api-Key":%q,"Nonce":%q,"alg":"HS256"}`, clean(apiKey), clean(nonce))
	head := base64.RawURLEncoding.EncodeToString([]byte(header))
	payload := base64.RawURLEncoding.EncodeToString(body)
	signingInput := head + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature
}

func clean(s string) string {
	return strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(strings.TrimSpace(s))
}

// Client is a Tokoku Open API client.
type Client struct {
	BaseURL string
	APIKey  string
	Secret  string
	HTTP    *http.Client
	Now     func() time.Time
	Log     func(endpoint string, req, resp []byte, status int, d time.Duration)
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

type envelope struct {
	Success    bool            `json:"success"`
	StatusCode string          `json:"statusCode"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

func (c *Client) do(ctx context.Context, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("itemku: marshal request: %w", err)
	}
	nonce := strconv.FormatInt(c.now().Unix(), 10)
	url := strings.TrimRight(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("itemku: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", clean(c.APIKey))
	req.Header.Set("Nonce", nonce)
	req.Header.Set("Authorization", "Bearer "+Token(c.APIKey, c.Secret, nonce, payload))

	started := time.Now()
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if c.Log != nil {
			c.Log(path, payload, nil, 0, time.Since(started))
		}
		return fmt.Errorf("itemku: %s: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if c.Log != nil {
		c.Log(path, payload, respBody, resp.StatusCode, time.Since(started))
	}
	if readErr != nil {
		return fmt.Errorf("itemku: read response: %w", readErr)
	}

	var env envelope
	_ = json.Unmarshal(respBody, &env)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{HTTP: resp.StatusCode, Code: env.StatusCode, Message: env.Message}
	}
	if !env.Success {
		msg := env.Message
		if msg == "" {
			msg = strings.TrimSpace(string(respBody))
		}
		return &APIError{HTTP: resp.StatusCode, Code: env.StatusCode, Message: msg}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("itemku: decode %s: %w", path, err)
		}
	}
	return nil
}

// ListOpen returns open (REQUIRE_PROCESS) orders created since the given time.
func (c *Client) ListOpen(ctx context.Context, since time.Time, limit int) ([]Order, error) {
	if limit <= 0 {
		limit = 30
	}
	body := map[string]any{
		"date_start":   since.Format("2006-01-02"),
		"order_status": "REQUIRE_PROCESS",
		"limit":        limit,
	}
	var raw json.RawMessage
	if err := c.do(ctx, "/api/order/list", body, &raw); err != nil {
		return nil, err
	}
	return decodeOrders(raw)
}

// GetOrder returns one order, or nil when itemku does not know it.
func (c *Client) GetOrder(ctx context.Context, orderID int64) (*Order, error) {
	body := map[string]any{
		"date_start": nowDate(c.now().AddDate(0, 0, -59)),
		"order_id":   orderID,
	}
	var raw json.RawMessage
	if err := c.do(ctx, "/api/order/list", body, &raw); err != nil {
		return nil, err
	}
	orders, err := decodeOrders(raw)
	if err != nil {
		return nil, err
	}
	for i := range orders {
		if orders[i].OrderID == orderID {
			return &orders[i], nil
		}
	}
	return nil, nil // never fall back to an order with another id
}

// Deliver marks an itemku order delivered.
func (c *Client) Deliver(ctx context.Context, orderID int64) error {
	return c.do(ctx, "/api/order/action", map[string]any{
		"order_id": orderID,
		"action":   "DELIVER",
	}, nil)
}

// Reject rejects an itemku order with one of the documented refund reasons.
func (c *Client) Reject(ctx context.Context, orderID int64, reason string) error {
	return c.do(ctx, "/api/order/action", map[string]any{
		"order_id":      orderID,
		"action":        "REJECT",
		"refund_reason": reason,
	}, nil)
}

type productPage struct {
	Products    []Product `json:"products"`
	Items       []Product `json:"items"`
	Data        []Product `json:"data"`
	Next        int       `json:"next"`
	NextPage    int       `json:"next_page"`
	CurrentPage int       `json:"current_page"`
	LastPage    int       `json:"last_page"`
}

// ListProducts fetches one page of the itemku catalogue.
func (c *Client) ListProducts(ctx context.Context, page int) ([]Product, int, error) {
	if page <= 0 {
		page = 1
	}
	var raw json.RawMessage
	if err := c.do(ctx, "/api/product/list", map[string]any{"page": page}, &raw); err != nil {
		return nil, 0, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, 0, nil
	}
	if trimmed[0] == '[' {
		var items []Product
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, 0, err
		}
		return items, 0, nil
	}
	var p productPage
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return nil, 0, err
	}
	items := p.Products
	if len(items) == 0 {
		items = p.Items
	}
	if len(items) == 0 {
		items = p.Data
	}
	next := p.Next
	if next == 0 {
		next = p.NextPage
	}
	if next == 0 && p.LastPage > p.CurrentPage {
		next = p.CurrentPage + 1
	}
	return items, next, nil
}

// UpdateStock sets a product's itemku stock. Zero is how a product is turned
// Off; ErrStockLocked is returned for products itemku does not allow us to
// change.
func (c *Client) UpdateStock(ctx context.Context, productID, stock int64) error {
	return c.do(ctx, "/api/product/stock/update", map[string]any{
		"product_id": productID,
		"new_stock":  stock,
	}, nil)
}

func decodeOrders(raw json.RawMessage) ([]Order, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var orders []Order
		if err := json.Unmarshal(trimmed, &orders); err != nil {
			return nil, fmt.Errorf("itemku: decode orders: %w", err)
		}
		return orders, nil
	}
	var wrapper struct {
		Orders []Order `json:"orders"`
		Data   []Order `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return nil, fmt.Errorf("itemku: decode orders: %w", err)
	}
	if len(wrapper.Orders) > 0 {
		return wrapper.Orders, nil
	}
	if len(wrapper.Data) > 0 {
		return wrapper.Data, nil
	}
	var single Order
	if err := json.Unmarshal(trimmed, &single); err == nil && single.OrderID != 0 {
		return []Order{single}, nil
	}
	return nil, nil
}

func nowDate(t time.Time) string {
	return t.Format("2006-01-02")
}
