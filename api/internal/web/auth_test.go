package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"wongkito/api/internal/config"
)

func TestApiRequiresSession(t *testing.T) {
	s := newServer(config.Config{}, &fakeHookEngine{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	rr := httptest.NewRecorder()

	s.handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestLoginSetsCookieAndRateLimits(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	cfg := config.Config{AdminPasswordHash: string(hash), SessionSecret: "session-secret"}
	s := newServer(cfg, &fakeHookEngine{}, nil)
	handler := s.handler()

	rr := postLogin(handler, "secret")
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	c := cookies[0]
	if c.Name != "s" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie = %#v, want s/HttpOnly/Secure/Strict//", c)
	}

	authed := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	authed.AddCookie(c)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authed)
	if rr.Code == http.StatusUnauthorized {
		t.Fatal("session cookie was not accepted")
	}

	// A fresh server so the successful login above does not reset the counter.
	s2 := newServer(cfg, &fakeHookEngine{}, nil)
	for i := 1; i <= 5; i++ {
		rr := postLogin(s2.handler(), "wrong")
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("bad try %d status = %d, want 401", i, rr.Code)
		}
	}
	if rr := postLogin(s2.handler(), "wrong"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("sixth bad try status = %d, want 429", rr.Code)
	}
}

func postLogin(h http.Handler, password string) *httptest.ResponseRecorder {
	body := bytes.NewBufferString(fmt.Sprintf(`{"password":%q}`, password))
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
