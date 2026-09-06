package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
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
func staticFS() (fs.FS, error) { return fs.Sub(embeddedFiles, "static") }
