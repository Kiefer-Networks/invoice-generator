//go:build !production

package web

import "html/template"

func developmentBanner(enabled bool) template.HTML {
	if !enabled {
		return ""
	}
	return `<p class="development-banner" data-testid="development-banner" role="status">LOCAL DEVELOPMENT — synthetic data only</p>`
}
