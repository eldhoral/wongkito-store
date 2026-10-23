package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Notifier is what the relay needs from Telegram; tests use fakes.
type Notifier interface {
	Send(ctx context.Context, text string, urgent bool) error
}

// Bot sends messages to one Telegram chat.
type Bot struct {
	Token   string
	ChatID  string
	BaseURL string
	HTTP    *http.Client
}

func (b *Bot) httpClient() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (b *Bot) baseURL() string {
	if b.BaseURL == "" {
		return "https://api.telegram.org"
	}
	return strings.TrimRight(b.BaseURL, "/")
}

// Send posts a message. Urgent messages are prefixed with 🚨.
func (b *Bot) Send(ctx context.Context, text string, urgent bool) error {
	if urgent {
		text = "🚨 " + text
	}
	payload, err := json.Marshal(map[string]any{
		"chat_id": b.ChatID,
		"text":    text,
	})
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}
	url := b.baseURL() + "/bot" + b.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(body, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		msg := result.Description
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return fmt.Errorf("telegram: HTTP %d: %s", resp.StatusCode, msg)
	}
	return nil
}
