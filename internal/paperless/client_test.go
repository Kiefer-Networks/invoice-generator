package paperless

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPaperlessFixedTagsAndTask(t *testing.T) {
	names := []string{"Kiefer Networks", "Rechnung", "Steuer", "Umsatzsteuer", "Gewerbesteuer", "INBOX"}
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret" {
			t.Error("auth missing")
		}
		if r.URL.Path == "/api/tags/" {
			name := r.URL.Query().Get("name__iexact")
			seen = append(seen, name)
			fmt.Fprintf(w, `{"count":1,"results":[{"id":%d,"name":%q}]}`, len(seen), name)
			return
		}
		if r.URL.Path == "/api/documents/post_document/" {
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
			}
			if len(r.MultipartForm.Value["tags"]) != 6 || r.FormValue("title") != "Invoice PL-1 [invoice-generator:abc]" {
				t.Error(r.Form)
			}
			fmt.Fprint(w, `"task-123"`)
			return
		}
		t.Error(r.URL)
		w.WriteHeader(404)
	}))
	defer s.Close()
	c, e := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
	if e != nil {
		t.Fatal(e)
	}
	ids, e := c.ResolveTags(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	task, e := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "Invoice PL-1 [invoice-generator:abc]", ids)
	if e != nil || task != "task-123" {
		t.Fatal(task, e)
	}
	if strings.Join(names, ",") != strings.Join(seen, ",") {
		t.Fatal(seen)
	}
}
func TestPaperlessRejectsUnsafeConfigAndResponses(t *testing.T) {
	for _, u := range []string{"http://paperless.internal", "https://user:secret@host", "https://host/?x=secret", "https://host/#secret", "https://169.254.169.254", "https://[::1]", "https://127.0.0.1"} {
		if _, e := NewClient(Config{URL: u, APIKey: "secret"}, nil, false); e == nil {
			t.Error(u)
		}
	}
	for _, body := range []string{strings.Repeat("x", (1<<20)+1), `{"results":[{"id":-1,"name":"Kiefer Networks"}]}`, `secret`, `{"results":[]} garbage`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		c, e := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.ResolveTags(context.Background())
		s.Close()
		if e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal(e)
		}
	}
}
func TestPaperlessTimeoutRedirectAndTLS(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
	if _, e := c.ResolveTags(context.Background()); e == nil || hit {
		t.Fatal(e, hit)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	c, _ = NewClient(Config{URL: slow.URL, APIKey: "secret"}, &http.Client{Timeout: 30 * time.Millisecond}, true)
	start := time.Now()
	if _, e := c.ResolveTags(context.Background()); e == nil || time.Since(start) > time.Second {
		t.Fatal(e)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tls.Close()
	c, _ = NewClient(Config{URL: tls.URL, APIKey: "secret"}, nil, true)
	if _, e := c.ResolveTags(context.Background()); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
}

func TestPaperlessCLISecretRedaction(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); fmt.Fprint(w, "secret-token") }))
	defer s.Close()
	path := writeTemp(t, t.TempDir(), "document.pdf", "%PDF")
	e := Upload(&Config{URL: s.URL, APIKey: "secret-token"}, path, "title")
	if e == nil || strings.Contains(e.Error(), "secret-token") {
		t.Fatal(e)
	}
}

func TestPaperlessTransportBoundsAndIPPolicy(t *testing.T) {
	c, e := NewClient(Config{URL: "https://paperless.internal", APIKey: "secret"}, nil, false)
	if e != nil {
		t.Fatal(e)
	}
	tr := c.http.Transport.(*http.Transport)
	if c.http.Timeout != 60*time.Second || tr.ResponseHeaderTimeout != 10*time.Second || tr.TLSHandshakeTimeout != 5*time.Second || tr.Proxy != nil || tr.MaxConnsPerHost != 2 || tr.MaxResponseHeaderBytes != 32<<10 {
		t.Fatal("unbounded transport")
	}
	for _, ip := range []string{"10.1.2.3", "192.168.1.1", "172.16.1.1", "100.100.1.1", "fd00::1"} {
		if !allowedIP(netip.MustParseAddr(ip), false) {
			t.Error("private deployment blocked", ip)
		}
	}
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "::ffff:169.254.169.254", "fe80::1", "224.0.0.1", "0.0.0.0", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1"} {
		if allowedIP(netip.MustParseAddr(ip), false) {
			t.Error("unsafe destination", ip)
		}
	}
}

func TestPaperlessInjectedTLSDialersCannotBypassPolicy(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer s.Close()
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			injected := s.Client()
			tr := injected.Transport.(*http.Transport).Clone()
			called := false
			dial := func(_, _ string) (net.Conn, error) {
				called = true
				return nil, errors.New("injected TLS dialer bypassed policy")
			}
			if legacy {
				//lint:ignore SA1019 Exercise legacy injected transports that must also have their TLS hook cleared.
				tr.DialTLS = dial //nolint:staticcheck // SA1019: deliberately inject the legacy bypass regression case.
			} else {
				tr.DialTLSContext = func(_ context.Context, network, address string) (net.Conn, error) {
					return dial(network, address)
				}
			}
			client, err := NewClient(Config{URL: s.URL, APIKey: "secret"}, &http.Client{Transport: tr}, true)
			if err != nil {
				t.Fatal(err)
			}
			defer client.http.CloseIdleConnections()
			response, err := client.http.Get(s.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if called || response.StatusCode != http.StatusNoContent {
				t.Fatalf("TLS hook called=%t, status=%d", called, response.StatusCode)
			}
		})
	}
}
func TestPaperlessPollingAndMaliciousResponses(t *testing.T) {
	for _, body := range []string{`[{"task_id":"other","status":"SUCCESS","related_document":42}]`, `[{"task_id":"task-1","status":"SUCCESS","related_document":"https://host/secret"}]`, `[{"task_id":"task-1","status":"SUCCESS","related_document":-1}]`, `[{"task_id":"task-1","status":"secret"}]`, `[]`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
		if _, e := c.Poll(context.Background(), "task-1"); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal(e)
		}
		s.Close()
	}
}

func TestPaperlessMalformedLookupCannotCreateDuplicateTag(t *testing.T) {
	created := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			created = true
			fmt.Fprint(w, `{"id":1}`)
			return
		}
		fmt.Fprint(w, `{"count":1,"results":[]}`)
	}))
	defer s.Close()
	c, _ := NewClient(Config{URL: s.URL, APIKey: "secret"}, s.Client(), true)
	if _, e := c.ResolveTags(context.Background()); e == nil || created {
		t.Fatal("incomplete lookup created tag", e, created)
	}
}
func TestPaperlessEchoedTokenCannotBecomeTaskID(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `"secret-token"`) }))
	defer s.Close()
	c, _ := NewClient(Config{URL: s.URL, APIKey: "secret-token"}, s.Client(), true)
	if _, e := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil); e == nil {
		t.Fatal("response echoed token persisted as task identifier")
	}
}
