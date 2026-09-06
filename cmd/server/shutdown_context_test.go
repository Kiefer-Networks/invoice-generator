package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestGracefulShutdownKeepsWriteContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, finish := make(chan context.Context, 1), make(chan struct{})
	srv := &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- r.Context(); <-finish; w.WriteHeader(204) })}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, srv, func() error { return srv.Serve(listener) }) }()
	go func() {
		res, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			res.Body.Close()
		}
	}()
	var request context.Context
	select {
	case request = <-started:
	case <-time.After(time.Second):
		t.Fatal("request missing")
	}
	cancel()
	select {
	case <-request.Done():
		close(finish)
		<-done
		t.Fatal("shutdown canceled the in-flight write context")
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not drain")
	}
}
