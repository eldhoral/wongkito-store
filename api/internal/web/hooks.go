package web

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"wongkito/api/internal/digiflazz"
)

const maxHookBody = 1 << 20

// itemkuHook verifies the shop header, acks immediately, then processes the
// order ids off the request goroutine because itemku retries slow handlers.
func (s *server) itemkuHook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ItemkuShopID == "" || r.Header.Get("X-itemku") != s.cfg.ItemkuShopID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Data []struct {
			OrderID int64 `json:"order_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxHookBody)).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ids := make([]int64, 0, len(payload.Data))
	for _, order := range payload.Data {
		if order.OrderID != 0 {
			ids = append(ids, order.OrderID)
		}
	}
	w.WriteHeader(http.StatusOK)
	if len(ids) == 0 {
		return
	}
	go func() {
		ctx := context.Background()
		s.hooks.NoteCallback()
		for _, id := range ids {
			// The engine records the error as itemku's last error; the poll retries.
			if err := s.hooks.VerifyAndIntake(ctx, id); err != nil {
				log.Printf("itemku callback order %d: %v", id, err)
			}
		}
	}()
}

// digiflazzHook verifies the raw-body HMAC before parsing anything.
func (s *server) digiflazzHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !digiflazz.VerifyWebhook(s.cfg.DigiflazzWebhookSecret, body, r.Header.Get("X-Hub-Signature")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tx, err := digiflazz.ParseWebhook(body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.hooks.ApplyTx(r.Context(), tx); err != nil {
		http.Error(w, "apply failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
