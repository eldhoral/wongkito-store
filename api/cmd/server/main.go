package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"wongkito/api/internal/config"
	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/itemku"
	"wongkito/api/internal/relay"
	"wongkito/api/internal/store"
	"wongkito/api/internal/telegram"
	"wongkito/api/internal/web"
)

const logRetention = 30 * 24 * time.Hour

type app struct {
	db      *store.Store
	engine  *relay.Engine
	handler http.Handler
}

var secretField = regexp.MustCompile(`(?i)"(sign|key|api_?key|secret|token|authorization|password)"\s*:\s*"[^"]*"`)

// newRedactor masks secret-looking JSON fields and any literal secret value.
func newRedactor(secrets ...string) func([]byte) []byte {
	var pairs []string
	for _, s := range secrets {
		if s != "" {
			pairs = append(pairs, s, "***")
		}
	}
	lit := strings.NewReplacer(pairs...)
	return func(b []byte) []byte {
		b = secretField.ReplaceAll(b, []byte(`"$1":"***"`))
		if len(pairs) > 0 {
			b = []byte(lit.Replace(string(b)))
		}
		return b
	}
}

// build wires config, store, clients, engine and HTTP handler. It starts nothing.
func build(cfg config.Config) (*app, error) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	redact := newRedactor(cfg.ItemkuAPIKey, cfg.ItemkuSecret, cfg.DigiflazzAPIKey,
		cfg.DigiflazzWebhookSecret, cfg.TelegramBotToken)
	logger := func(service string) func(string, []byte, []byte, int, time.Duration) {
		return func(endpoint string, req, resp []byte, status int, d time.Duration) {
			_, err := db.DB.Exec(`INSERT INTO api_log (at, service, endpoint, req, resp, http_status, ms)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				time.Now().Unix(), service, endpoint, string(redact(req)), string(redact(resp)), status, d.Milliseconds())
			if err != nil {
				log.Printf("api_log insert: %v", err)
			}
		}
	}
	e := &relay.Engine{
		DB: db,
		IK: &itemku.Client{
			BaseURL: cfg.ItemkuBaseURL, APIKey: cfg.ItemkuAPIKey, Secret: cfg.ItemkuSecret,
			Log: logger("itemku"),
		},
		DF: &digiflazz.Client{
			Username: cfg.DigiflazzUsername, APIKey: cfg.DigiflazzAPIKey,
			Log: logger("digiflazz"),
		},
		TG:     &telegram.Bot{Token: cfg.TelegramBotToken, ChatID: cfg.TelegramChatID},
		DryRun: cfg.DryRun,
	}
	return &app{db: db, engine: e, handler: web.New(cfg, e, web.Static())}, nil
}

func pruneLogs(ctx context.Context, db *store.Store) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if _, err := db.DB.ExecContext(ctx, `DELETE FROM api_log WHERE at < ?`,
			time.Now().Add(-logRetention).Unix()); err != nil && ctx.Err() == nil {
			log.Printf("prune api_log: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	a, err := build(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer a.db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go a.engine.Run(ctx)
	go pruneLogs(ctx, a.db)

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: a.handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("listening on %s (dry_run=%v)", cfg.ListenAddr, cfg.DryRun)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
