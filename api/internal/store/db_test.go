package store_test

import (
	"path/filepath"
	"strings"
	"testing"

	"wongkito/api/internal/store"
)

func openTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func insertOrder(t *testing.T, s *store.Store, id string, itemkuID int64) {
	t.Helper()
	_, err := s.DB.Exec(
		`INSERT INTO orders (id, source, itemku_order_id, state, created_at, updated_at)
		 VALUES (?, 'itemku', ?, 'new', 1, 1)`, id, itemkuID)
	if err != nil {
		t.Fatalf("insert order %s: %v", id, err)
	}
}

func insertAttempt(t *testing.T, s *store.Store, ref, orderID string, unit int, status string) error {
	t.Helper()
	_, err := s.DB.Exec(
		`INSERT INTO attempts (ref_id, order_id, unit, attempt_no, buyer_sku_code, customer_no, status, created_at, updated_at)
		 VALUES (?, ?, ?, 1, 'sku', 'cust', ?, 1, 1)`, ref, orderID, unit, status)
	return err
}

func TestOpenMigratesTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	s1, err := store.Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	var applied int
	if err := s2.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied != 2 { // 001_init, 002_ads_fee
		t.Fatalf("schema_migrations = %d, want 2", applied)
	}
}

func TestOneOpenAttemptPerUnit(t *testing.T) {
	s := openTest(t)
	insertOrder(t, s, "o1", 101)

	if err := insertAttempt(t, s, "r1", "o1", 0, "Pending"); err != nil {
		t.Fatalf("insert first attempt: %v", err)
	}
	err := insertAttempt(t, s, "r2", "o1", 0, "unknown")
	if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Fatalf("second open attempt error = %v, want UNIQUE", err)
	}

	if _, err := s.DB.Exec(`UPDATE attempts SET status = 'Gagal' WHERE ref_id = 'r1'`); err != nil {
		t.Fatalf("fail first attempt: %v", err)
	}
	if err := insertAttempt(t, s, "r2", "o1", 0, "unknown"); err != nil {
		t.Fatalf("insert after Gagal: %v", err)
	}

	if err := insertAttempt(t, s, "r3", "o1", 0, "Gagal"); err != nil {
		t.Fatalf("insert Gagal alongside open attempt: %v", err)
	}
}

func TestItemkuOrderUnique(t *testing.T) {
	s := openTest(t)
	insertOrder(t, s, "first", 777)

	_, err := s.DB.Exec(
		`INSERT INTO orders (id, source, itemku_order_id, state, created_at, updated_at)
		 VALUES ('second', 'itemku', 777, 'new', 1, 1)`)
	if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Fatalf("duplicate itemku_order_id error = %v, want UNIQUE", err)
	}
}
