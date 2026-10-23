package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"wongkito/api/internal/config"
	"wongkito/api/internal/digiflazz"
	"wongkito/api/internal/relay"
)

// hookEngine is the small relay surface the public webhooks need, so their
// tests do not have to stand up a database or the real HTTP clients.
type hookEngine interface {
	VerifyAndIntake(ctx context.Context, orderID int64) error
	NoteCallback()
	ApplyTx(ctx context.Context, r digiflazz.TxResult) error
}

type server struct {
	cfg     config.Config
	hooks   hookEngine
	relay   *relay.Engine
	static  fs.FS
	limiter *loginLimiter
}

// New builds the HTTP handler for the real engine.
func New(cfg config.Config, e *relay.Engine, static fs.FS) http.Handler {
	s := newServer(cfg, e, static)
	s.relay = e
	return s.handler()
}

func newServer(cfg config.Config, hooks hookEngine, static fs.FS) *server {
	return &server{
		cfg:     cfg,
		hooks:   hooks,
		static:  static,
		limiter: newLoginLimiter(),
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/itemku", s.itemkuHook)
	mux.HandleFunc("POST /hooks/digiflazz", s.digiflazzHook)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	if s.relay != nil {
		mux.Handle("/api/", s.requireAuth(s.apiMux()))
	} else {
		mux.Handle("/api/", s.requireAuth(http.HandlerFunc(s.apiNotFound)))
	}
	mux.Handle("/", s.staticHandler())
	return mux
}

func (s *server) apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (s *server) staticHandler() http.Handler {
	files := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.static == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(s.static, name); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		// SPA fallback: unknown non-API paths render the app shell.
		fallback := r.Clone(r.Context())
		fallback.URL.Path = "/index.html"
		files.ServeHTTP(w, fallback)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
