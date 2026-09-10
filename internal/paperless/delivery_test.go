package paperless

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPaperlessSubmissionCertaintyFromTransport(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		ln, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		c, e := NewClient(Config{URL: "http://" + addr, APIKey: "secret"}, nil, true)
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
		var failure *SubmissionError
		if !errors.As(e, &failure) || failure.Certainty != NotSent {
			t.Fatal("unclassified pre-send connection failure", e)
		}
	})
	t.Run("tls", func(t *testing.T) {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted request sent") }))
		defer s.Close()
		c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, nil, true)
		_, e := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
		var failure *SubmissionError
		if !errors.As(e, &failure) || failure.Certainty != NotSent {
			t.Fatal(e)
		}
	})
	t.Run("after_write", func(t *testing.T) {
		read := make(chan struct{})
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
			if err := r.ParseMultipartForm(1000); err != nil { // #nosec G120 -- MaxBytesReader above bounds the total mock upload body.
				t.Error(err)
			}
			close(read)
			<-r.Context().Done()
		}))
		defer s.Close()
		c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, &http.Client{Timeout: 50 * time.Millisecond}, true)
		_, e := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
		<-read
		var failure *SubmissionError
		if !errors.As(e, &failure) || failure.Certainty != PossiblySent {
			t.Fatal(e)
		}
	})
}

func TestPaperlessLookupRejectsDifferentTitle(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"count":1,"results":[{"id":42,"title":"different"}]}`)
	}))
	defer s.Close()
	c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
	if _, e := c.FindDocument(context.Background(), "expected"); e == nil {
		t.Fatal("mismatching exact-title query treated as absent")
	}
}

func TestPaperlessDNSAndResponseHeaderTimeouts(t *testing.T) {
	t.Run("dns_before_send", func(t *testing.T) {
		blackhole, e := net.ListenPacket("udp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer blackhole.Close()
		dialed := make(chan struct{}, 2)
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
			default:
			}
			return (&net.Dialer{}).DialContext(ctx, "udp", blackhole.LocalAddr().String())
		}}
		c, e := newClientWithResolver(Config{URL: "https://paperless.invalid", APIKey: "secret"}, &http.Client{Timeout: time.Second}, false, resolver)
		if e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() {
			_, err := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
			result <- err
		}()
		select {
		case <-dialed:
		case err := <-result:
			t.Fatalf("request ended before the injected DNS lookup started: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("DNS lookup did not start")
		}
		// Synchronize on the real lookup before checking that the blackholed
		// DNS exchange is interrupted, instead of timing test scheduling.
		select {
		case e = <-result:
			var failure *SubmissionError
			if !errors.As(e, &failure) || failure.Certainty != NotSent {
				t.Fatal(e)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("DNS lookup outlived the client timeout")
		}
	})
	t.Run("response_headers_after_write", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
			if err := r.ParseMultipartForm(1000); err != nil { // #nosec G120 -- MaxBytesReader above bounds the total mock upload body.
				t.Error(err)
			}
			<-r.Context().Done()
		}))
		defer s.Close()
		c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
		c.http.Timeout = time.Second
		c.http.Transport.(*http.Transport).ResponseHeaderTimeout = 40 * time.Millisecond
		start := time.Now()
		_, e := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
		var failure *SubmissionError
		if !errors.As(e, &failure) || failure.Certainty != PossiblySent || time.Since(start) > 500*time.Millisecond {
			t.Fatal(e)
		}
	})
}
