package itemku

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	GameName            string `json:"game_name"`
	ProductName         string `json:"product_name"`
	Status              string `json:"status"`
	RequiredInformation string `json:"required_information"`
	UsingDeliveryInfo   bool   `json:"using_delivery_info"`
	OrderCreatedEpoch   int64  `json:"order_created_at"`
	BuyerName           string `json:"buyer_name"`
	CategoryName        string `json:"category_name"`
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
