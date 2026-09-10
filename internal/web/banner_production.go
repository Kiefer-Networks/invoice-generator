//go:build production

package web

import "html/template"

func developmentBanner(bool) template.HTML { return "" }
