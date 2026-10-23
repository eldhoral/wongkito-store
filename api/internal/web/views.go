package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"wongkito/api/internal/itemku"
	"wongkito/api/internal/relay"
	"wongkito/api/internal/tmpl"
)

// OrderView is the JSON shape the Queue and History screens consume.
type OrderView struct {
	ID            string            `json:"id"`
	Source        string            `json:"source"`
	OrderNumber   string            `json:"orderNumber"`
	Game          string            `json:"game"`
	Product       string            `json:"product"`
	Quantity      int64             `json:"quantity"`
	Info          map[string]string `json:"info"`
	CustomerNo    string            `json:"customerNo"`
	SKU           string            `json:"sku"`
	Seller        string            `json:"seller"`
	Cost          int64             `json:"cost"`
	Income        int64             `json:"income"`
	Margin        int64             `json:"margin"`
	State         string            `json:"state"`
	Reason        string            `json:"reason"`
	Detail        string            `json:"detail"`
	DeadlineAt    *int64            `json:"deadlineAt"`
	DeadlineClass string            `json:"deadlineClass"`
	CreatedAt     int64             `json:"createdAt"`
	BuyerWA       string            `json:"buyerWa"`
	WhatsAppURL   string            `json:"whatsappUrl,omitempty"`
}

// Attempt mirrors one row of the attempts table for the detail sheet.
type Attempt struct {
	RefID      string `json:"refId"`
	Unit       int64  `json:"unit"`
	AttemptNo  int64  `json:"attemptNo"`
	SKU        string `json:"sku"`
	CustomerNo string `json:"customerNo"`
	Status     string `json:"status"`
	RC         string `json:"rc"`
	Message    string `json:"message"`
	SN         string `json:"sn"`
	Price      int64  `json:"price"`
	CreatedAt  int64  `json:"createdAt"`
}

// SkuOption is one mapped SKU rendered through its own seller template.
type SkuOption struct {
	SKU           string  `json:"sku"`
	Seller        string  `json:"seller"`
	Price         int64   `json:"price"`
	CustomerNo    *string `json:"customerNo"`
	TemplateError *string `json:"templateError"`
	Margin        *int64  `json:"margin"`
}

// OrderDetail adds attempts and retry options to an OrderView.
type OrderDetail struct {
	OrderView
	Attempts []Attempt   `json:"attempts"`
	Options  []SkuOption `json:"options"`
}

// QueueResponse is GET /api/queue.
type QueueResponse struct {
	Needs      []OrderView `json:"needs"`
	Processing []OrderView `json:"processing"`
	Stats      QueueStats  `json:"stats"`
}

type QueueStats struct {
	Balance   *int64 `json:"balance"`
	AutoToday int64  `json:"autoToday"`
	SentToday int64  `json:"sentToday"`
}

// ProductView is one item in a Products group.
type ProductView struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Mode          string   `json:"mode"`
	OffReason     string   `json:"offReason"`
	DeadlineClass string   `json:"deadlineClass"`
	StockLocked   bool     `json:"stockLocked"`
	Cost          int64    `json:"cost"`
	Price         int64    `json:"price"`
	Net           int64    `json:"net"`
	Margin        int64    `json:"margin"`
	SKUs          []string `json:"skus"`
}

// Group is a (game, seller) card on the Products screen.
type Group struct {
	Game     string        `json:"game"`
	Seller   string        `json:"seller"`
	Template string        `json:"template"`
	Items    []ProductView `json:"items"`
}

type PricelistItem struct {
	SKU                 string `json:"sku"`
	ProductName         string `json:"productName"`
	Brand               string `json:"brand"`
	Category            string `json:"category"`
	Type                string `json:"type"`
	Seller              string `json:"seller"`
	Price               int64  `json:"price"`
	Stock               int64  `json:"stock"`
	UnlimitedStock      bool   `json:"unlimitedStock"`
	Multi               bool   `json:"multi"`
	BuyerProductStatus  bool   `json:"buyerProductStatus"`
	SellerProductStatus bool   `json:"sellerProductStatus"`
}

type SettingsView struct {
	MinMargin    int64   `json:"min_margin"`
	FeePct       float64 `json:"fee_pct"`
	BalanceAlert int64   `json:"balance_alert"`
}

