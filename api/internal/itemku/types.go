package itemku

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrStockLocked is returned when itemku refuses to change stock for a
// product it controls (for example an auto-delivery product).
var ErrStockLocked = errors.New("itemku: stock update rejected")

// Order is one itemku order from /api/order/list or the order callback.
type Order struct {
	OrderID             int64  `json:"order_id"`
	OrderNumber         string `json:"order_number"`
	ProductID           int64  `json:"product_id"`
	Price               int64  `json:"price"`
	Quantity            int64  `json:"quantity"`
	OrderIncome         int64  `json:"order_income"`
	AdsFee              int64  `json:"ads_fee"` // Iklan Tokoku fee, charged outside order_income
	GameName            string `json:"game_name"`
	ProductName         string `json:"product_name"`
	Status              string `json:"status"`
	RequiredInformation string `json:"required_information"`
	UsingDeliveryInfo   bool   `json:"using_delivery_info"`
	OrderCreatedEpoch   int64  `json:"order_created_at"`
	BuyerName           string `json:"buyer_name"`
	CategoryName        string `json:"category_name"`
}

// wib is itemku's local time (Asia/Jakarta, UTC+7, no DST). A fixed zone
// avoids depending on tzdata being installed.
var wib = time.FixedZone("WIB", 7*3600)

// UnmarshalJSON decodes the money, quantity and timestamp fields leniently:
// their exact JSON types are unverified, and one mismatch would fail the whole
// order list and stop all intake.
func (o *Order) UnmarshalJSON(data []byte) error {
	type plain Order
	var raw struct {
		plain
		Price       lenientInt  `json:"price"`
		Quantity    lenientInt  `json:"quantity"`
		OrderIncome lenientInt  `json:"order_income"`
		AdsFee      lenientInt  `json:"ads_fee"`
		CreatedAt   lenientTime `json:"order_created_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = Order(raw.plain)
	o.Price = int64(raw.Price)
	o.Quantity = int64(raw.Quantity)
	o.OrderIncome = int64(raw.OrderIncome)
	o.AdsFee = int64(raw.AdsFee)
	o.OrderCreatedEpoch = int64(raw.CreatedAt)
	return nil
}

// lenientInt accepts a JSON number or a numeric string ("15000", "15000.00").
type lenientInt int64

func (n *lenientInt) UnmarshalJSON(data []byte) error {
	s := unquote(data)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		*n = lenientInt(v)
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("itemku: invalid number %q", s)
	}
	*n = lenientInt(math.Round(v))
	return nil
}

// lenientTime accepts epoch seconds (number or string) or a WIB wall-clock
// "2006-01-02 15:04:05" and stores epoch seconds.
type lenientTime int64

func (t *lenientTime) UnmarshalJSON(data []byte) error {
	s := unquote(data)
	if s == "" || s == "null" {
		*t = 0
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		*t = lenientTime(v)
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if v, err := time.ParseInLocation(layout, s, wib); err == nil {
			*t = lenientTime(v.Unix())
			return nil
		}
	}
	return fmt.Errorf("itemku: invalid timestamp %q", s)
}

func unquote(data []byte) string {
	s := strings.TrimSpace(string(data))
	var text string
	if strings.HasPrefix(s, `"`) && json.Unmarshal(data, &text) == nil {
		return strings.TrimSpace(text)
	}
	return s
}

// Info parses required_information (a JSON object encoded as a string) into
// the key/value pairs used by customer-number templates. Scalars are
// stringified.
func (o Order) Info() (map[string]string, error) {
	out := make(map[string]string)
	if o.RequiredInformation == "" {
		return out, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(o.RequiredInformation), &raw); err != nil {
		return nil, fmt.Errorf("itemku: parse required_information: %w", err)
	}
	for key, value := range raw {
		var s string
		if err := json.Unmarshal(value, &s); err == nil {
			out[key] = s
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(value))
		dec.UseNumber()
		var decoded any
		if err := dec.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("itemku: parse required_information[%s]: %w", key, err)
		}
		switch v := decoded.(type) {
		case json.Number:
			out[key] = v.String()
		case bool:
			out[key] = strconv.FormatBool(v)
		case nil:
			out[key] = ""
		default:
			out[key] = string(value)
		}
	}
	return out, nil
}

// Product is one itemku product from /api/product/list.
type Product struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	GameName     string `json:"game_name"`
	ItemTypeName string `json:"item_type_name"`
	Price        int64  `json:"price"`
	Stock        int64  `json:"stock"`
}

// APIError is a non-success response from itemku.
type APIError struct {
	HTTP    int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("itemku: %s (HTTP %d): %s", e.Code, e.HTTP, e.Message)
	}
	return fmt.Sprintf("itemku: HTTP %d: %s", e.HTTP, e.Message)
}

// Unwrap lets callers use errors.Is(err, ErrStockLocked).
func (e *APIError) Unwrap() error {
	if e.Code == "UPDATE_STOCK_REJECTED" {
		return ErrStockLocked
	}
	return nil
}
