package relay

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
)

var wib = time.FixedZone("WIB", 7*3600)

func TestAutoOffWhenNoMargin(t *testing.T) {
	e, ik, df, tg := newTestEngine(t)
	seedCatalogProduct(t, e, 50, "auto", 23000, 10, 0.05, "seller-a")
	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-a", 22000, true, true, true, 100, "", "")}

	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("SyncPriceList: %v", err)
	}
	if len(ik.updateStockIDs) != 1 || ik.updateStocks[0] != 0 || ik.updateStockIDs[0] != 50 {
		t.Fatalf("stock calls = %v/%v, want product 50 -> 0", ik.updateStockIDs, ik.updateStocks)
	}
	p := loadProductRow(t, e, 50)
	if p.Mode != "off" || p.OffReason != "auto:no_margin" {
		t.Fatalf("mode/off_reason = %s/%s, want off/auto:no_margin", p.Mode, p.OffReason)
	}
	if !p.SavedStock.Valid || p.SavedStock.Int64 != 10 {
		t.Fatalf("saved_stock = %v, want 10", p.SavedStock)
	}
	if !p.SavedMode.Valid || p.SavedMode.String != "auto" {
		t.Fatalf("saved_mode = %v, want auto", p.SavedMode)
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("Telegram messages = %d, want 1", got)
	}
}

func TestAutoBackOnRestoresStock(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	seedCatalogProduct(t, e, 51, "auto", 23000, 10, 0.05, "seller-a")
	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-a", 22000, true, true, true, 100, "", "")}
	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("first SyncPriceList: %v", err)
	}

	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-a", 21000, true, true, true, 100, "", "")}
	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("second SyncPriceList: %v", err)
	}
	if len(ik.updateStocks) != 2 || ik.updateStocks[1] != 10 {
		t.Fatalf("stock calls = %v, want second call restoring 10", ik.updateStocks)
	}
	p := loadProductRow(t, e, 51)
	if p.Mode != "auto" || p.OffReason != "" {
		t.Fatalf("mode/off_reason = %s/%s, want auto/empty", p.Mode, p.OffReason)
	}
	if !p.SavedStock.Valid {
		t.Fatal("saved_stock was not cleared")
	}
}

func TestManualOffStaysOff(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	seedCatalogProduct(t, e, 52, "auto", 23000, 10, 0.05, "seller-a")
	if err := e.SetMode(context.Background(), 52, "off"); err != nil {
		t.Fatalf("SetMode(off): %v", err)
	}
	if len(ik.updateStockIDs) != 1 {
		t.Fatalf("stock calls after manual off = %d, want 1", len(ik.updateStockIDs))
	}
	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-a", 20000, true, true, true, 100, "", "")}
	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("SyncPriceList: %v", err)
	}
	if len(ik.updateStockIDs) != 1 {
		t.Fatalf("stock calls after sync = %d, want still 1", len(ik.updateStockIDs))
	}
	p := loadProductRow(t, e, 52)
	if p.Mode != "off" || p.OffReason != "manual" {
		t.Fatalf("mode/off_reason = %s/%s, want off/manual", p.Mode, p.OffReason)
	}
}

func TestCutOffWrapsMidnight(t *testing.T) {
	e, ik, df, _ := newTestEngine(t)
	seedCatalogProduct(t, e, 53, "auto", 23000, 10, 0, "seller-a")
	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-a", 20000, true, true, true, 100, "23:00", "01:00")}

	e.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 30, 0, 0, wib) }
	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("00:30 SyncPriceList: %v", err)
	}
	p := loadProductRow(t, e, 53)
	if p.Mode != "off" || p.OffReason != "auto:cut_off" {
		t.Fatalf("00:30 mode/reason = %s/%s, want off/auto:cut_off", p.Mode, p.OffReason)
	}

	e.Now = func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, wib) }
	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("01:00 SyncPriceList: %v", err)
	}
	p = loadProductRow(t, e, 53)
	if p.Mode != "auto" || p.OffReason != "" {
		t.Fatalf("01:00 mode/reason = %s/%s, want auto/empty", p.Mode, p.OffReason)
	}
	if len(ik.updateStocks) != 2 || ik.updateStocks[1] != 10 {
		t.Fatalf("stock calls = %v, want restore 10", ik.updateStocks)
	}
}

