package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbeddedAssetManifest(t *testing.T) {
	var manifest struct {
		Assets map[string]struct {
			SHA256  string `json:"sha256"`
			License string `json:"license"`
		} `json:"assets"`
		RemoteURLs []string `json:"remote_urls"`
	}
	b, err := embeddedFiles.ReadFile("testdata/asset-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.RemoteURLs) != 0 {
		t.Fatalf("remote assets are forbidden: %v", manifest.RemoteURLs)
	}
	for _, name := range []string{"app.css", "htmx.min.js", "icons.svg"} {
		entry, ok := manifest.Assets[name]
		if !ok || entry.License == "" || entry.SHA256 == "" {
			t.Fatalf("manifest lacks hash or license metadata for %s", name)
		}
		asset, err := assetBytes(name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(asset)
		if entry.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("manifest hash mismatch for %s", name)
		}
		content := strings.ToLower(string(asset))
		if strings.Contains(content, "src=\"http") || strings.Contains(content, "href=\"http") || strings.Contains(content, "url(http") {
			t.Fatalf("asset %s refers to a remote URL", name)
		}
	}
}
