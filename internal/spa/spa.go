// Package spa embeds the built frontend and serves it: real files from the
// build, and index.html for any unmatched path so the client-side router works.
// The bundle is public — it holds no secrets, and every data call it makes goes
// through the gated API.
//
// The real build (frontend/ via bun) is written into dist/ and is gitignored; a
// committed PLACEHOLDER keeps the embed non-empty so `go build`/`go test` work
// without a frontend build. When index.html is absent (no build), the handler
// serves a small notice instead of failing.
package spa

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// notBuilt is served when the frontend has not been built into this binary.
const notBuilt = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>replichai</title></head>` +
	`<body style="font-family:sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem;color:#333">` +
	`<h1>replichai</h1><p>The frontend is not built into this binary. Build it with ` +
	`<code>cd frontend &amp;&amp; bun install &amp;&amp; bun run build</code> and rebuild the server.</p></body></html>`

// Handler returns an http.Handler that serves the embedded SPA. A path that maps
// to a real asset is served as that asset; anything else falls back to
// index.html so a deep link loads the app and lets it route.
func Handler() (http.Handler, error) {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		index = []byte(notBuilt) // no frontend build in this binary
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