func TestStockLockedDropsToManual(t *testing.T) {
	e, ik, df, tg := newTestEngine(t)
	seedCatalogProduct(t, e, 54, "auto", 23000, 10, 0.05, "seller-a")
	ik.updateStockErr = itemku.ErrStockLocked

	err := e.SetMode(context.Background(), 54, "off")
	if !errors.Is(err, ErrStockLocked) {
		t.Fatalf("SetMode(off) error = %v, want ErrStockLocked", err)
	}
	p := loadProductRow(t, e, 54)
	if !p.StockLocked {
		t.Fatal("stock_locked = false, want true")
	}
	if len(ik.updateStockIDs) != 1 {
		t.Fatalf("stock calls after failed SetMode = %d, want 1", len(ik.updateStockIDs))
	}

	ik.updateStockErr = nil
	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-a", 22000, true, true, true, 100, "", "")}
	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("SyncPriceList: %v", err)
	}
	if len(ik.updateStockIDs) != 1 {
		t.Fatalf("stock calls after unavailable sync = %d, want still 1", len(ik.updateStockIDs))
	}
	p = loadProductRow(t, e, 54)
	if p.Mode != "manual" {
		t.Fatalf("mode = %s, want manual", p.Mode)
	}
	msgs := tg.messages()
	if len(msgs) != 1 || !msgs[0].urgent {
		t.Fatalf("messages = %#v, want one urgent", msgs)
	}
}

func TestSellerChangeDropsAuto(t *testing.T) {
	e, _, df, tg := newTestEngine(t)
	seedCatalogProduct(t, e, 55, "auto", 23000, 10, 0, "seller-a")
	df.priceList = []digiflazz.PriceItem{priceItem("primary", "seller-b", 20000, true, true, true, 100, "", "")}

	if err := e.SyncPriceList(context.Background()); err != nil {
		t.Fatalf("SyncPriceList: %v", err)
	}
	p := loadProductRow(t, e, 55)
	if p.Mode != "manual" {
		t.Fatalf("mode = %s, want manual", p.Mode)
	}
	if p.LastSeller != "seller-b" {
		t.Fatalf("last_seller = %s, want seller-b", p.LastSeller)
	}
	if got := len(tg.messages()); got != 1 {
		t.Fatalf("Telegram messages = %d, want 1", got)
	}
}

func TestLearnFee(t *testing.T) {
	e, _, _, _ := newTestEngine(t)
	seedProduct(t, e, 56, "auto", "regular")
	o := itemku.Order{ProductID: 56, Price: 10000, Quantity: 2, OrderIncome: 18000}
	if err := e.LearnFee(context.Background(), o); err != nil {
		t.Fatalf("LearnFee: %v", err)
	}
	p := loadProductRow(t, e, 56)
	if !p.FeeRate.Valid || math.Abs(p.FeeRate.Float64-0.1) > 1e-9 {
		t.Fatalf("fee_rate = %v, want 0.1", p.FeeRate)
	}
}

func TestSyncProductsUpsertsWithoutTouchingMode(t *testing.T) {
	e, ik, _, _ := newTestEngine(t)
	seedProduct(t, e, 57, "auto", "regular")
	ik.products = []itemku.Product{{
		ID: 57, Name: "Renamed", GameName: "MLBB", ItemTypeName: "Topup", Price: 16000, Stock: 7,
	}}
	if err := e.SyncProducts(context.Background()); err != nil {
		t.Fatalf("SyncProducts: %v", err)
	}
	p := loadProductRow(t, e, 57)
	if p.Name != "Renamed" || p.ItemkuPrice != 16000 || p.ItemkuStock != 7 {
		t.Fatalf("product = %#v, want renamed/16000/7", p)
	}
	if p.Mode != "auto" {
		t.Fatalf("mode = %s, want auto unchanged", p.Mode)
	}
}

func seedCatalogProduct(t *testing.T, e *Engine, id int64, mode string, itemkuPrice, stock int64, fee float64, lastSeller string) {
	t.Helper()
	seedProduct(t, e, id, mode, "regular")
	mustExec(t, e,
		`UPDATE products SET itemku_price = ?, itemku_stock = ?, fee_rate = ?, last_seller = ? WHERE id = ?`,
		itemkuPrice, stock, fee, lastSeller, id)
	seedSKU(t, e, id, 0, "primary")
}

func priceItem(sku, seller string, price int64, buyerStatus, sellerStatus, unlimited bool, stock int64, start, end string) digiflazz.PriceItem {
	return digiflazz.PriceItem{
		BuyerSKUCode: sku, ProductName: "Product", SellerName: seller,
		Price: digiflazz.Num(price), Stock: digiflazz.Num(stock),
		BuyerProductStatus: buyerStatus, SellerProductStatus: sellerStatus,
		UnlimitedStock: unlimited, Multi: true,
		StartCutOff: start, EndCutOff: end,
	}
}

func loadProductRow(t *testing.T, e *Engine, id int64) *productRow {
	t.Helper()
	p, err := e.loadProduct(context.Background(), id)
	if err != nil {
		t.Fatalf("loadProduct: %v", err)
	}
	if p == nil {
		t.Fatalf("product %d not found", id)
	}
	return p
}