type StatusView struct {
	LastCallback  int64        `json:"lastCallback"`
	LastPoll      int64        `json:"lastPoll"`
	LastPriceSync int64        `json:"lastPriceSync"`
	LastErrors    StatusErrors `json:"lastErrors"`
}

type StatusErrors struct {
	Itemku    string `json:"itemku"`
	Digiflazz string `json:"digiflazz"`
	Telegram  string `json:"telegram"`
}

type orderRecord struct {
	ID           string
	Source       string
	OrderNumber  string
	ProductID    sql.NullInt64
	SKUOverride  sql.NullString
	GameName     string
	ProductName  string
	Price        int64
	Quantity     int64
	OrderIncome  int64
	RequiredInfo string
	CustomerNo   string
	BuyerName    string
	BuyerWA      string
	PaidPrice    int64
	State        string
	Reason       string
	Detail       string
	DeadlineAt   sql.NullInt64
	CreatedAt    int64
	UpdatedAt    int64
	DeliveredAt  sql.NullInt64
}

const orderSelect = `SELECT id, source, order_number, product_id, sku_override, game_name, product_name,
	price, quantity, order_income, required_info, customer_no, buyer_name, buyer_wa, paid_price,
	state, reason, detail, deadline_at, created_at, updated_at, delivered_at FROM orders`

