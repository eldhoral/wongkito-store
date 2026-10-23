package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds every runtime setting. Secrets are read from the environment
// only; none of them are ever exposed to the browser.
type Config struct {
	ItemkuAPIKey           string
	ItemkuSecret           string
	ItemkuShopID           string
	ItemkuBaseURL          string
	DigiflazzUsername      string
	DigiflazzAPIKey        string
	DigiflazzWebhookSecret string
	TelegramBotToken       string
	TelegramChatID         string
	AdminPasswordHash      string
	SessionSecret          string
	DBPath                 string
	ListenAddr             string
	DryRun                 bool
}

// Load reads configuration from the environment and applies defaults.
func Load() (Config, error) {
	cfg := Config{
		ItemkuAPIKey:           os.Getenv("ITEMKU_API_KEY"),
		ItemkuSecret:           os.Getenv("ITEMKU_SECRET"),
		ItemkuShopID:           os.Getenv("ITEMKU_SHOP_ID"),
		ItemkuBaseURL:          os.Getenv("ITEMKU_BASE_URL"),
		DigiflazzUsername:      os.Getenv("DF_USERNAME"),
		DigiflazzAPIKey:        os.Getenv("DF_API_KEY"),
		DigiflazzWebhookSecret: os.Getenv("DF_WEBHOOK_SECRET"),
		TelegramBotToken:       os.Getenv("TG_BOT_TOKEN"),
		TelegramChatID:         os.Getenv("TG_CHAT_ID"),
		AdminPasswordHash:      os.Getenv("ADMIN_PASSWORD_HASH"),
		SessionSecret:          os.Getenv("SESSION_SECRET"),
		DBPath:                 os.Getenv("DB_PATH"),
		ListenAddr:             os.Getenv("LISTEN_ADDR"),
	}
	if cfg.ItemkuBaseURL == "" {
		cfg.ItemkuBaseURL = "https://tokoku-gateway.itemku.com"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "wongkito.db"
	}
	if cfg.ListenAddr == "" {
		// Localhost only: Caddy fronts it, and clientIP trusts X-Forwarded-For.
		cfg.ListenAddr = "127.0.0.1:8080"
	}
	// An empty secret silently weakens auth (forgeable cookies or webhooks),
	// so refuse to start. TG_* may be empty: alerts are then just not sent.
	var missing []string
	for _, v := range []struct{ name, value string }{
		{"ITEMKU_API_KEY", cfg.ItemkuAPIKey},
		{"ITEMKU_SECRET", cfg.ItemkuSecret},
		{"ITEMKU_SHOP_ID", cfg.ItemkuShopID},
		{"DF_USERNAME", cfg.DigiflazzUsername},
		{"DF_API_KEY", cfg.DigiflazzAPIKey},
		{"DF_WEBHOOK_SECRET", cfg.DigiflazzWebhookSecret},
		{"ADMIN_PASSWORD_HASH", cfg.AdminPasswordHash},
		{"SESSION_SECRET", cfg.SessionSecret},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("config: missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if raw := os.Getenv("DRY_RUN"); raw != "" {
		dry, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.DryRun = dry
	}
	return cfg, nil
}
