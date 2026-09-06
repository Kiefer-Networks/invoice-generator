package main

import (
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/jobs"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

func validateDocumentRoot(path string) error {
	if e := documents.ValidateRoot(path); e != nil {
		return e
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("document root must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("document root permissions must be owner-only")
	}
	return nil
}
func startDocuments(ctx context.Context, db *store.Store, cfg Config) (*documents.Service, func(), func(context.Context) error, error) {
	return startDocumentsWithStorage(ctx, db, cfg, documents.NewStorage)
}

func startDocumentsWithStorage(ctx context.Context, db *store.Store, cfg Config, openStorage func(string, int64) (*documents.Storage, error)) (*documents.Service, func(), func(context.Context) error, error) {
	root := cfg.DocumentRoot
	if root == "" && cfg.Development {
		var e error
		root, e = filepath.Abs(filepath.Join(filepath.Dir(cfg.Database), "documents"))
		if e != nil {
			return nil, nil, nil, e
		}
	}
	if e := validateDocumentRoot(root); e != nil {
		return nil, nil, nil, e
	}
	storage, e := openStorage(root, 20<<20)
	if e != nil {
		return nil, nil, nil, e
	}
	svc := documents.New(db, storage)
	if e = svc.Recover(ctx); e != nil {
		storage.Close()
		return nil, nil, nil, e
	}
	runner := jobs.New(db.DocumentRepository(), svc.Generate)
	if e = runner.Start(ctx); e != nil {
		storage.Close()
		return nil, nil, nil, e
	}
	paperlessWorker := jobs.NewPaperlessWorker(db, storage, func() (*paperless.Client, error) {
		if cfg.Development || cfg.PaperlessURL == "" {
			return nil, errors.New("configuration_missing")
		}
		token, e := paperless.ReadToken(cfg.PaperlessTokenFile)
		if e != nil {
			return nil, e
		}
		c, e := paperless.NewClient(paperless.Config{URL: cfg.PaperlessURL, APIKey: token}, nil, false)
		if e != nil {
			return nil, errors.New("configuration_invalid")
		}
		return c, nil
	})
	paperlessCtx, cancelPaperless := context.WithCancel(ctx)
	paperlessDone := make(chan struct{})
	go func() { defer close(paperlessDone); paperlessWorker.Run(paperlessCtx) }()
	var once sync.Once
	var closeErr error
	stop := func(ctx context.Context) error {
		cancelPaperless()
		select {
		case <-paperlessDone:
		case <-ctx.Done():
			return ctx.Err()
		}
		if e := runner.Stop(ctx); e != nil {
			return e
		}
		once.Do(func() { closeErr = storage.Close() })
		return closeErr
	}
	return svc, func() { runner.Wake(); paperlessWorker.Wake() }, stop, nil
}

// serveHTTP joins shutdown before document storage and SQLite are closed.
func serveHTTP(ctx context.Context, server *http.Server, listen func() error) error {
	return serveHTTPWithGrace(ctx, server, listen, 10*time.Second)
}

func serveHTTPWithGrace(ctx context.Context, server *http.Server, listen func() error, grace time.Duration) error {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			drain, cancel := context.WithTimeout(context.Background(), grace)
			defer cancel()
			if e := server.Shutdown(drain); e != nil {
				_ = server.Close()
			}
		case <-stop:
		}
	}()
	e := listen()
	close(stop)
	<-done
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
