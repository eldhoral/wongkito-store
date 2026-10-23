package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
)

// ErrStockLocked means itemku refused the stock change. The product is flagged
// stock_locked and the owner has to turn it off in the itemku dashboard.
var ErrStockLocked = fmt.Errorf("relay: itemku stock update rejected: %w", itemku.ErrStockLocked)

// ErrAutoNotReady means Auto was requested without the data Auto needs.
var ErrAutoNotReady = errors.New("relay: auto needs a primary SKU and a template")

var wibZone = time.FixedZone("WIB", 7*3600)

// SyncProducts pages itemku's product list into products. It never touches
// mode, so a sync cannot undo an owner's choice.
func (e *Engine) SyncProducts(ctx context.Context) error {
	page := 1
	for page > 0 {
		products, next, err := e.IK.ListProducts(ctx, page)
		if err != nil {
			return fmt.Errorf("relay: list itemku products page %d: %w", page, err)
		}
		for _, p := range products {
			if _, err := e.DB.DB.ExecContext(ctx,
				`INSERT INTO products (id, name, game_name, item_type, itemku_price, itemku_stock)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT(id) DO UPDATE SET
				   name = excluded.name,
				   game_name = excluded.game_name,
				   item_type = excluded.item_type,
				   itemku_price = excluded.itemku_price,
				   itemku_stock = excluded.itemku_stock`,
				p.ID, p.Name, p.GameName, p.ItemTypeName, p.Price, p.Stock); err != nil {
				return fmt.Errorf("relay: upsert itemku product %d: %w", p.ID, err)
			}
		}
		if next <= page {
			break
		}
		page = next
	}
	return nil
}

