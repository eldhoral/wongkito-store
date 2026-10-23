package digiflazz

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sign is the md5 hex signature Digiflazz expects: md5(username + apiKey + suffix).
func Sign(username, key, suffix string) string {
	sum := md5.Sum([]byte(username + key + suffix))
	return hex.EncodeToString(sum[:])
}

// Num is a Digiflazz number that may arrive as a JSON number, a numeric
// string, an empty string, or null.
type Num int64

func (n *Num) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == `""` {
		*n = 0
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		s = strings.TrimSpace(text)
	}
	if s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("digiflazz: invalid number %q: %w", s, err)
	}
	*n = Num(v)
	return nil
}

// TxResult is a prepaid transaction (top-up or status re-check) result.
type TxResult struct {
	RefID          string        `json:"ref_id"`
	CustomerNo     string        `json:"customer_no"`
	SKU            string        `json:"buyer_sku_code"`
	Message        string        `json:"message"`
	Status         string        `json:"status"`
	RC             string        `json:"rc"`
	SN             string        `json:"sn"`
	Price          Num           `json:"price"`
	BuyerLastSaldo flexibleFloat `json:"buyer_last_saldo"`
}

// TopupReq is one purchase request. MaxPrice is omitted when zero; Testing
// is sent only when true.
type TopupReq struct {
	SKU        string `json:"buyer_sku_code"`
	CustomerNo string `json:"customer_no"`
	RefID      string `json:"ref_id"`
	MaxPrice   int64  `json:"max_price,omitempty"`
	Testing    bool   `json:"testing,omitempty"`
}

// PriceItem is one row of the Digiflazz prepaid price list.
type PriceItem struct {
	ProductName         string `json:"product_name"`
	Category            string `json:"category"`
	Brand               string `json:"brand"`
	Type                string `json:"type"`
	SellerName          string `json:"seller_name"`
	Price               Num    `json:"price"`
	BuyerSKUCode        string `json:"buyer_sku_code"`
	BuyerProductStatus  bool   `json:"buyer_product_status"`
	SellerProductStatus bool   `json:"seller_product_status"`
	UnlimitedStock      bool   `json:"unlimited_stock"`
	Stock               Num    `json:"stock"`
	Multi               bool   `json:"multi"`
	StartCutOff         string `json:"start_cut_off"`
	EndCutOff           string `json:"end_cut_off"`
	Desc                string `json:"desc"`
}

// Client is a Digiflazz buyer-API client.
type Client struct {
	Username string
	APIKey   string
	BaseURL  string
	HTTP     *http.Client
	Log      func(endpoint string, req, resp []byte, status int, d time.Duration)
}

const defaultBaseURL = "https://api.digiflazz.com/v1"

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("digiflazz: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("digiflazz: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	started := time.Now()
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if c.Log != nil {
			c.Log(path, payload, nil, 0, time.Since(started))
		}
		return nil, fmt.Errorf("digiflazz: %s: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if c.Log != nil {
		c.Log(path, payload, respBody, resp.StatusCode, time.Since(started))
	}
	if readErr != nil {
		return nil, fmt.Errorf("digiflazz: read response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Digiflazz can send a definitive transaction result (e.g. Gagal) with a
		// non-2xx status. Treat it as the result; otherwise the attempt would
		// stay unknown forever.
		if data, ok := finalTxData(respBody); ok {
			return data, nil
		}
		return nil, fmt.Errorf("digiflazz: %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var env struct {
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("digiflazz: decode %s: %w", path, err)
	}
	if len(env.Data) == 0 || bytes.Equal(bytes.TrimSpace(env.Data), []byte("null")) {
		if env.Message != "" {
			return nil, fmt.Errorf("digiflazz: %s: %s", path, env.Message)
		}
		return nil, fmt.Errorf("digiflazz: %s: empty data", path)
	}
	return env.Data, nil
}

// finalTxData returns body's data when it is a transaction with a known status.
func finalTxData(body []byte) (json.RawMessage, bool) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	var tx struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &env) != nil || json.Unmarshal(env.Data, &tx) != nil {
		return nil, false
	}
	switch tx.Status {
	case "Sukses", "Pending", "Gagal":
		return env.Data, true
	}
	return nil, false
}

// Topup buys one unit. Re-sending the same RefID is safe: Digiflazz returns
// the current status of that transaction instead of buying again.
func (c *Client) Topup(ctx context.Context, r TopupReq) (TxResult, error) {
	body := map[string]any{
		"username":       c.Username,
		"buyer_sku_code": r.SKU,
		"customer_no":    r.CustomerNo,
		"ref_id":         r.RefID,
		"sign":           Sign(c.Username, c.APIKey, r.RefID),
	}
	if r.MaxPrice > 0 {
		body["max_price"] = r.MaxPrice
	}
	if r.Testing {
		body["testing"] = true
	}
	raw, err := c.post(ctx, "/transaction", body)
	if err != nil {
		return TxResult{}, err
	}
	var result TxResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return TxResult{}, fmt.Errorf("digiflazz: decode transaction: %w", err)
	}
	return result, nil
}

// PriceList returns the full prepaid price list.
func (c *Client) PriceList(ctx context.Context) ([]PriceItem, error) {
	body := map[string]any{
		"cmd":      "prepaid",
		"username": c.Username,
		"sign":     Sign(c.Username, c.APIKey, "pricelist"),
	}
	raw, err := c.post(ctx, "/price-list", body)
	if err != nil {
		return nil, err
	}
	var items []PriceItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("digiflazz: decode price list: %w", err)
	}
	return items, nil
}

type flexibleFloat float64

func (f *flexibleFloat) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == `""` {
		*f = 0
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		s = strings.TrimSpace(text)
	}
	if s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("digiflazz: invalid float %q: %w", s, err)
	}
	*f = flexibleFloat(v)
	return nil
}

// Balance returns the current deposit. The docs specify the sign suffix as
// "depo" (not "deposit" or "balance").
func (c *Client) Balance(ctx context.Context) (int64, error) {
	body := map[string]any{
		"cmd":      "deposit",
		"username": c.Username,
		"sign":     Sign(c.Username, c.APIKey, "depo"),
	}
	raw, err := c.post(ctx, "/cek-saldo", body)
	if err != nil {
		return 0, err
	}
	var data struct {
		Deposit flexibleFloat `json:"deposit"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, fmt.Errorf("digiflazz: decode balance: %w", err)
	}
	return int64(data.Deposit), nil
}

// VerifyWebhook checks X-Hub-Signature: sha1=<hex HMAC-SHA1(raw body)> in
// constant time.
func VerifyWebhook(secret string, body []byte, sigHeader string) bool {
	const prefix = "sha1="
	if !strings.HasPrefix(sigHeader, prefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(sigHeader, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}

// ParseWebhook reads the prepaid payload from {"data": {...}}.
func ParseWebhook(body []byte) (TxResult, error) {
	var env struct {
		Data TxResult `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return TxResult{}, fmt.Errorf("digiflazz: parse webhook: %w", err)
	}
	return env.Data, nil
}
