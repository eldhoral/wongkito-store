package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"wongkito/api/internal/relay"
	"wongkito/api/internal/tmpl"
)

func (s *server) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/queue", s.getQueue)
	mux.HandleFunc("GET /api/orders/{id}", s.getOrder)
	mux.HandleFunc("POST /api/orders/{id}/buy", s.buyOrder)
	mux.HandleFunc("POST /api/orders/{id}/reject", s.rejectOrder)
	mux.HandleFunc("POST /api/orders/{id}/cancel", s.cancelOrder)
	mux.HandleFunc("POST /api/orders/direct", s.createDirect)
	mux.HandleFunc("GET /api/history", s.getHistory)
	mux.HandleFunc("GET /api/products", s.getProducts)
	mux.HandleFunc("PATCH /api/products/{id}", s.patchProduct)
	mux.HandleFunc("POST /api/groups/mode", s.groupMode)
	mux.HandleFunc("PUT /api/templates", s.putTemplate)
	mux.HandleFunc("GET /api/pricelist", s.getPricelist)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("POST /api/settings/telegram-test", s.telegramTest)
	mux.HandleFunc("GET /api/status", s.getStatus)
	mux.HandleFunc("/", s.apiNotFound)
	return mux
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func (s *server) getQueue(w http.ResponseWriter, r *http.Request) {
	needs, err := s.loadOrderViews(r.Context(),
		`WHERE state = ? ORDER BY (deadline_at IS NULL), deadline_at ASC, created_at ASC`,
		relay.StateNeedsYou)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	processing, err := s.loadOrderViews(r.Context(),
		`WHERE state IN (?, ?, ?, ?) ORDER BY created_at DESC`,
		relay.StateNew, relay.StateBuying, relay.StateProcessing, relay.StateDelivering)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, QueueResponse{
		Needs:      needs,
		Processing: processing,
		Stats:      s.queueStats(r.Context()),
	})
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := s.loadOrderRecord(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rec == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
		return
	}
	view, err := s.viewForOrder(r.Context(), *rec)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	attempts, err := s.attemptsForOrder(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	options, err := s.optionsForOrder(r.Context(), rec)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, OrderDetail{OrderView: view, Attempts: attempts, Options: options})
}