// SyncPriceList replaces the cached Digiflazz price list atomically, then
// re-evaluates every product against it.
func (e *Engine) SyncPriceList(ctx context.Context) error {
	items, err := e.DF.PriceList(ctx)
	if err != nil {
		return fmt.Errorf("relay: fetch digiflazz price list: %w", err)
	}

	tx, err := e.DB.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM pricelist`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO pricelist (buyer_sku_code, product_name, category, brand, type, seller_name,
		 price, buyer_product_status, seller_product_status, unlimited_stock, stock, multi,
		 start_cut_off, end_cut_off, synced_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := e.now().Unix()
	for _, item := range items {
		if _, err := stmt.ExecContext(ctx,
			item.BuyerSKUCode, item.ProductName, item.Category, item.Brand, item.Type, item.SellerName,
			int64(item.Price), boolInt(item.BuyerProductStatus), boolInt(item.SellerProductStatus),
			boolInt(item.UnlimitedStock), int64(item.Stock), boolInt(item.Multi),
			item.StartCutOff, item.EndCutOff, now); err != nil {
			return fmt.Errorf("relay: cache price item %s: %w", item.BuyerSKUCode, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return e.evaluateProducts(ctx)
}

// SetMode applies an owner action: off, manual or auto.
func (e *Engine) SetMode(ctx context.Context, productID int64, mode string) error {
	if mode != "off" && mode != "manual" && mode != "auto" {
		return fmt.Errorf("relay: unknown product mode %q", mode)
	}
	p, err := e.loadProduct(ctx, productID)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("relay: product %d not found", productID)
	}

	if mode == "auto" {
		if err := e.autoReady(ctx, p); err != nil {
			return err
		}
	}

	if mode == "off" {
		// Already manually off: no stock call and no change to what was saved.
		if p.Mode == "off" && p.OffReason == "manual" {
			return nil
		}
		if err := e.IK.UpdateStock(ctx, p.ID, 0); err != nil {
			if errors.Is(err, itemku.ErrStockLocked) {
				if err := e.flagStockLocked(ctx, p.ID); err != nil {
					return err
				}
				return fmt.Errorf("relay: product %d: %w", p.ID, ErrStockLocked)
			}
			return err
		}
		if p.Mode == "off" {
			// It was off automatically; keep the stock saved at that time.
			_, err = e.DB.DB.ExecContext(ctx,
				`UPDATE products SET off_reason = 'manual', stock_locked = 0 WHERE id = ?`, p.ID)
			return err
		}
		_, err = e.DB.DB.ExecContext(ctx,
			`UPDATE products SET saved_stock = ?, saved_mode = ?, mode = 'off', off_reason = 'manual',
			 stock_locked = 0 WHERE id = ?`,
			p.ItemkuStock, p.Mode, p.ID)
		return err
	}

	// manual / auto
	if p.Mode == "off" {
		stock := p.ItemkuStock
		if p.SavedStock.Valid {
			stock = p.SavedStock.Int64
		}
		if err := e.IK.UpdateStock(ctx, p.ID, stock); err != nil {
			if errors.Is(err, itemku.ErrStockLocked) {
				if err := e.flagStockLocked(ctx, p.ID); err != nil {
					return err
				}
				return fmt.Errorf("relay: product %d: %w", p.ID, ErrStockLocked)
			}
			return err
		}
	}
	_, err = e.DB.DB.ExecContext(ctx,
		`UPDATE products SET mode = ?, off_reason = '', stock_locked = 0 WHERE id = ?`,
		mode, p.ID)
	return err
}

func (e *Engine) autoReady(ctx context.Context, p *productRow) error {
	sku, err := e.primarySKU(ctx, p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("relay: product %d: %w", p.ID, ErrAutoNotReady)
	}
	if err != nil {
		return err
	}
	template, seller, err := e.primaryTemplate(ctx, p.ID, sku)
	if err != nil {
		return err
	}
	if template == "" || seller == "" {
		return fmt.Errorf("relay: product %d: %w", p.ID, ErrAutoNotReady)
	}
	return nil
}

func (e *Engine) flagStockLocked(ctx context.Context, productID int64) error {
	_, err := e.DB.DB.ExecContext(ctx, `UPDATE products SET stock_locked = 1 WHERE id = ?`, productID)
	return err
}

// evaluateProducts runs after each price-list sync. It turns products off when
// their primary SKU is unavailable, restores products that it turned off, and
// drops Auto to Manual when the seller behind the SKU changes.
func (e *Engine) evaluateProducts(ctx context.Context) error {
	products, err := e.loadCatalogProducts(ctx)
	if err != nil {
		return err
	}
	prices, err := e.loadPriceList(ctx)
	if err != nil {
		return err
	}
	primaries, err := e.loadPrimarySKUs(ctx)
	if err != nil {
		return err
	}

	now := e.now().In(wibZone)
	minMargin := e.settingInt(ctx, "min_margin")

	for i := range products {
		p := &products[i]
		sku, ok := primaries[p.ID]
		if !ok {
			sku = ""
		}
		item, listed := prices[sku]
		seller := ""
		if sku != "" && listed {
			seller = item.SellerName
		}

		// Seller change: the (seller, game) template pair may now be wrong.
		if seller != "" && seller != p.LastSeller {
			if p.LastSeller != "" && autoLike(p) {
				if p.Mode == "auto" {
					if _, err := e.DB.DB.ExecContext(ctx,
						`UPDATE products SET mode = 'manual', off_reason = '' WHERE id = ?`, p.ID); err != nil {
						return err
					}
					p.Mode = "manual"
				} else {
					if _, err := e.DB.DB.ExecContext(ctx,
						`UPDATE products SET saved_mode = 'manual' WHERE id = ?`, p.ID); err != nil {
						return err
					}
					p.SavedMode = sql.NullString{String: "manual", Valid: true}
				}
				e.alert(ctx, fmt.Sprintf("%s — %s: Digiflazz seller changed from %s to %s; auto is now manual",
					p.GameName, p.Name, p.LastSeller, seller), false)
			}
			if _, err := e.DB.DB.ExecContext(ctx,
				`UPDATE products SET last_seller = ? WHERE id = ?`, seller, p.ID); err != nil {
				return err
			}
			p.LastSeller = seller
		}

		// A product the owner turned off by hand is never touched again.
		if p.Mode == "off" && !strings.HasPrefix(p.OffReason, "auto:") {
			continue
		}

		reason := e.unavailableReason(ctx, p, sku, item, listed, now, minMargin)
		if reason == "" {
			if p.Mode == "off" {
				if err := e.restoreProduct(ctx, p); err != nil {
					return err
				}
			}
			continue
		}

		if p.Mode == "off" {
			continue // already off for this reason (or stock-locked)
		}

		if p.StockLocked {
			if p.Mode != "manual" {
				if _, err := e.DB.DB.ExecContext(ctx,
					`UPDATE products SET mode = 'manual', off_reason = '', saved_stock = NULL,
					 saved_mode = NULL WHERE id = ?`, p.ID); err != nil {
					return err
				}
				e.alert(ctx, fmt.Sprintf("%s — %s: stock is locked by itemku; turn it off in the itemku dashboard",
					p.GameName, p.Name), true)
			}
			continue
		}

		if err := e.IK.UpdateStock(ctx, p.ID, 0); err != nil {
			if errors.Is(err, itemku.ErrStockLocked) {
				if err := e.flagStockLocked(ctx, p.ID); err != nil {
					return err
				}
				if _, err := e.DB.DB.ExecContext(ctx,
					`UPDATE products SET mode = 'manual', off_reason = '', saved_stock = NULL,
					 saved_mode = NULL WHERE id = ?`, p.ID); err != nil {
					return err
				}
				e.alert(ctx, fmt.Sprintf("%s — %s: itemku rejected the stock change (%s); turn it off in the itemku dashboard",
					p.GameName, p.Name, reason), true)
				continue
			}
			return fmt.Errorf("relay: turn off product %d: %w", p.ID, err)
		}

		if _, err := e.DB.DB.ExecContext(ctx,
			`UPDATE products SET saved_stock = ?, saved_mode = ?, mode = 'off', off_reason = ?,
			 stock_locked = 0 WHERE id = ?`,
			p.ItemkuStock, p.Mode, "auto:"+reason, p.ID); err != nil {
			return err
		}
		e.alert(ctx, fmt.Sprintf("%s — %s: turned off (%s)", p.GameName, p.Name, reason), false)
	}
	return nil
}

func (e *Engine) restoreProduct(ctx context.Context, p *productRow) error {
	stock := p.ItemkuStock
	if p.SavedStock.Valid {
		stock = p.SavedStock.Int64
	}
	mode := "manual"
	if p.SavedMode.Valid && (p.SavedMode.String == "auto" || p.SavedMode.String == "manual") {
		mode = p.SavedMode.String
	}
	if err := e.IK.UpdateStock(ctx, p.ID, stock); err != nil {
		if errors.Is(err, itemku.ErrStockLocked) {
			if err := e.flagStockLocked(ctx, p.ID); err != nil {
				return err
			}
			if _, err := e.DB.DB.ExecContext(ctx,
				`UPDATE products SET mode = 'manual', off_reason = '', saved_stock = NULL,
				 saved_mode = NULL WHERE id = ?`, p.ID); err != nil {
				return err
			}
			e.alert(ctx, fmt.Sprintf("%s — %s: itemku rejected restoring stock; turn it off in the itemku dashboard",
				p.GameName, p.Name), true)
			return nil
		}
		return fmt.Errorf("relay: restore product %d: %w", p.ID, err)
	}
	if _, err := e.DB.DB.ExecContext(ctx,
		`UPDATE products SET mode = ?, off_reason = '', stock_locked = 0 WHERE id = ?`,
		mode, p.ID); err != nil {
		return err
	}
	e.alert(ctx, fmt.Sprintf("%s — %s: back on (%s, stock %d)", p.GameName, p.Name, mode, stock), false)
	return nil
}

func autoLike(p *productRow) bool {
	if p.Mode == "auto" {
		return true
	}
	return p.Mode == "off" && p.SavedMode.Valid && p.SavedMode.String == "auto"
}

func (e *Engine) unavailableReason(ctx context.Context, p *productRow, sku string,
	item digiflazz.PriceItem, listed bool, now time.Time, minMargin int64) string {
	if sku == "" {
		return "no_sku"
	}
	if !listed {
		return "not_listed"
	}
	if !item.BuyerProductStatus {
		return "buyer_status"
	}
	if !item.SellerProductStatus {
		return "seller_status"
	}
	if !item.UnlimitedStock && int64(item.Stock) == 0 {
		return "no_stock"
	}
	if inCutOff(now, item.StartCutOff, item.EndCutOff) {
		return "cut_off"
	}
	if int64(item.Price) > e.netUnit(ctx, p)-minMargin {
		return "no_margin"
	}
	return ""
}

// netUnit is what one itemku sale is worth to us after itemku's fee.
func (e *Engine) netUnit(ctx context.Context, p *productRow) int64 {
	rate := e.settingFloat(ctx, "fee_pct") / 100
	if p.FeeRate.Valid {
		rate = p.FeeRate.Float64
	}
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	return int64(math.Floor(float64(p.ItemkuPrice) * (1 - rate)))
}

// LearnFee derives the itemku fee from a real order and stores it per product.
func (e *Engine) LearnFee(ctx context.Context, o itemku.Order) error {
	revenue := o.Price * o.Quantity
	if revenue <= 0 || o.ProductID == 0 {
		return nil
	}
	fee := 1 - float64(o.OrderIncome)/float64(revenue)
	if fee < 0 {
		fee = 0
	}
	if fee > 1 {
		fee = 1
	}
	_, err := e.DB.DB.ExecContext(ctx, `UPDATE products SET fee_rate = ? WHERE id = ?`, fee, o.ProductID)
	return err
}

func (e *Engine) loadCatalogProducts(ctx context.Context) ([]productRow, error) {
	rows, err := e.DB.DB.QueryContext(ctx, `SELECT id, name, game_name, item_type, itemku_price, itemku_stock,
		mode, off_reason, saved_stock, saved_mode, deadline_class, stock_locked, fee_rate, last_seller
		FROM products ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []productRow
	for rows.Next() {
		var p productRow
		var locked int
		if err := rows.Scan(&p.ID, &p.Name, &p.GameName, &p.ItemType, &p.ItemkuPrice, &p.ItemkuStock,
			&p.Mode, &p.OffReason, &p.SavedStock, &p.SavedMode, &p.DeadlineClass, &locked,
			&p.FeeRate, &p.LastSeller); err != nil {
			return nil, err
		}
		p.StockLocked = locked != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (e *Engine) loadPriceList(ctx context.Context) (map[string]digiflazz.PriceItem, error) {
	rows, err := e.DB.DB.QueryContext(ctx, `SELECT buyer_sku_code, product_name, category, brand, type,
		seller_name, price, buyer_product_status, seller_product_status, unlimited_stock, stock,
		multi, start_cut_off, end_cut_off FROM pricelist`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]digiflazz.PriceItem)
	for rows.Next() {
		var item digiflazz.PriceItem
		var buyerStatus, sellerStatus, unlimited, multi int
		var price, stock int64
		if err := rows.Scan(&item.BuyerSKUCode, &item.ProductName, &item.Category, &item.Brand, &item.Type,
			&item.SellerName, &price, &buyerStatus, &sellerStatus, &unlimited, &stock,
			&multi, &item.StartCutOff, &item.EndCutOff); err != nil {
			return nil, err
		}
		item.Price = digiflazz.Num(price)
		item.Stock = digiflazz.Num(stock)
		item.BuyerProductStatus = buyerStatus != 0
		item.SellerProductStatus = sellerStatus != 0
		item.UnlimitedStock = unlimited != 0
		item.Multi = multi != 0
		out[item.BuyerSKUCode] = item
	}
	return out, rows.Err()
}

func (e *Engine) loadPrimarySKUs(ctx context.Context) (map[int64]string, error) {
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT product_id, buyer_sku_code FROM product_skus ORDER BY product_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var productID int64
		var sku string
		if err := rows.Scan(&productID, &sku); err != nil {
			return nil, err
		}
		if _, ok := out[productID]; !ok {
			out[productID] = sku
		}
	}
	return out, rows.Err()
}

// inCutOff reports whether now is inside [start, end). The window may wrap
// midnight. Empty or invalid times mean "no cut-off".
func inCutOff(now time.Time, start, end string) bool {
	a, okA := clockMinutes(start)
	b, okB := clockMinutes(end)
	if !okA || !okB || a == b {
		return false
	}
	nowMinutes := now.Hour()*60 + now.Minute()
	if a < b {
		return nowMinutes >= a && nowMinutes < b
	}
	return nowMinutes >= a || nowMinutes < b
}

func clockMinutes(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func (e *Engine) settingFloat(ctx context.Context, key string) float64 {
	var value string
	if err := e.DB.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		return 0
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return n
}
