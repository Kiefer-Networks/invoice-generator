// Package jobs runs durable document jobs with two bounded workers.
package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

type Runner struct {
	repo     *store.DocumentRepository
	work     func(context.Context, store.DocumentJob) error
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	wake     chan struct{}
	now      func() time.Time
	interval time.Duration
}

func New(repo *store.DocumentRepository, work func(context.Context, store.DocumentJob) error) *Runner {
	return &Runner{repo: repo, work: work, wake: make(chan struct{}, 2), now: time.Now, interval: 30 * time.Second}
}
func (r *Runner) Start(parent context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return errors.New("runner already started")
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done = make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.loop(ctx) }()
	}
	done := r.done
	go func() { wg.Wait(); close(done) }()
	return nil
}
func (r *Runner) Stop(ctx context.Context) error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		r.mu.Lock()
		if r.done == done {
			r.cancel = nil
		}
		r.mu.Unlock()
		return nil
	}
}
func (r *Runner) Wake() {
	for i := 0; i < 2; i++ {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}
func (r *Runner) loop(ctx context.Context) {
	timer := time.NewTicker(r.interval)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		j, e := r.repo.Claim(ctx, r.now(), 5*time.Minute)
		if e == nil {
			workCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			e = r.work(workCtx, j)
			cancel()
			if e != nil {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				code := "render_failed"
				if ctx.Err() != nil {
					code = "cancelled"
				}
				_ = r.repo.Fail(cleanup, j, r.now(), code, 5)
				stop()
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}
	}
}
