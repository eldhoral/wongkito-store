package relay

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"wongkito/api/internal/tmpl"
)

// DirectReq is an owner-entered WhatsApp order. Give either ProductID (to use
// that product's primary template and fields) or SKU plus a final CustomerNo.
type DirectReq struct {
	ProductID  int64             `json:"productId"`
	SKU        string            `json:"sku"`
	Fields     map[string]string `json:"fields"`
	CustomerNo string            `json:"customerNo"`
	Quantity   int64             `json:"quantity"`
	BuyerName  string            `json:"buyerName"`
	BuyerWA    string            `json:"buyerWa"`
	PaidPrice  int64             `json:"paidPrice"`
	// ClientID is the form's idempotency key. A retried submit with the same
	// key returns the existing order instead of buying again.
	ClientID string `json:"clientId"`
}

var clientIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// CreateDirect inserts a direct order and buys it immediately. The owner is the
// confirmation, so product mode and Off do not apply.
func (e *Engine) CreateDirect(ctx context.Context, r DirectReq) (string, error) {
	quantity := r.Quantity
	if quantity == 0 {
		quantity = 1
	}
	if quantity < 0 {
		return "", fmt.Errorf("relay: direct quantity must be positive")
	}
	if r.ClientID != "" && !clientIDRE.MatchString(r.ClientID) {
		return "", fmt.Errorf("relay: invalid clientId")
	}

	fields := r.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	requiredInfo, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}

	var (
		p           *productRow
		productID   any
		gameName    string
		productName string
	)
	sku := strings.TrimSpace(r.SKU)
	customerNo := strings.TrimSpace(r.CustomerNo)

	if r.ProductID > 0 {
		p, err = e.loadProduct(ctx, r.ProductID)
		if err != nil {
			return "", err
		}
		if p == nil {
			return "", fmt.Errorf("relay: product %d not found", r.ProductID)
		}
		productID = p.ID
		gameName = p.GameName
		productName = p.Name
		if sku == "" {
			sku, err = e.primarySKU(ctx, p.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("relay: product %d has no primary SKU", p.ID)
			}
			if err != nil {
				return "", err
			}
		}
		if customerNo == "" {
			template, seller, err := e.primaryTemplate(ctx, p.ID, sku)
			if err != nil {
				return "", err
			}
			if template == "" {
				return "", fmt.Errorf("relay: no template for seller %q and game %q", seller, p.GameName)
			}
			customerNo, err = tmpl.Render(template, fields)
			if err != nil {
				return "", err
			}
		}
	} else {
		if sku == "" || customerNo == "" {
			return "", fmt.Errorf("relay: direct order needs ProductID or SKU and customer number")
		}
		err = e.DB.DB.QueryRowContext(ctx,
			`SELECT product_name FROM pricelist WHERE buyer_sku_code = ?`, sku).Scan(&productName)
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("relay: SKU %s is not in the price list", sku)
		}
		if err != nil {
			return "", err
		}
	}
	if sku == "" || customerNo == "" {
		return "", fmt.Errorf("relay: direct order has no SKU or customer number")
	}

	id := "dir-" + r.ClientID
	if r.ClientID == "" {
		if id, err = newDirectID(); err != nil {
			return "", err
		}
	}
	unitPrice := int64(0)
	if r.PaidPrice > 0 {
		unitPrice = r.PaidPrice / quantity
	}
	result, err := e.DB.DB.ExecContext(ctx,
		`INSERT INTO orders (id, source, itemku_order_id, order_number, product_id, sku_override,
		 game_name, product_name, price, quantity, order_income, required_info, using_delivery_info,
		 customer_no, buyer_name, buyer_wa, paid_price, state, reason, detail, deadline_at,
		 created_at, updated_at)
		 VALUES (?, 'direct', NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, 'new', '', '', NULL, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		id, id, productID, sku, gameName, productName, unitPrice, quantity, r.PaidPrice,
		string(requiredInfo), customerNo, r.BuyerName, r.BuyerWA, r.PaidPrice,
		e.now().Unix(), e.now().Unix())
	if err != nil {
		return "", fmt.Errorf("relay: insert direct order: %w", err)
	}
	if inserted, err := result.RowsAffected(); err != nil {
		return "", err
	} else if inserted == 0 {
		return id, nil // same submit again: follow the existing order, never buy twice
	}

	if err := e.BuyManual(ctx, id, BuyOpts{SKU: sku, CustomerNo: customerNo}); err != nil {
		return id, err
	}
	return id, nil
}

func newDirectID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "dir-" + hex.EncodeToString(raw[:]), nil
}

// OrderView is the relay-local subset DeliveryText needs. The web layer has
// its own JSON view; this keeps relay from importing web.
type OrderView struct {
	Product    string
	CustomerNo string
	Units      []OrderUnit
}

// OrderUnit is one successful unit's delivery data.
type OrderUnit struct {
	SN string
}

// DeliveryText is the WhatsApp hand-off message for a delivered direct order.
func DeliveryText(o OrderView) string {
	lines := []string{o.Product, "ID: " + o.CustomerNo}
	for _, unit := range o.Units {
		lines = append(lines, "SN: "+unit.SN)
	}
	return strings.Join(lines, "\n")
}

// WhatsAppLink normalises an Indonesian number to 62... and builds a wa.me
// link. An empty number yields an empty link (the UI copies the text instead).
func WhatsAppLink(buyerWA, text string) string {
	number := normalizeWhatsApp(buyerWA)
	if number == "" {
		return ""
	}
	// %20, not '+': wa.me does not decode '+' as a space in every client.
	return "https://wa.me/" + number + "?text=" + strings.ReplaceAll(url.QueryEscape(text), "+", "%20")
}

func normalizeWhatsApp(raw string) string {
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	number := digits.String()
	switch {
	case number == "":
		return ""
	case strings.HasPrefix(number, "0"):
		number = "62" + number[1:]
	case strings.HasPrefix(number, "62"):
		// Already in international form.
	case strings.HasPrefix(number, "8"):
		number = "62" + number
	}
	return number
}
