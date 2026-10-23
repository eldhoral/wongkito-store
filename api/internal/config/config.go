package config

import (
	"os"
	"strconv"
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
		cfg.ListenAddr = ":8080"
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
