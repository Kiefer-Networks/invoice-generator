package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/web"
	"github.com/kiefer-networks/invoice-generator/internal/zugferd"
)

func checkReadiness(ctx context.Context, checks ...func(context.Context) error) error {
	for _, check := range checks {
		if err := check(ctx); err != nil {
			return errors.New("unavailable")
		}
	}
	return nil
}

// A minimal valid CII grammar probe contains no business fixture data.
const readinessCII = `<rsm:CrossIndustryInvoice xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100" xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100" xmlns:udt="urn:un:unece:uncefact:data:standard:UnqualifiedDataType:100"><rsm:ExchangedDocumentContext/><rsm:ExchangedDocument><ram:ID>readiness</ram:ID><ram:IssueDateTime><udt:DateTimeString format="102">20000101</udt:DateTimeString></ram:IssueDateTime></rsm:ExchangedDocument><rsm:SupplyChainTradeTransaction><ram:ApplicableHeaderTradeAgreement/><ram:ApplicableHeaderTradeDelivery/><ram:ApplicableHeaderTradeSettlement/></rsm:SupplyChainTradeTransaction></rsm:CrossIndustryInvoice>`

func validateRuntime(ctx context.Context) error {
	if err := zugferd.ValidateCII(ctx, []byte(readinessCII)); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "invoice-readiness-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }() // Best-effort cleanup of the synthetic readiness probe.
	return render.FromSnapshot(ctx, &render.TplData{}, filepath.Join(dir, "probe.pdf"))
}

// Expensive checks refresh in the background. Health requests read a bounded
// cache and never start Chrome. Failed initial validation prevents job startup.
func readiness(ctx context.Context, db *store.Store, cfg Config, manager *auth.Manager) (func(context.Context) error, error) {
	var mu sync.RWMutex
	var checked time.Time
	var runtimeErr error
	refresh := func() error {
		probe, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		err := checkReadiness(probe, manager.CheckDiscovery, validateRuntime)
		mu.Lock()
		checked = time.Now()
		runtimeErr = err
		mu.Unlock()
		return err
	}
	if err := refresh(); err != nil {
		return nil, err
	}
	ready := func(request context.Context) error {
		if ctx.Err() != nil {
			return errors.New("stopping")
		}
		mu.RLock()
		err := runtimeErr
		stale := time.Since(checked) > 60*time.Second
		mu.RUnlock()
		if err != nil || stale {
			return errors.New("runtime unavailable")
		}
		return checkReadiness(request, db.DB().PingContext, db.CheckMigrations, func(context.Context) error {
			if err := validateDocumentRoot(cfg.DocumentRoot); err != nil {
				return err
			}
			f, err := os.CreateTemp(cfg.DocumentRoot, ".ready-")
			if err != nil {
				return err
			}
			name := f.Name()
			err = f.Close()
			return errors.Join(err, os.Remove(name)) // #nosec G703 -- name comes directly from CreateTemp in the validated document root; no request path is used.
		})
	}
	if err := ready(ctx); err != nil {
		return nil, err
	}
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = refresh()
			}
		}
	}()
	return ready, nil
}

func withHealth(next http.Handler, ready func(context.Context) error) http.Handler {
	health := web.HealthHandler(ready)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" {
			health.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
