//go:build !production

package web

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
)

func developmentFiles(path string) (fs.ReadFileFS, func(), error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	return root.FS().(fs.ReadFileFS), func() { _ = root.Close() }, nil
}

func reloadHandler(deps Dependencies) (http.Handler, error) {
	if !deps.Config.Development {
		return nil, errors.New("asset reload requires development")
	}
	files, closeRoot, err := developmentFiles(deps.Config.DevAssetsDir)
	if err != nil {
		return nil, err
	}
	_, err = newWithFiles(deps, files)
	closeRoot()
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files, closeRoot, err := developmentFiles(deps.Config.DevAssetsDir)
		if err != nil {
			http.Error(w, "assets unavailable", http.StatusServiceUnavailable)
			return
		}
		defer closeRoot()
		handler, err := newWithFiles(deps, files)
		if err != nil {
			http.Error(w, "assets unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}), nil
}
