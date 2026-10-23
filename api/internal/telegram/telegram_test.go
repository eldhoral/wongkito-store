package telegram_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wongkito/api/internal/telegram"
)

func TestSendPostsToChat(t *testing.T) {
	var gotPath, gotChatID, gotText string
	var gotParseMode any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		gotChatID, _ = body["chat_id"].(string)
		gotText, _ = body["text"].(string)
		gotParseMode = body["parse_mode"]
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	bot := &telegram.Bot{Token: "TOKEN", ChatID: "123", BaseURL: srv.URL}
	if err := bot.Send(context.Background(), "hello", true); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/botTOKEN/sendMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotChatID != "123" {
		t.Fatalf("chat_id = %q", gotChatID)
	}
	if gotText != "🚨 hello" {
		t.Fatalf("text = %q, want urgent prefix", gotText)
	}
	if gotParseMode != nil {
		t.Fatalf("parse_mode = %v, want unset", gotParseMode)
	}

	if err := bot.Send(context.Background(), "calm", false); err != nil {
		t.Fatalf("Send calm: %v", err)
	}
	if gotText != "calm" {
		t.Fatalf("calm text = %q", gotText)
	}
}

func TestSendErrorsOnTelegramFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "bad chat"})
	}))
	defer srv.Close()

	bot := &telegram.Bot{Token: "TOKEN", ChatID: "123", BaseURL: srv.URL}
	if err := bot.Send(context.Background(), "hi", false); err == nil {
		t.Fatal("Send() error = nil, want error")
	}
}
