package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"wongkito/api/internal/config"
)

func TestServerBoots(t *testing.T) {
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "t.db"))
	t.Setenv("ITEMKU_API_KEY", "k")
	t.Setenv("ITEMKU_SECRET", "s")
	t.Setenv("ITEMKU_SHOP_ID", "1")
	t.Setenv("DF_USERNAME", "u")
	t.Setenv("DF_API_KEY", "k")
	t.Setenv("DF_WEBHOOK_SECRET", "whsec")
	t.Setenv("ADMIN_PASSWORD_HASH", "x")
	t.Setenv("SESSION_SECRET", "s")
	t.Setenv("DRY_RUN", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	srv := httptest.NewServer(a.handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	resp, err = http.Post(srv.URL+"/hooks/digiflazz", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unsigned hook = %d, want 401", resp.StatusCode)
	}
}

func TestRedact(t *testing.T) {
	r := newRedactor("topsecret", "")
	got := string(r([]byte(`{"sign":"abc","username":"u","key":"k","msg":"x topsecret y"}`)))
	for _, bad := range []string{"abc", `"k"`, "topsecret"} {
		if strings.Contains(got, bad) {
			t.Fatalf("not redacted %q: %s", bad, got)
		}
	}
	if !strings.Contains(got, `"username":"u"`) {
		t.Fatalf("over-redacted: %s", got)
	}
}
