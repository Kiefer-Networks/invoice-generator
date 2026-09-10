//go:build production

package web

import (
	"errors"
	"net/http"
)

func reloadHandler(Dependencies) (http.Handler, error) {
	return nil, errors.New("unsupported configuration")
}
