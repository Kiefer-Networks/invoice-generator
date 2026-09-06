package main

import (
	"context"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDocumentProductionRootConfiguration(t *testing.T) {
	cfg := testConfig(t)
	for _, root := range []string{"", "relative/documents"} {
		cfg.DocumentRoot = root
		if e := cfg.Validate(); e == nil {
			t.Fatalf("accepted root %q", root)
		}
	}
	cfg.DocumentRoot = t.TempDir()
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" {
		os.Chmod(cfg.DocumentRoot, 0755)
		defer os.Chmod(cfg.DocumentRoot, 0700)
		if e := cfg.Validate(); e == nil {
			t.Fatal("broad permissions accepted")
		}
	}
}
func TestDocumentLifecycleStartRecoveryStop(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	cfg := testConfig(t)
	cfg.DocumentRoot = filepath.Join(t.TempDir(), "docs")
	svc, wake, stop, e := startDocuments(ctx, db, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if svc == nil || wake == nil || stop == nil {
		t.Fatal("missing document lifecycle")
	}
	wake()
	end, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if e = stop(end); e != nil {
		t.Fatal(e)
	}
	cfg.DocumentRoot = filepath.Join(t.TempDir(), "file")
	os.WriteFile(cfg.DocumentRoot, []byte("x"), 0600)
	if _, _, _, e = startDocuments(ctx, db, cfg); e == nil {
		t.Fatal("unsafe storage started")
	}
}
func TestDocumentServerDrainsActiveRequestsBeforeStorageClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.Write([]byte("done")) })}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, srv, func() error { return srv.Serve(listener) }) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		resp, e := http.Get("http://" + listener.Addr().String())
		if e == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case e := <-done:
		close(release)
		t.Fatal("returned while request still active", e)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not drain")
	}
	<-requestDone
}
func TestDocumentServerForcedShutdownTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		done <- serveHTTPWithGrace(ctx, srv, func() error { return srv.Serve(ln) }, 20*time.Millisecond)
	}()
	requestDone := make(chan error, 1)
	go func() {
		resp, e := http.Get("http://" + ln.Addr().String())
		if resp != nil {
			resp.Body.Close()
		}
		requestDone <- e
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not serving")
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forced shutdown exceeded its bound")
	}
	select {
	case e := <-requestDone:
		if e == nil {
			t.Fatal("blocked connection was not closed")
		}
	case <-time.After(time.Second):
		t.Fatal("forced shutdown left connection open")
	}
}
