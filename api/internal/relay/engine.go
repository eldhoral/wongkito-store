package relay

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
	"wongkito/api/internal/store"
	"wongkito/api/internal/telegram"
)

// Order states.
const (
	StateNew        = "new"
	StateBuying     = "buying"
	StateProcessing = "processing"
	StateDelivering = "delivering"
	StateDelivered  = "delivered"
	StateNeedsYou   = "needs_you"
	StateRejected   = "rejected"
	StateCancelled  = "cancelled"
)

// Reasons an order can land in needs_you.
const (
	ReasonAutoFailed      = "auto_failed"
	ReasonManualProduct   = "manual_product"
	ReasonPartial         = "partial"
	ReasonMultiBlocked    = "multi_blocked"
	ReasonTemplateError   = "template_error"
	ReasonUnsupportedType = "unsupported_type"
	ReasonDeliverFailed   = "deliver_failed"
	ReasonNoMapping       = "no_mapping"
)

// Itemku is the subset of the Tokoku client the relay uses.
type Itemku interface {
	GetOrder(ctx context.Context, orderID int64) (*itemku.Order, error)
	ListOpen(ctx context.Context, since time.Time, limit int) ([]itemku.Order, error)
	Deliver(ctx context.Context, orderID int64) error
	Reject(ctx context.Context, orderID int64, reason string) error
	ListProducts(ctx context.Context, page int) ([]itemku.Product, int, error)
	UpdateStock(ctx context.Context, productID, stock int64) error
}

// Digiflazz is the subset of the Digiflazz client the relay uses.
type Digiflazz interface {
	Topup(ctx context.Context, r digiflazz.TopupReq) (digiflazz.TxResult, error)
	PriceList(ctx context.Context) ([]digiflazz.PriceItem, error)
	Balance(ctx context.Context) (int64, error)
}

// Engine owns every money-safety rule in the relay.
type Engine struct {
	DB     *store.Store
	IK     Itemku
	DF     Digiflazz
	TG     telegram.Notifier
	Now    func() time.Time
	DryRun bool

	// mu serializes order state transitions. A global lock is deliberate at
	// this volume; per-order locks can replace it if throughput matters.
	mu sync.Mutex
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

type order struct {
	ID                string
	Source            string
	ItemkuOrderID     sql.NullInt64
	OrderNumber       string
	ProductID         sql.NullInt64
	SKUOverride       sql.NullString
	GameName          string
	ProductName       string
	Price             int64
	Quantity          int64
	OrderIncome       int64
	AdsFee            int64
	RequiredInfo      string
	UsingDeliveryInfo bool
	CustomerNo        string
	BuyerName         string
	BuyerWA           string
	PaidPrice         int64
	State             string
	Reason            string
	Detail            string
	DeadlineAt        sql.NullInt64
	CreatedAt         int64
	UpdatedAt         int64
	DeliveredAt       sql.NullInt64
}

const orderColumns = `id, source, itemku_order_id, order_number, product_id, sku_override,
	game_name, product_name, price, quantity, order_income, ads_fee, required_info, using_delivery_info,
	customer_no, buyer_name, buyer_wa, paid_price, state, reason, detail, deadline_at,
	created_at, updated_at, delivered_at`

func (e *Engine) loadOrder(ctx context.Context, id string) (*order, error) {
	row := e.DB.DB.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ?`, id)
	var o order
	var usingDelivery int
	err := row.Scan(
		&o.ID, &o.Source, &o.ItemkuOrderID, &o.OrderNumber, &o.ProductID, &o.SKUOverride,
		&o.GameName, &o.ProductName, &o.Price, &o.Quantity, &o.OrderIncome, &o.AdsFee, &o.RequiredInfo, &usingDelivery,
		&o.CustomerNo, &o.BuyerName, &o.BuyerWA, &o.PaidPrice, &o.State, &o.Reason, &o.Detail, &o.DeadlineAt,
		&o.CreatedAt, &o.UpdatedAt, &o.DeliveredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.UsingDeliveryInfo = usingDelivery != 0
	return &o, nil
}

type productRow struct {
	ID            int64
	Name          string
	GameName      string
	ItemType      string
	ItemkuPrice   int64
	ItemkuStock   int64
	Mode          string
	OffReason     string
	SavedStock    sql.NullInt64
	SavedMode     sql.NullString
	DeadlineClass string
	StockLocked   bool
	FeeRate       sql.NullFloat64
	LastSeller    string
}

func (e *Engine) loadProduct(ctx context.Context, id int64) (*productRow, error) {
	row := e.DB.DB.QueryRowContext(ctx, `SELECT id, name, game_name, item_type, itemku_price, itemku_stock,
		mode, off_reason, saved_stock, saved_mode, deadline_class, stock_locked, fee_rate, last_seller
		FROM products WHERE id = ?`, id)
	var p productRow
	var locked int
	err := row.Scan(&p.ID, &p.Name, &p.GameName, &p.ItemType, &p.ItemkuPrice, &p.ItemkuStock,
		&p.Mode, &p.OffReason, &p.SavedStock, &p.SavedMode, &p.DeadlineClass, &locked, &p.FeeRate, &p.LastSeller)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.StockLocked = locked != 0
	return &p, nil
}

func (e *Engine) deadlineClass(ctx context.Context, productID int64) string {
	var class string
	err := e.DB.DB.QueryRowContext(ctx, `SELECT deadline_class FROM products WHERE id = ?`, productID).Scan(&class)
	if err != nil || class == "" {
		return "regular"
	}
	return class
}

func deadlineDuration(class string) time.Duration {
	switch class {
	case "instant":
		return 2 * time.Minute
	case "ten_min":
		return 10 * time.Minute
	default:
		return 24 * time.Hour
	}
}

func isFinal(state string) bool {
	switch state {
	case StateDelivered, StateRejected, StateCancelled:
		return true
	}
	return false
}

// alert is best-effort: a Telegram outage must never stop the relay. The
// failure is recorded as Telegram's last error.
func (e *Engine) alert(ctx context.Context, text string, urgent bool) {
	if e.TG == nil {
		return
	}
	e.NoteError("telegram", e.TG.Send(ctx, text, urgent))
}

// NoteError records the last error per external service (itemku, digiflazz,
// telegram) for the Settings screen. Errors must already be secret-free; the
// clients never put credentials in error text. It uses its own context so a
// cancelled request still leaves a trace.
func (e *Engine) NoteError(service string, err error) {
	if err == nil || e.DB == nil {
		return
	}
	text := err.Error()
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	value := e.now().In(wibZone).Format("2006-01-02 15:04 WIB") + ": " + text
	_ = e.setSetting(context.Background(), "last_error_"+service, value)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
