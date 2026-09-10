package jobs

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
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
	if err := r.Stop(ctx); err != nil {
		t.Error(err)
	}
}
func TestRunnerIdleWakeDoesNotBusyPoll(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "idle.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	called := make(chan struct{}, 2)
	r := New(s.DocumentRepository(), func(ctx context.Context, j store.DocumentJob) error {
		called <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	r.interval = time.Hour
	var polls atomic.Int32
	initial := make(chan struct{}, 2)
	r.now = func() time.Time {
		if polls.Add(1) <= 2 {
			initial <- struct{}{}
		}
		return time.Now().Add(time.Second)
	}
	if e = r.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Stop(ctx) }() // Best-effort cleanup after assertions; explicit shutdown behavior has separate assertions.
	for i := 0; i < 2; i++ {
		select {
		case <-initial:
		case <-time.After(3 * time.Second):
			t.Fatal("idle claim not attempted")
		}
	}
	time.Sleep(30 * time.Millisecond)
	before := polls.Load()
	if before != 2 {
		t.Fatal("runner busy polls without work", before)
	}
	_, e = s.DB().Exec(`INSERT INTO customers(id,number,display_name) VALUES('idle','idle','Buyer'); INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot) VALUES('idle','idle','finalized','EUR','IDLE','{}','{}','{}','{}','{}','{}','{}')`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DocumentRepository().Enqueue(ctx, "idle"); e != nil {
		t.Fatal(e)
	}
	r.Wake()
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("Wake did not resume idle workers")
	}
}