func (s *server) buyOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SKU        string `json:"sku"`
		CustomerNo string `json:"customerNo"`
		NoCap      bool   `json:"noCap"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.relay.BuyManual(r.Context(), r.PathValue("id"), relay.BuyOpts{
		SKU:        req.SKU,
		CustomerNo: req.CustomerNo,
		NoCap:      req.NoCap,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) rejectOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
		Force  bool   `json:"force"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Reason == "" {
		req.Reason = "NO_STOCK"
	}
	if err := s.relay.Reject(r.Context(), r.PathValue("id"), req.Reason, req.Force); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	// The body is optional: an empty one means no force.
	if r.ContentLength != 0 && !decodeBody(w, r, &req) {
		return
	}
	if err := s.relay.Cancel(r.Context(), r.PathValue("id"), req.Force); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) createDirect(w http.ResponseWriter, r *http.Request) {
	var req relay.DirectReq
	if !decodeBody(w, r, &req) {
		return
	}
	id, err := s.relay.CreateDirect(r.Context(), req)
	if id == "" {
		if err == nil {
			err = errors.New("direct order was not created")
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// The order exists even if the buy errored. Return its id so the UI follows
	// the order instead of the owner re-submitting (a second buy).
	res := map[string]string{"id": id}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) getHistory(w http.ResponseWriter, r *http.Request) {
	where := "WHERE 1 = 1"
	var args []any
	switch source := r.URL.Query().Get("source"); source {
	case "", "itemku", "direct":
		if source != "" {
			where += " AND source = ?"
			args = append(args, source)
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source must be itemku or direct"})
		return
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		like := "%" + q + "%"
		where += " AND (order_number LIKE ? OR customer_no LIKE ?)"
		args = append(args, like, like)
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before must be a unix timestamp"})
			return
		}
		where += " AND created_at < ?"
		args = append(args, before)
	}
	where += " ORDER BY created_at DESC LIMIT 50"
	views, err := s.loadOrderViews(r.Context(), where, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *server) getProducts(w http.ResponseWriter, r *http.Request) {
	groups, err := s.productGroups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *server) patchProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid product id"})
		return
	}
	var req struct {
		Mode          *string   `json:"mode"`
		DeadlineClass *string   `json:"deadlineClass"`
		SKUs          *[]string `json:"skus"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var currentMode string
	if err := s.relay.DB.DB.QueryRowContext(r.Context(),
		`SELECT mode FROM products WHERE id = ?`, id).Scan(&currentMode); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found"})
		return
	}

	if req.SKUs != nil {
		skus, err := cleanSKUs(*req.SKUs)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if currentMode == "auto" && (req.Mode == nil || *req.Mode == "auto") {
			if err := s.autoReadyForSKUs(r.Context(), id, skus); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		tx, err := s.relay.DB.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM product_skus WHERE product_id = ?`, id); err != nil {
			_ = tx.Rollback()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for position, sku := range skus {
			if _, err := tx.ExecContext(r.Context(),
				`INSERT INTO product_skus (product_id, position, buyer_sku_code) VALUES (?, ?, ?)`,
				id, position, sku); err != nil {
				_ = tx.Rollback()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		if err := tx.Commit(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	if req.DeadlineClass != nil {
		switch *req.DeadlineClass {
		case "regular", "ten_min", "instant":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deadline class"})
			return
		}
		if _, err := s.relay.DB.DB.ExecContext(r.Context(),
			`UPDATE products SET deadline_class = ? WHERE id = ?`, *req.DeadlineClass, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	if req.Mode != nil {
		if err := s.relay.SetMode(r.Context(), id, *req.Mode); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, relay.ErrStockLocked) {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func cleanSKUs(skus []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(skus))
	for _, sku := range skus {
		sku = strings.TrimSpace(sku)
		if sku == "" {
			return nil, fmt.Errorf("SKU cannot be empty")
		}
		if seen[sku] {
			return nil, fmt.Errorf("SKU %s is listed twice", sku)
		}
		seen[sku] = true
		out = append(out, sku)
	}
	return out, nil
}

func (s *server) autoReadyForSKUs(ctx context.Context, productID int64, skus []string) error {
	if len(skus) == 0 {
		return fmt.Errorf("auto needs a primary SKU")
	}
	var game string
	if err := s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT game_name FROM products WHERE id = ?`, productID).Scan(&game); err != nil {
		return err
	}
	var seller string
	if err := s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT seller_name FROM pricelist WHERE buyer_sku_code = ?`, skus[0]).Scan(&seller); err != nil {
		return fmt.Errorf("primary SKU %s is not in the price list", skus[0])
	}
	var template string
	if err := s.relay.DB.DB.QueryRowContext(ctx,
		`SELECT template FROM templates WHERE seller_name = ? AND game_name = ?`, seller, game).Scan(&template); err != nil || template == "" {
		return fmt.Errorf("auto needs a template for seller %q and game %q", seller, game)
	}
	return nil
}

func (s *server) groupMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game   string `json:"game"`
		Seller string `json:"seller"`
		Mode   string `json:"mode"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Game == "" || (req.Mode != "off" && req.Mode != "manual" && req.Mode != "auto") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "game and a valid mode are required"})
		return
	}
	rows, err := s.relay.DB.DB.QueryContext(r.Context(),
		`SELECT p.id, COALESCE(pr.seller_name, '')
		 FROM products p
		 LEFT JOIN product_skus s ON s.product_id = p.id
		   AND s.position = (SELECT MIN(position) FROM product_skus WHERE product_id = p.id)
		 LEFT JOIN pricelist pr ON pr.buyer_sku_code = s.buyer_sku_code
		 WHERE p.game_name = ? ORDER BY p.id`, req.Game)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type item struct {
		ID    int64  `json:"id"`
		Error string `json:"error,omitempty"`
	}
	results := make([]item, 0)
	for rows.Next() {
		var id int64
		var seller string
		if err := rows.Scan(&id, &seller); err != nil {
			rows.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if seller != req.Seller {
			continue
		}
		result := item{ID: id}
		if err := s.relay.SetMode(r.Context(), id, req.Mode); err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	if err := rows.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *server) putTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seller   string `json:"seller"`
		Game     string `json:"game"`
		Template string `json:"template"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Seller == "" || req.Game == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seller and game are required"})
		return
	}
	keys := tmpl.Keys(req.Template)
	if len(keys) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "template needs at least one placeholder"})
		return
	}
	// Render with sample values: catches invalid placeholders and stray braces
	// ("{player_id}|{zone_id" would otherwise send a half-filled number).
	sample := make(map[string]string, len(keys))
	for _, k := range keys {
		sample[k] = "1"
	}
	if _, err := tmpl.Render(req.Template, sample); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, err := s.relay.DB.DB.ExecContext(r.Context(),
		`INSERT INTO templates (seller_name, game_name, template) VALUES (?, ?, ?)
		 ON CONFLICT(seller_name, game_name) DO UPDATE SET template = excluded.template`,
		req.Seller, req.Game, req.Template); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) getPricelist(w http.ResponseWriter, r *http.Request) {
	items, err := s.pricelistSearch(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsView(r.Context()))
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MinMargin    *int64   `json:"min_margin"`
		FeePct       *float64 `json:"fee_pct"`
		BalanceAlert *int64   `json:"balance_alert"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.MinMargin != nil {
		if *req.MinMargin < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "min_margin cannot be negative"})
			return
		}
		if err := s.setSettingValue(r.Context(), "min_margin", strconv.FormatInt(*req.MinMargin, 10)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.FeePct != nil {
		if *req.FeePct < 0 || *req.FeePct > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fee_pct must be between 0 and 100"})
			return
		}
		if err := s.setSettingValue(r.Context(), "fee_pct", strconv.FormatFloat(*req.FeePct, 'f', -1, 64)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.BalanceAlert != nil {
		if *req.BalanceAlert < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "balance_alert cannot be negative"})
			return
		}
		if err := s.setSettingValue(r.Context(), "balance_alert", strconv.FormatInt(*req.BalanceAlert, 10)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, s.settingsView(r.Context()))
}

func (s *server) setSettingValue(ctx context.Context, key, value string) error {
	_, err := s.relay.DB.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *server) telegramTest(w http.ResponseWriter, r *http.Request) {
	if s.relay.TG == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Telegram is not configured"})
		return
	}
	if err := s.relay.TG.Send(r.Context(), "wongkito: Telegram test", false); err != nil {
		// Bot.Send keeps the token out of its errors, so this is safe to show.
		s.relay.NoteError("telegram", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statusView(r.Context()))
}
