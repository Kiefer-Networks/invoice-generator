package paperless

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const MaxDocumentSize int64 = 20 << 20
const maxResponseSize = 1 << 20

var taskPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var ErrRequest = errors.New("Paperless request failed")
var ErrRejected = errors.New("Paperless upload rejected")
var ErrResponse = errors.New("Paperless response invalid")
var ErrPending = errors.New("remote_pending")
var ErrRemoteFailed = errors.New("remote_failed")

type DeliveryCertainty uint8

const (
	PossiblySent DeliveryCertainty = iota
	NotSent
	Rejected
)

// SubmissionError contains only safe local errors, never transport URLs or
// upstream text. NotSent requires evidence that no connection was assigned and
// no request write or body read occurred. Other transport errors stay ambiguous.
type SubmissionError struct {
	Certainty DeliveryCertainty
	cause     error
}

func (e *SubmissionError) Error() string { return e.cause.Error() }
func (e *SubmissionError) Unwrap() error { return e.cause }

type observedBody struct {
	io.ReadCloser
	read *atomic.Bool
}

func (b *observedBody) Read(p []byte) (int, error) { b.read.Store(true); return b.ReadCloser.Read(p) }

// DefaultTags returns a fresh list so configuration cannot change automatic tags.
func DefaultTags() []string {
	return []string{"Kiefer Networks", "Rechnung", "Steuer", "Umsatzsteuer", "Gewerbesteuer", "INBOX"}
}

type Client struct {
	cfg  Config
	http *http.Client
}

