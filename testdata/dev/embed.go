// Package fixtures contains synthetic development data only.
package fixtures

import "embed"

//go:embed *.json
var Files embed.FS