func (s *server) loadOrderViews(ctx context.Context, where string, args ...any) ([]OrderView, error) {
	rows, err := s.relay.DB.DB.QueryContext(ctx, orderSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	views := make([]OrderView, 0)
	for rows.Next() {
		var rec orderRecord
		if err := rows.Scan(&rec.ID, &rec.Source, &rec.OrderNumber, &rec.ProductID, &rec.SKUOverride,
			&rec.GameName, &rec.ProductName, &rec.Price, &rec.Quantity, &rec.OrderIncome,
			&rec.RequiredInfo, &rec.CustomerNo, &rec.BuyerName, &rec.BuyerWA, &rec.PaidPrice,
			&rec.State, &rec.Reason, &rec.Detail, &rec.DeadlineAt, &rec.CreatedAt, &rec.UpdatedAt,
			&rec.DeliveredAt); err != nil {
			return nil, err
		}
		view, err := s.viewForOrder(ctx, rec)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, rows.Err()
}

func (s *server) loadOrderRecord(ctx context.Context, id string) (*orderRecord, error) {
	row := s.relay.DB.DB.QueryRowContext(ctx, orderSelect+" WHERE id = ?", id)
	var rec orderRecord
	err := row.Scan(&rec.ID, &rec.Source, &rec.OrderNumber, &rec.ProductID, &rec.SKUOverride,
		&rec.GameName, &rec.ProductName, &rec.Price, &rec.Quantity, &rec.OrderIncome,
		&rec.RequiredInfo, &rec.CustomerNo, &rec.BuyerName, &rec.BuyerWA, &rec.PaidPrice,
		&rec.State, &rec.Reason, &rec.Detail, &rec.DeadlineAt, &rec.CreatedAt, &rec.UpdatedAt,
		&rec.DeliveredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *server) viewForOrder(ctx context.Context, rec orderRecord) (OrderView, error) {
	v := OrderView{
		ID:            rec.ID,
		Source:        rec.Source,
		OrderNumber:   rec.OrderNumber,
		Game:          rec.GameName,
		Product:       rec.ProductName,
		Quantity:      rec.Quantity,
		Info:          map[string]string{},
		CustomerNo:    rec.CustomerNo,
		State:         rec.State,
		Reason:        rec.Reason,
		Detail:        rec.Detail,
		DeadlineClass: "regular",
		CreatedAt:     rec.CreatedAt,
		BuyerWA:       rec.BuyerWA,
		Income:        rec.OrderIncome,
	}
	if v.Quantity < 1 {
		v.Quantity = 1
	}
	if rec.DeadlineAt.Valid {
		deadline := rec.DeadlineAt.Int64
		v.DeadlineAt = &deadline
	}
	if info, err := (itemku.Order{RequiredInformation: rec.RequiredInfo}).Info(); err == nil {
		v.Info = info
	}

	sku := ""
	if rec.SKUOverride.Valid {
		sku = rec.SKUOverride.String
	}
	if rec.ProductID.Valid {
		if err := s.relay.DB.DB.QueryRowContext(ctx,
			`SELECT deadline_class FROM products WHERE id = ?`, rec.ProductID.Int64).Scan(&v.DeadlineClass); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
		if sku == "" {
			_ = s.relay.DB.DB.QueryRowContext(ctx,
				`SELECT buyer_sku_code FROM product_skus WHERE product_id = ? ORDER BY position LIMIT 1`,
				rec.ProductID.Int64).Scan(&sku)
		}
	}

	rows, err := s.relay.DB.DB.QueryContext(ctx,
		`SELECT unit, buyer_sku_code, status, sn, price FROM attempts
		 WHERE order_id = ? ORDER BY unit, attempt_no`, rec.ID)
	if err != nil {
		return v, err
	}
	type unitResult struct {
		unit  int64
		sku   string
		sn    string
		price int64
	}
	var successes []unitResult
	for rows.Next() {
		var unit int64
		var attemptSKU, status, sn string
		var price int64
		if err := rows.Scan(&unit, &attemptSKU, &status, &sn, &price); err != nil {
			rows.Close()
			return v, err
		}
		sku = attemptSKU
		if status == "Sukses" {
			successes = append(successes, unitResult{unit: unit, sku: attemptSKU, sn: sn, price: price})
		}
	}
	if err := rows.Close(); err != nil {
		return v, err
	}
	v.SKU = sku

	var listedPrice int64
	var found bool
	if sku != "" {
		err := s.relay.DB.DB.QueryRowContext(ctx,
			`SELECT seller_name, price FROM pricelist WHERE buyer_sku_code = ?`, sku).Scan(&v.Seller, &listedPrice)
		if err == nil {
			found = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
	}
	if len(successes) > 0 {
		for _, success := range successes {
			v.Cost += success.price
		}
	} else if found {
		v.Cost = listedPrice * v.Quantity
	}
	v.Margin = v.Income - v.Cost

	if rec.Source == "direct" && rec.State == relay.StateDelivered {
		units := make([]relay.OrderUnit, 0, len(successes))
		for _, success := range successes {
			units = append(units, relay.OrderUnit{SN: success.sn})
		}
		text := relay.DeliveryText(relay.OrderView{
			Product:    rec.ProductName,
			CustomerNo: rec.CustomerNo,
			Units:      units,
		})
		v.WhatsAppURL = relay.WhatsAppLink(rec.BuyerWA, text)
	}
	return v, nil
}

func (s *server) attemptsForOrder(ctx context.Context, id string) ([]Attempt, error) {
	rows, err := s.relay.DB.DB.QueryContext(ctx,
		`SELECT ref_id, unit, attempt_no, buyer_sku_code, customer_no, status, rc, message, sn, price, created_at
		 FROM attempts WHERE order_id = ? ORDER BY unit, attempt_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := make([]Attempt, 0)
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.RefID, &a.Unit, &a.AttemptNo, &a.SKU, &a.CustomerNo, &a.Status,
			&a.RC, &a.Message, &a.SN, &a.Price, &a.CreatedAt); err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

func (s *server) optionsForOrder(ctx context.Context, rec *orderRecord) ([]SkuOption, error) {
	options := make([]SkuOption, 0)
	if !rec.ProductID.Valid {
		return options, nil
	}
	var game string
	if err := s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT game_name FROM products WHERE id = ?`, rec.ProductID.Int64).Scan(&game); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return options, nil
		}
		return nil, err
	}
	info, _ := (itemku.Order{RequiredInformation: rec.RequiredInfo}).Info()
	quantity := rec.Quantity
	if quantity < 1 {
		quantity = 1
	}
	unitIncome := rec.OrderIncome / quantity

	rows, err := s.relay.DB.DB.QueryContext(ctx,
		`SELECT buyer_sku_code FROM product_skus WHERE product_id = ? ORDER BY position`,
		rec.ProductID.Int64)
	if err != nil {
		return nil, err
	}
	var skus []string
	for rows.Next() {
		var sku string
		if err := rows.Scan(&sku); err != nil {
			rows.Close()
			return nil, err
		}
		skus = append(skus, sku)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for _, sku := range skus {
		option := SkuOption{SKU: sku}
		var price int64
		err := s.relay.DB.DB.QueryRowContext(ctx,
			`SELECT seller_name, price FROM pricelist WHERE buyer_sku_code = ?`, sku).
			Scan(&option.Seller, &price)
		if err == nil {
			option.Price = price
			margin := unitIncome - price
			option.Margin = &margin
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}

		var template string
		err = s.relay.DB.DB.QueryRowContext(ctx,
			`SELECT template FROM templates WHERE seller_name = ? AND game_name = ?`,
			option.Seller, game).Scan(&template)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if errors.Is(err, sql.ErrNoRows) || template == "" {
			message := fmt.Sprintf("no template for seller %q and game %q", option.Seller, game)
			option.TemplateError = &message
			options = append(options, option)
			continue
		}
		customerNo, renderErr := tmpl.Render(template, info)
		if renderErr != nil {
			message := renderErr.Error()
			option.TemplateError = &message
		} else {
			option.CustomerNo = &customerNo
		}
		options = append(options, option)
	}
	return options, nil
}

func (s *server) queueStats(ctx context.Context) QueueStats {
	var stats QueueStats
	// The balance loop stores it; calling Digiflazz on every 5 s queue poll
	// would flood cek-saldo. Nil (never fetched) shows as unknown, not Rp 0.
	if n, err := strconv.ParseInt(s.settingString(ctx, "balance"), 10, 64); err == nil {
		stats.Balance = &n
	}
	start := startOfWIBDay(s.now())
	_ = s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM orders WHERE source = 'itemku' AND delivered_at >= ?`, start).Scan(&stats.AutoToday)
	_ = s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM orders WHERE source = 'direct' AND delivered_at >= ?`, start).Scan(&stats.SentToday)
	return stats
}

func startOfWIBDay(now time.Time) int64 {
	wib := time.FixedZone("WIB", 7*3600)
	local := now.In(wib)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, wib).Unix()
}

func (s *server) now() time.Time {
	if s.relay != nil && s.relay.Now != nil {
		return s.relay.Now()
	}
	return time.Now()
}

func (s *server) productGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.relay.DB.DB.QueryContext(ctx,
		`SELECT id, name, game_name, itemku_price, mode, off_reason, deadline_class, stock_locked, fee_rate
		 FROM products ORDER BY game_name, name, id`)
	if err != nil {
		return nil, err
	}
	type product struct {
		view    ProductView
		game    string
		feeRate sql.NullFloat64
	}
	var products []product
	for rows.Next() {
		var p product
		var locked int
		if err := rows.Scan(&p.view.ID, &p.view.Name, &p.game, &p.view.Price, &p.view.Mode,
			&p.view.OffReason, &p.view.DeadlineClass, &locked, &p.feeRate); err != nil {
			rows.Close()
			return nil, err
		}
		p.view.StockLocked = locked != 0
		products = append(products, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	skus := make(map[int64][]string)
	rows, err = s.relay.DB.DB.QueryContext(ctx,
		`SELECT product_id, buyer_sku_code FROM product_skus ORDER BY product_id, position`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var productID int64
		var sku string
		if err := rows.Scan(&productID, &sku); err != nil {
			rows.Close()
			return nil, err
		}
		skus[productID] = append(skus[productID], sku)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	type priceRow struct {
		seller string
		price  int64
	}
	prices := make(map[string]priceRow)
	rows, err = s.relay.DB.DB.QueryContext(ctx, `SELECT buyer_sku_code, seller_name, price FROM pricelist`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sku string
		var row priceRow
		if err := rows.Scan(&sku, &row.seller, &row.price); err != nil {
			rows.Close()
			return nil, err
		}
		prices[sku] = row
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	templates := make(map[string]string)
	rows, err = s.relay.DB.DB.QueryContext(ctx, `SELECT seller_name, game_name, template FROM templates`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var seller, game, template string
		if err := rows.Scan(&seller, &game, &template); err != nil {
			rows.Close()
			return nil, err
		}
		templates[seller+"\x00"+game] = template
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	feePct := s.settingFloat(ctx, "fee_pct") / 100
	groups := make(map[string]*Group)
	for _, p := range products {
		p.view.SKUs = skus[p.view.ID]
		if p.view.SKUs == nil {
			p.view.SKUs = []string{}
		}
		seller := ""
		linked := false
		if len(p.view.SKUs) > 0 {
			if row, ok := prices[p.view.SKUs[0]]; ok {
				seller = row.seller
				p.view.Cost = row.price
				linked = true
			}
		}
		rate := feePct
		if p.feeRate.Valid {
			rate = p.feeRate.Float64
		}
		if rate < 0 {
			rate = 0
		}
		if rate > 1 {
			rate = 1
		}
		p.view.Net = int64(math.Floor(float64(p.view.Price) * (1 - rate)))
		if linked {
			p.view.Margin = p.view.Net - p.view.Cost
		}

		key := p.game + "\x00" + seller
		group := groups[key]
		if group == nil {
			group = &Group{Game: p.game, Seller: seller, Template: templates[seller+"\x00"+p.game], Items: []ProductView{}}
			groups[key] = group
		}
		group.Items = append(group.Items, p.view)
	}

	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.Items, func(i, j int) bool {
			if group.Items[i].Name != group.Items[j].Name {
				return group.Items[i].Name < group.Items[j].Name
			}
			return group.Items[i].ID < group.Items[j].ID
		})
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Game != out[j].Game {
			return out[i].Game < out[j].Game
		}
		return out[i].Seller < out[j].Seller
	})
	return out, nil
}

func (s *server) pricelistSearch(ctx context.Context, q string) ([]PricelistItem, error) {
	like := "%" + q + "%"
	rows, err := s.relay.DB.DB.QueryContext(ctx,
		`SELECT buyer_sku_code, product_name, brand, category, type, seller_name, price, stock,
			unlimited_stock, multi, buyer_product_status, seller_product_status
		 FROM pricelist
		 WHERE ? = '' OR buyer_sku_code LIKE ? OR product_name LIKE ? OR brand LIKE ?
		 ORDER BY product_name, buyer_sku_code LIMIT 30`, q, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PricelistItem, 0)
	for rows.Next() {
		var item PricelistItem
		var unlimited, multi, buyerStatus, sellerStatus int
		if err := rows.Scan(&item.SKU, &item.ProductName, &item.Brand, &item.Category, &item.Type,
			&item.Seller, &item.Price, &item.Stock, &unlimited, &multi, &buyerStatus, &sellerStatus); err != nil {
			return nil, err
		}
		item.UnlimitedStock = unlimited != 0
		item.Multi = multi != 0
		item.BuyerProductStatus = buyerStatus != 0
		item.SellerProductStatus = sellerStatus != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *server) settingString(ctx context.Context, key string) string {
	var value string
	if err := s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		return ""
	}
	return value
}

func (s *server) settingFloat(ctx context.Context, key string) float64 {
	n, err := strconv.ParseFloat(s.settingString(ctx, key), 64)
	if err != nil {
		return 0
	}
	return n
}

func (s *server) settingsView(ctx context.Context) SettingsView {
	return SettingsView{
		MinMargin:    int64(s.settingFloat(ctx, "min_margin")),
		FeePct:       s.settingFloat(ctx, "fee_pct"),
		BalanceAlert: int64(s.settingFloat(ctx, "balance_alert")),
	}
}

func (s *server) statusView(ctx context.Context) StatusView {
	at := func(key string) int64 {
		n, _ := strconv.ParseInt(s.settingString(ctx, key), 10, 64)
		return n
	}
	return StatusView{
		LastCallback:  at("last_callback_at"),
		LastPoll:      at("last_poll_at"),
		LastPriceSync: at("last_price_sync_at"),
		LastErrors: StatusErrors{
			Itemku:    s.settingString(ctx, "last_error_itemku"),
			Digiflazz: s.settingString(ctx, "last_error_digiflazz"),
			Telegram:  s.settingString(ctx, "last_error_telegram"),
		},
	}
}