// Private RFC1918/ULA and NetBird CGNAT destinations are intentional: only the
// administrator's fixed origin is contacted. Loopback is fixture-only; metadata,
// link-local, multicast, unspecified and reserved destinations are never allowed.
func allowedIP(ip netip.Addr, fixture bool) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return fixture
	}
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, p := range []string{"0.0.0.0/8", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "::/96"} {
		if netip.MustParsePrefix(p).Contains(ip) {
			return false
		}
	}
	return ip.IsGlobalUnicast()
}
func ValidateURL(raw string, fixture bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.Contains(u.Path, "..") {
		return errors.New("invalid Paperless URL")
	}
	if u.Scheme != "https" && !(fixture && u.Scheme == "http" && (u.Hostname() == "localhost" || isLoopback(u.Hostname()))) {
		return errors.New("Paperless requires HTTPS")
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !allowedIP(ip, fixture) {
		return errors.New("Paperless destination forbidden")
	}
	if strings.EqualFold(u.Hostname(), "localhost") && !fixture {
		return errors.New("Paperless destination forbidden")
	}
	return nil
}
func isLoopback(host string) bool { ip, e := netip.ParseAddr(host); return e == nil && ip.IsLoopback() }

// An injected client may supply a trusted test CA or shorter total timeout. Its
// transport must be a standard transport, cloned and bounded by the same policy.
func NewClient(cfg Config, injected *http.Client, fixture bool) (*Client, error) {
	if e := ValidateURL(cfg.URL, fixture); e != nil {
		return nil, e
	}
	if cfg.APIKey == "" || len(cfg.APIKey) > 4096 || strings.ContainsAny(cfg.APIKey, "\r\n\x00") {
		return nil, errors.New("invalid Paperless token")
	}
	u, _ := url.Parse(cfg.URL)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	timeout := 60 * time.Second
	if injected != nil {
		if injected.Timeout > 0 && injected.Timeout < timeout {
			timeout = injected.Timeout
		}
		if injected.Transport != nil {
			given, ok := injected.Transport.(*http.Transport)
			if !ok {
				return nil, errors.New("unsupported Paperless transport")
			}
			tr = given.Clone()
		}
	}
	tr.Proxy = nil
	tr.TLSHandshakeTimeout = 5 * time.Second
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.ExpectContinueTimeout = time.Second
	tr.IdleConnTimeout = 30 * time.Second
	tr.MaxConnsPerHost = 2
	tr.MaxIdleConnsPerHost = 2
	tr.MaxResponseHeaderBytes = 32 << 10
	tr.DisableCompression = true
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	} else {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	}
	tr.TLSClientConfig.InsecureSkipVerify = false
	tr.TLSClientConfig.ServerName = u.Hostname()
	tr.TLSClientConfig.MinVersion = tls.VersionTLS13
	tr.DialTLSContext = nil
	tr.DialTLS = nil
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		host, port, e := net.SplitHostPort(address)
		if e != nil || !strings.EqualFold(host, u.Hostname()) {
			return nil, ErrRequest
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, ErrRequest
		}
		for _, ip := range ips {
			if !allowedIP(ip, fixture) {
				return nil, ErrRequest
			}
		}
		dial := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		for _, ip := range ips {
			conn, e := dial.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, ErrRequest
	}
	return &Client{cfg: cfg, http: &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, method, path, contentType string, body io.Reader, out any) (err error) {
	upload := method == "POST" && path == "/api/documents/post_document/"
	var connected, wrote, read atomic.Bool
	if upload {
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			GotConn:      func(httptrace.GotConnInfo) { connected.Store(true) },
			WroteHeaders: func() { wrote.Store(true) },
			WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) },
		})
		defer func() {
			if err == nil {
				return
			}
			certainty := PossiblySent
			if !connected.Load() && !wrote.Load() && !read.Load() {
				certainty = NotSent
			}
			if errors.Is(err, ErrRejected) {
				certainty = Rejected
			}
			err = &SubmissionError{Certainty: certainty, cause: err}
		}()
	}
	req, e := http.NewRequestWithContext(ctx, method, apiURL(c.cfg.URL, path), body)
	if e != nil {
		return ErrRequest
	}
	setAuth(req, c.cfg.APIKey)
	if upload {
		req.GetBody = nil // never transparently replay a consumption request
		if req.Body != nil {
			req.Body = &observedBody{req.Body, &read}
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return ErrRequest
	}
	defer resp.Body.Close()
	if method == "POST" && path == "/api/documents/post_document/" {
		switch resp.StatusCode {
		case 400, 401, 403, 404, 405, 413, 415, 422, 429:
			return ErrRejected
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrRequest
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if e != nil || len(data) > maxResponseSize {
		return ErrResponse
	}
	if out == nil {
		return nil
	}
	if e = json.Unmarshal(data, out); e != nil {
		return ErrResponse
	}
	return nil
}
func (c *Client) resolveTag(ctx context.Context, name string) (int, error) {
	var list tagListResponse
	if e := c.request(ctx, "GET", "/api/tags/?name__iexact="+url.QueryEscape(name), "", nil, &list); e != nil {
		return 0, e
	}
	if list.Count < 0 || list.Count > len(list.Results) {
		return 0, ErrResponse
	}
	for _, t := range list.Results {
		if strings.EqualFold(t.Name, name) {
			if t.ID <= 0 {
				return 0, ErrResponse
			}
			return t.ID, nil
		}
	}
	body, _ := json.Marshal(map[string]string{"name": name})
	var created tagCreateResponse
	if e := c.request(ctx, "POST", "/api/tags/", "application/json", bytes.NewReader(body), &created); e != nil {
		return 0, e
	}
	if created.ID <= 0 {
		return 0, ErrResponse
	}
	return created.ID, nil
}
func (c *Client) ResolveTags(ctx context.Context) ([]int, error) {
	return c.resolveTags(ctx, DefaultTags())
}
func (c *Client) resolveTags(ctx context.Context, names []string) ([]int, error) {
	var ids []int
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		id, e := c.resolveTag(ctx, name)
		if e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, nil
}
func (c *Client) Submit(ctx context.Context, r io.Reader, size int64, title string, ids []int) (string, error) {
	return c.submit(ctx, r, size, title, ids, true)
}
func (c *Client) submit(ctx context.Context, r io.Reader, size int64, title string, ids []int, requireTask bool) (string, error) {
	if size <= 0 || size > MaxDocumentSize || len(title) > 512 || len(ids) > 64 {
		return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, e := mw.CreateFormFile("document", "invoice.pdf")
	if e != nil {
		return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
	}
	n, e := io.Copy(part, io.LimitReader(r, size+1))
	if e != nil || n != size {
		return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
	}
	if e = mw.WriteField("title", title); e != nil {
		return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
	}
	for _, id := range ids {
		if id <= 0 {
			return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
		}
		if e = mw.WriteField("tags", strconv.Itoa(id)); e != nil {
			return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
		}
	}
	if e = mw.Close(); e != nil {
		return "", &SubmissionError{Certainty: NotSent, cause: ErrRequest}
	}
	var task string
	var out any = &task
	if !requireTask {
		out = nil
	}
	if e = c.request(ctx, "POST", "/api/documents/post_document/", mw.FormDataContentType(), &buf, out); e != nil {
		return "", e
	}
	if requireTask && (!taskPattern.MatchString(task) || strings.Contains(task, c.cfg.APIKey)) {
		return "", &SubmissionError{Certainty: PossiblySent, cause: ErrResponse}
	}
	return task, nil
}
func (c *Client) FindDocument(ctx context.Context, title string) (int64, error) {
	var list struct {
		Count   int `json:"count"`
		Results []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if e := c.request(ctx, "GET", "/api/documents/?title__iexact="+url.QueryEscape(title), "", nil, &list); e != nil {
		return 0, e
	}
	var id int64
	for _, d := range list.Results {
		if d.Title != title || d.ID <= 0 || id != 0 {
			return 0, ErrResponse
		}
		id = d.ID
	}
	if list.Count != len(list.Results) {
		return 0, ErrResponse
	}
	return id, nil
}
func (c *Client) Poll(ctx context.Context, task string) (int64, error) {
	if !taskPattern.MatchString(task) {
		return 0, ErrResponse
	}
	var list []struct {
		TaskID          string          `json:"task_id"`
		Status          string          `json:"status"`
		RelatedDocument json.RawMessage `json:"related_document"`
	}
	if e := c.request(ctx, "GET", "/api/tasks/?task_id="+url.QueryEscape(task), "", nil, &list); e != nil {
		return 0, e
	}
	if len(list) != 1 || list[0].TaskID != task {
		return 0, ErrResponse
	}
	switch list[0].Status {
	case "SUCCESS":
		raw := strings.Trim(string(list[0].RelatedDocument), `"`)
		id, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || id <= 0 {
			return 0, ErrResponse
		}
		return id, nil
	case "FAILURE", "REVOKED":
		return 0, ErrRemoteFailed
	case "PENDING", "STARTED", "RETRY":
		return 0, ErrPending
	default:
		return 0, ErrResponse
	}
}
func Title(number, documentID string) string {
	return fmt.Sprintf("Invoice %s [invoice-generator:%s]", number, documentID)
}

// VerifyDocument binds adoption to both remote identity and the exact immutable
// original bytes. Paperless may generate a different archive PDF; request the
// original and hash a bounded stream instead of trusting its mutable title.
func (c *Client) VerifyDocument(ctx context.Context, id int64, title, sum string, size int64) error {
	if id <= 0 || size <= 0 || size > MaxDocumentSize || len(sum) != 64 {
		return ErrResponse
	}
	var doc struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	path := "/api/documents/" + strconv.FormatInt(id, 10) + "/"
	if e := c.request(ctx, "GET", path, "", nil, &doc); e != nil {
		return e
	}
	if doc.ID != id || doc.Title != title {
		return ErrResponse
	}
	req, e := http.NewRequestWithContext(ctx, "GET", apiURL(c.cfg.URL, path+"download/?original=true"), nil)
	if e != nil {
		return ErrRequest
	}
	setAuth(req, c.cfg.APIKey)
	resp, e := c.http.Do(req)
	if e != nil {
		return ErrRequest
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || (resp.ContentLength >= 0 && resp.ContentLength != size) {
		return ErrResponse
	}
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(resp.Body, size+1))
	if e != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != sum {
		return ErrResponse
	}
	return nil
}
