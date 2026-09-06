// Package paperless uploads a generated invoice/quote PDF to a
// Paperless-ngx instance (https://docs.paperless-ngx.com/api/) via its
// REST API, tagging it by name (creating any tag that doesn't exist yet).
package paperless

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("paperless config unavailable")
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, fmt.Errorf("paperless config must be a protected regular file")
	}
	if info.Size() > maxConfigFileSize {
		return nil, fmt.Errorf("paperless config file too large (%d bytes, max %d)", info.Size(), maxConfigFileSize)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("paperless config unavailable")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("paperless config changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileSize+1))
	if err != nil || len(data) > maxConfigFileSize {
		return nil, fmt.Errorf("paperless config unreadable or too large")
	}

	cfg := &Config{}
	if strings.ToLower(filepath.Ext(path)) == ".toml" {
		err = toml.Unmarshal(data, cfg)
	} else {
		err = yaml.Unmarshal(data, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("could not parse paperless config")
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

// Upload preserves the CLI entry point and caller-selected tags. Loopback HTTP
// remains available for CLI fixtures; every other destination requires HTTPS.
func Upload(cfg *Config, filePath, title string) error {
	if cfg == nil {
		return ErrRequest
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, e := NewClient(*cfg, nil, true)
	if e != nil {
		return e
	}
	tags, e := client.resolveTags(ctx, cfg.Tags)
	if e != nil {
		return e
	}
	f, e := os.Open(filePath)
	if e != nil {
		return ErrRequest
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return ErrRequest
	}
	_, e = client.submit(ctx, f, info.Size(), title, tags, false)
	return e
}
func apiURL(base, p string) string { return strings.TrimRight(base, "/") + p }

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

func setAuth(req *http.Request, key string) {
	req.Header.Set("Authorization", "Token "+key)
	req.Header.Set("Accept", "application/json; version=10")
}
