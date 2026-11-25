package catalog

import (
	"context"
	"database/sql"
	"log"
	"sync/atomic"
	"time"
)

// Holder serves the current index and rebuilds it in the background when the
// data changes. Readers always get a complete index: the old one keeps
// serving until the new one is ready.
type Holder struct {
	db   *sql.DB
	cur  atomic.Pointer[Index]
	sig  string
	busy atomic.Bool
}

// NewHolder returns an empty holder; call Start to build and watch.
func NewHolder(db *sql.DB) *Holder { return &Holder{db: db} }

// Get returns the current index, or nil while the first build runs.
func (h *Holder) Get() *Index { return h.cur.Load() }

// Start builds the index in the background, then checks every interval
// whether a sync changed the data and rebuilds when it did.
func (h *Holder) Start(ctx context.Context, interval time.Duration) {
	go func() {
		h.Rebuild(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if sig, err := h.signature(ctx); err == nil && sig != h.sig {
					h.Rebuild(ctx)
				}
			}
		}
	}()
}

// Rebuild builds a fresh index and swaps it in. Concurrent calls are dropped.
func (h *Holder) Rebuild(ctx context.Context) {
	if !h.busy.CompareAndSwap(false, true) {
		return
	}
	defer h.busy.Store(false)
	sig, _ := h.signature(ctx)
	ix, err := Build(ctx, h.db)
	if err != nil {
		log.Printf("catalog index: build failed (keeping the previous index): %v", err)
		return
	}
	h.cur.Store(ix)
	h.sig = sig
	log.Printf("catalog index: %s", ix.Summary())
}

// signature changes whenever a sync (or any product change) happened: the
// last sync time, the product count and the highest id.
func (h *Holder) signature(ctx context.Context) (string, error) {
	var sync, count, maxID sql.NullString
	_ = h.db.QueryRowContext(ctx, `SELECT MAX(updated_at) FROM sync_state`).Scan(&sync)
	err := h.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(id) FROM products`).Scan(&count, &maxID)
	return sync.String + "|" + count.String + "|" + maxID.String, err
}
