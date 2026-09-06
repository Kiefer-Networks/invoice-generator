package jobs

import (
	"context"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerBoundedWakeAndCancellation(t *testing.T) {
	s, e := store.Open(context.Background(), filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		_, e = s.DB().Exec(`INSERT INTO customers(id,number,display_name) VALUES(?,?, 'Buyer')`, i, i)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.DB().Exec(`INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot) VALUES(?,?,'finalized','EUR',?,'{}','{}','{}','{}','{}','{}','{}')`, i, i, i)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.DocumentRepository().Enqueue(context.Background(), string(rune('0'+i))); e != nil {
			t.Fatal(e)
		}
	}
	var active, maxActive, calls atomic.Int32
	entered := make(chan struct{}, 4)
	work := func(ctx context.Context, j store.DocumentJob) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	r := New(s.DocumentRepository(), work)
	if e = r.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("not started")
		}
	}
	if maxActive.Load() != 2 {
		t.Fatal(maxActive.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = r.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 || active.Load() != 0 {
		t.Fatal(calls.Load(), active.Load())
	}
	r.Wake()
	if e = r.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart failed")
	}
	r.Stop(ctx)
}
