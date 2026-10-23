package config

import (
	"strings"
	"testing"
)

var required = map[string]string{
	"ITEMKU_API_KEY": "k", "ITEMKU_SECRET": "s", "ITEMKU_SHOP_ID": "1",
	"DF_USERNAME": "u", "DF_API_KEY": "k", "DF_WEBHOOK_SECRET": "w",
	"ADMIN_PASSWORD_HASH": "h", "SESSION_SECRET": "x",
}

func TestLoadRefusesMissingSecret(t *testing.T) {
	for name := range required {
		t.Run(name, func(t *testing.T) {
			for k, v := range required {
				t.Setenv(k, v)
			}
			t.Setenv(name, "")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Load() error = %v, want one naming %s", err, name)
			}
		})
	}
}

func TestLoadDefaultsToLocalhost(t *testing.T) {
	for k, v := range required {
		t.Setenv(k, v)
	}
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("TG_BOT_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("ListenAddr = %q, want 127.0.0.1:8080", cfg.ListenAddr)
	}
}
