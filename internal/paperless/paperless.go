// Package paperless uploads a generated invoice/quote PDF to a
// Paperless-ngx instance (https://docs.paperless-ngx.com/api/) via its
// REST API, tagging it by name (creating any tag that doesn't exist yet).
package paperless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// Config holds the settings needed to upload a document to Paperless-ngx.
type Config struct {
	URL    string   `yaml:"url" toml:"url"`
	APIKey string   `yaml:"api_key" toml:"api_key"`
	Tags   []string `yaml:"tags" toml:"tags"`
}

// maxConfigFileSize bounds how large the paperless config file may be —
// it only ever holds a URL, a key, and a short tag list.
const maxConfigFileSize = 1 << 20 // 1 MiB

// Load reads a Paperless config from a YAML or TOML file, selected by
// file extension (defaulting to YAML for anything else).
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxConfigFileSize {
		return nil, fmt.Errorf("paperless config file too large (%d bytes, max %d)", info.Size(), maxConfigFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if strings.ToLower(filepath.Ext(path)) == ".toml" {
		err = toml.Unmarshal(data, cfg)
	} else {
		err = yaml.Unmarshal(data, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("could not parse paperless config: %w", err)
	}

	if cfg.URL == "" {
		return nil, fmt.Errorf("paperless config: missing required field \"url\"")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("paperless config: missing required field \"api_key\"")
	}
	return cfg, nil
}

// LocalOverridePath mirrors config.LocalOverridePath: "paperless.yaml"
// -> "paperless.local.yaml". Lets a real API key be kept out of version
// control the same way real company/bank data is (see
// internal/config.LoadWithLocalOverride) — *.local.* is gitignored by
// default.
func LocalOverridePath(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + ".local" + ext
}

// LoadWithLocalOverride loads path, then — if a sibling
// "<name>.local.<ext>" file exists — loads that too and lets its
// non-empty fields take precedence over the base file.
func LoadWithLocalOverride(path string) (*Config, string, error) {
	base, err := Load(path)
	if err != nil {
		return nil, "", err
	}

	overridePath := LocalOverridePath(path)
	if _, statErr := os.Stat(overridePath); statErr != nil {
		return base, "", nil
	}

	override, err := Load(overridePath)
	if err != nil {
		return nil, "", fmt.Errorf("error loading local override %s: %w", overridePath, err)
	}
	if override.URL == "" {
		override.URL = base.URL
	}
	if override.APIKey == "" {
		override.APIKey = base.APIKey
	}
	if len(override.Tags) == 0 {
		override.Tags = base.Tags
	}
	return override, overridePath, nil
}

// uploadTimeout bounds the whole upload (tag resolution + file upload),
// so an unreachable or hanging Paperless instance cannot block the CLI
// indefinitely.
const uploadTimeout = 60 * time.Second

// Upload sends filePath to the configured Paperless-ngx instance's
// "post_document" endpoint, resolving (and creating, if necessary) each
// of cfg.Tags by name, and titling the document with title.
func Upload(cfg *Config, filePath, title string) error {
	warnIfInsecure(cfg.URL)

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()
	client := &http.Client{Timeout: uploadTimeout}

	var tagIDs []int
	for _, name := range cfg.Tags {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		id, err := resolveOrCreateTag(ctx, client, cfg, name)
		if err != nil {
			return fmt.Errorf("could not resolve tag %q: %w", name, err)
		}
		tagIDs = append(tagIDs, id)
	}

	return postDocument(ctx, client, cfg, filePath, title, tagIDs)
}

// warnIfInsecure flags a plain-HTTP Paperless URL (other than localhost),
// since the API key travels in a header on every request.
func warnIfInsecure(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" {
		return
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return
	}
	fmt.Fprintln(os.Stderr, "Warning: paperless.url uses plain HTTP — your API key is sent unencrypted on every request. Use HTTPS if at all possible.")
}

func apiURL(base, p string) string {
	return strings.TrimRight(base, "/") + p
}

type tagListResponse struct {
	Count   int `json:"count"`
	Results []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"results"`
}

type tagCreateResponse struct {
	ID int `json:"id"`
}

// resolveOrCreateTag looks up a Paperless tag by exact name (case
// sensitive match on the API's own case-insensitive filter, then
// verified client-side) and creates it if it doesn't exist yet.
func resolveOrCreateTag(ctx context.Context, client *http.Client, cfg *Config, name string) (int, error) {
	lookupURL := apiURL(cfg.URL, "/api/tags/?name__iexact="+url.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookupURL, nil)
	if err != nil {
		return 0, err
	}
	setAuth(req, cfg.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("contacting Paperless: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status %d looking up tag: %s", resp.StatusCode, readErrBody(resp.Body))
	}

	var list tagListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return 0, fmt.Errorf("could not parse tag list response: %w", err)
	}
	for _, t := range list.Results {
		if strings.EqualFold(t.Name, name) {
			return t.ID, nil
		}
	}

	// Not found — create it.
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return 0, err
	}
	createURL := apiURL(cfg.URL, "/api/tags/")
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	setAuth(req, cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("contacting Paperless: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("unexpected status %d creating tag: %s", resp.StatusCode, readErrBody(resp.Body))
	}
	var created tagCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return 0, fmt.Errorf("could not parse tag creation response: %w", err)
	}
	return created.ID, nil
}

// postDocument uploads filePath as multipart/form-data to Paperless-ngx's
// consumption endpoint.
func postDocument(ctx context.Context, client *http.Client, cfg *Config, filePath, title string, tagIDs []int) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("could not open %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	part, err := mw.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("could not read %s: %w", filePath, err)
	}
	if title != "" {
		if err := mw.WriteField("title", title); err != nil {
			return err
		}
	}
	for _, id := range tagIDs {
		if err := mw.WriteField("tags", fmt.Sprintf("%d", id)); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}

	uploadURL := apiURL(cfg.URL, "/api/documents/post_document/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return err
	}
	setAuth(req, cfg.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("contacting Paperless: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("paperless upload failed with status %d: %s", resp.StatusCode, readErrBody(resp.Body))
	}
	return nil
}

func setAuth(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Token "+apiKey)
}

// readErrBody returns a short, safe-to-print snippet of a failed
// response body for error messages (bounded so a misbehaving server
// can't dump megabytes into the CLI's stderr).
func readErrBody(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 4096))
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "(empty response)"
	}
	return s
}
