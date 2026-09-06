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
		ln.Close()
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
			r.ParseMultipartForm(1000)
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
		fmt.Fprint(w, `{"count":1,"results":[{"id":42,"title":"different"}]}`)
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
		old := net.DefaultResolver
		defer func() { net.DefaultResolver = old }()
		dialed := make(chan struct{}, 2)
		net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
			default:
			}
			return (&net.Dialer{}).DialContext(ctx, "udp", blackhole.LocalAddr().String())
		}}
		c, e := NewClient(Config{URL: "https://paperless.invalid", APIKey: "secret"}, &http.Client{Timeout: 70 * time.Millisecond}, false)
		if e != nil {
			t.Fatal(e)
		}
		start := time.Now()
		_, e = c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
		var failure *SubmissionError
		if !errors.As(e, &failure) || failure.Certainty != NotSent || time.Since(start) > time.Second {
			t.Fatal(e)
		}
		select {
		case <-dialed:
		default:
			t.Fatal("DNS was not attempted")
		}
	})
	t.Run("response_headers_after_write", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.ParseMultipartForm(1000); <-r.Context().Done() }))
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
