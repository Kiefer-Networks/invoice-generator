package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed static/* templates/* testdata/asset-manifest.json
var embeddedFiles embed.FS

func assetBytes(name string) ([]byte, error) { return embeddedFiles.ReadFile("static/" + name) }
func assetVersion(name string) string {
	b, err := assetBytes(name)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
