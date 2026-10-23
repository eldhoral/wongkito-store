package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
)

const (
	pollInterval     = 30 * time.Second
	recheckInterval  = 2 * time.Minute
	catalogInterval  = 15 * time.Minute
	balanceInterval  = 10 * time.Minute
	reminderInterval = time.Minute
)

type loop struct {
	name     string
	interval time.Duration
	fn       func(context.Context) error
}

// Run starts the background loops and blocks until ctx is cancelled and all
// in-flight ticks have returned. Each tick runs in its own goroutine so a slow
// API call never delays the other loops.
func (e *Engine) Run(ctx context.Context) {
	e.run(ctx, []loop{
		{"poll orders", pollInterval, e.PollOrders},
		{"recheck pending", recheckInterval, e.RecheckPending},
		{"sync catalog", catalogInterval, func(ctx context.Context) error {
			productErr := e.SyncProducts(ctx)
			priceErr := e.SyncPriceList(ctx)
			if productErr != nil {
				return productErr
			}
			return priceErr
		}},
		{"check balance", balanceInterval, e.CheckBalance},
		{"reminders", reminderInterval, e.Reminders},
	})
}

func (e *Engine) run(ctx context.Context, loops []loop) {
	var wg sync.WaitGroup
	for _, l := range loops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.every(ctx, &wg, l.name, l.interval, l.fn)
		}()
	}
	wg.Wait()
}

// every returns when ctx is done; ticks it spawned are tracked by wg.
func (e *Engine) every(ctx context.Context, wg *sync.WaitGroup, name string, interval time.Duration, fn func(context.Context) error) {
	tick := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.runTick(ctx, name, fn)
		}()
	}
	tick()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

func (e *Engine) runTick(ctx context.Context, name string, fn func(context.Context) error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("relay: %s panicked: %v", name, r)
		}
	}()
	if err := fn(ctx); err != nil {
		log.Printf("relay: %s: %v", name, err)
	}
}

// PollOrders is the backup path for a broken callback. It intakes open orders,
// flags a silent callback or a backlog, and checks overdue orders for refunds.
func (e *Engine) PollOrders(ctx context.Context) error {
	now := e.now()
	orders, err := e.IK.ListOpen(ctx, now.Add(-48*time.Hour), 30)
	if err != nil {
		return fmt.Errorf("relay: list open itemku orders: %w", err)
	}
	_ = e.setSetting(ctx, "last_poll_at", strconv.FormatInt(now.Unix(), 10))

	lastCallback := e.settingInt(ctx, "last_callback_at")
	var firstErr error
	for _, o := range orders {
		created, err := e.Intake(ctx, o)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if !created {
			continue
		}
		orderAt := o.OrderCreatedEpoch
		if orderAt == 0 {
			orderAt = now.Unix()
		}
		if lastCallback == 0 || orderAt-lastCallback > int64((10*time.Minute)/time.Second) {
			e.alertOnce(ctx, "callback_silent", time.Hour,
				fmt.Sprintf("poll found order %s but itemku sent no callback for it; check the callback URL", o.OrderNumber),
				false)
		}
	}

	if len(orders) >= 30 {
		e.alertOnce(ctx, "poll_backlog", time.Hour,
			"itemku poll returned 30 open orders; there may be a backlog", false)
	}

	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT id, itemku_order_id FROM orders
		 WHERE source = 'itemku' AND state NOT IN (?, ?, ?) AND deadline_at < ?
		   AND itemku_order_id IS NOT NULL`,
		StateDelivered, StateRejected, StateCancelled, now.Unix())
	if err != nil {
		return err
	}
	type overdue struct {
		id       string
		itemkuID int64
	}
	var overdueOrders []overdue
	for rows.Next() {
		var o overdue
		if err := rows.Scan(&o.id, &o.itemkuID); err != nil {
			rows.Close()
			return err
		}
		overdueOrders = append(overdueOrders, o)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, o := range overdueOrders {
		fresh, err := e.IK.GetOrder(ctx, o.itemkuID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if fresh == nil {
			continue
		}
		if err := e.MarkCancelledIfRefunded(ctx, *fresh); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// NoteCallback records a verified itemku callback so the poll can notice when
// callbacks stop arriving.
func (e *Engine) NoteCallback() {
	_ = e.setSetting(context.Background(), "last_callback_at", strconv.FormatInt(e.now().Unix(), 10))
}

// CheckBalance alerts when the Digiflazz balance falls below the threshold.
func (e *Engine) CheckBalance(ctx context.Context) error {
	balance, err := e.DF.Balance(ctx)
	if err != nil {
		return fmt.Errorf("relay: check digiflazz balance: %w", err)
	}
	threshold := e.settingInt(ctx, "balance_alert")
	if threshold > 0 && balance < threshold {
		e.alertOnce(ctx, "balance_low", 6*time.Hour,
			fmt.Sprintf("Digiflazz balance is Rp %d, below the Rp %d threshold", balance, threshold), false)
	}
	return nil
}

// Reminders sends the two regular-order reminders (4 h and 1 h left). A late
// start sends only the nearest reminder, never both at once.
func (e *Engine) Reminders(ctx context.Context) error {
	rows, err := e.DB.DB.QueryContext(ctx,
		`SELECT o.id, o.order_number, o.game_name, o.product_name, o.deadline_at
		 FROM orders o JOIN products p ON p.id = o.product_id
		 WHERE o.state = ? AND o.deadline_at IS NOT NULL AND p.deadline_class = 'regular'
		 ORDER BY o.deadline_at`, StateNeedsYou)
	if err != nil {
		return err
	}
	defer rows.Close()

	now := e.now().Unix()
	var firstErr error
	for rows.Next() {
		var id, orderNumber, gameName, productName string
		var deadline int64
		if err := rows.Scan(&id, &orderNumber, &gameName, &productName, &deadline); err != nil {
			return err
		}
		left := deadline - now
		var key, text string
		switch {
		case left <= 3600:
			key = "remind:" + id + ":1h"
			text = fmt.Sprintf("%s — %s — order %s needs you within 1 hour", gameName, productName, orderNumber)
		case left <= 4*3600:
			key = "remind:" + id + ":4h"
			text = fmt.Sprintf("%s — %s — order %s needs you within 4 hours", gameName, productName, orderNumber)
		default:
			continue
		}
		if err := e.alertOnce(ctx, key, 0, text, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return firstErr
}

// alertOnce sends at most one alert per key and interval. every <= 0 means the
// key is permanent for the life of the database.
func (e *Engine) alertOnce(ctx context.Context, key string, every time.Duration, text string, urgent bool) error {
	if e.TG == nil {
		return nil
	}
	now := e.now().Unix()
	var at int64
	err := e.DB.DB.QueryRowContext(ctx, `SELECT at FROM alerts_sent WHERE key = ?`, key).Scan(&at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (every <= 0 || now-at < int64(every/time.Second)) {
		return nil
	}
	e.alert(ctx, text, urgent)
	_, err = e.DB.DB.ExecContext(ctx,
		`INSERT INTO alerts_sent (key, at) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET at = excluded.at`, key, now)
	return err
}

func (e *Engine) setSetting(ctx context.Context, key, value string) error {
	_, err := e.DB.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
