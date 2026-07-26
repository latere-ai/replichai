// Package spa embeds the built frontend and serves it: real files from the
// build, and index.html for any unmatched path so the client-side router works.
// The bundle is public — it holds no secrets, and every data call it makes goes
// through the gated API.
package spa

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Handler returns an http.Handler that serves the embedded SPA. A path that
// maps to a real asset is served as that asset; anything else falls back to
// index.html, so a deep link like /jobs/abc loads the app and lets it route.
func Handler() (http.Handler, error) {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" && exists(dist, p) {
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}), nil
}

func exists(dir fs.FS, name string) bool {
	f, err := dir.Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
