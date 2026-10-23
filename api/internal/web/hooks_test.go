package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"wongkito/api/internal/config"
	"wongkito/api/internal/digiflazz"
)

type fakeHookEngine struct {
	mu          sync.Mutex
	noteCalls   int
	verifyCalls []int64
	applyCalls  []digiflazz.TxResult
	verifyGate  <-chan struct{}
}

func (f *fakeHookEngine) NoteCallback() {
	f.mu.Lock()
	f.noteCalls++
	f.mu.Unlock()
}

func (f *fakeHookEngine) VerifyAndIntake(_ context.Context, orderID int64) error {
	f.mu.Lock()
	f.verifyCalls = append(f.verifyCalls, orderID)
	gate := f.verifyGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return nil
}

func (f *fakeHookEngine) ApplyTx(_ context.Context, r digiflazz.TxResult) error {
	f.mu.Lock()
	f.applyCalls = append(f.applyCalls, r)
	f.mu.Unlock()
	return nil
}

func (f *fakeHookEngine) calls() (int, []int64, []digiflazz.TxResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.noteCalls, append([]int64(nil), f.verifyCalls...), append([]digiflazz.TxResult(nil), f.applyCalls...)
}

func TestItemkuHookWrongShop403(t *testing.T) {
	fake := &fakeHookEngine{}
	s := newServer(config.Config{ItemkuShopID: "shop-1"}, fake, nil)
	req := httptest.NewRequest(http.MethodPost, "/hooks/itemku",
		bytes.NewBufferString(`{"data":[{"order_id":1}]}`))
	req.Header.Set("X-itemku", "shop-2")
	rr := httptest.NewRecorder()

	s.handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	note, verify, _ := fake.calls()
	if note != 0 || len(verify) != 0 {
		t.Fatalf("engine calls = note %d / verify %v, want none", note, verify)
	}
}

func TestItemkuHookAcks200Fast(t *testing.T) {
	gate := make(chan struct{})
	fake := &fakeHookEngine{verifyGate: gate}
	s := newServer(config.Config{ItemkuShopID: "shop-1"}, fake, nil)
	req := httptest.NewRequest(http.MethodPost, "/hooks/itemku",
		bytes.NewBufferString(`{"data":[{"order_id":11},{"order_id":12}]}`))
	req.Header.Set("X-itemku", "shop-1")
	rr := httptest.NewRecorder()

	started := time.Now()
	s.handler().ServeHTTP(rr, req)
	elapsed := time.Since(started)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("handler took %s, want an immediate ack", elapsed)
	}
	close(gate)

	deadline := time.Now().Add(2 * time.Second)
	for {
		note, verify, _ := fake.calls()
		if note == 1 && len(verify) == 2 {
			if verify[0] != 11 || verify[1] != 12 {
				t.Fatalf("verified = %v, want [11 12]", verify)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("engine calls = note %d / verify %v, want 1/2", note, verify)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDigiflazzHookBadSig401(t *testing.T) {
	fake := &fakeHookEngine{}
	s := newServer(config.Config{DigiflazzWebhookSecret: "whsecret"}, fake, nil)
	body := []byte(`{"data":{"ref_id":"r1","status":"Sukses"}}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/digiflazz", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", "sha1=deadbeef")
	rr := httptest.NewRecorder()

	s.handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	_, _, apply := fake.calls()
	if len(apply) != 0 {
		t.Fatalf("ApplyTx calls = %v, want none", apply)
	}
}

func TestDigiflazzHookAppliesTx(t *testing.T) {
	fake := &fakeHookEngine{}
	s := newServer(config.Config{DigiflazzWebhookSecret: "whsecret"}, fake, nil)
	body := []byte(`{"data":{"ref_id":"r1","status":"Sukses","sn":"SN-1"}}`)
	sig := signSHA1("whsecret", body)
	req := httptest.NewRequest(http.MethodPost, "/hooks/digiflazz", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", "sha1="+sig)
	rr := httptest.NewRecorder()

	s.handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	_, _, apply := fake.calls()
	if len(apply) != 1 || apply[0].RefID != "r1" || apply[0].SN != "SN-1" {
		t.Fatalf("ApplyTx calls = %#v, want one r1/SN-1", apply)
	}
}

func signSHA1(secret string, body []byte) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
