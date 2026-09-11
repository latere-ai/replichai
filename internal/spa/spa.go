// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package spa embeds the built frontend and serves it: real files from the
// build, and index.html for any unmatched path so the client-side router works.
// The bundle is public: it holds no secrets, and every data call it makes goes
// through the gated API.
//
// The real build, produced from frontend/ by bun, is written into dist/ and is
// gitignored. A committed PLACEHOLDER keeps the embed non-empty so `go build`
// and `go test` work without a frontend build. When index.html is absent, the
// handler serves a small notice instead of failing.
package spa

import (
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/latere-ai/replichai/internal/seo"
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
	return compressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" && exists(dist, p) {
			// Asset filenames are hashed, so the bytes at a given name never
			// change. A long cache lifetime is therefore correct, and without
			// it every visit re-downloads the whole bundle.
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			// Fonts are named by face rather than by content hash, so
			// "immutable" would be wrong: swapping a face would leave the old
			// one pinned in every reader's cache. A long finite lifetime gets
			// most of the benefit and still expires.
			if strings.HasPrefix(p, "fonts/") {
				w.Header().Set("Cache-Control", "public, max-age=2592000")
			}
			files.ServeHTTP(w, r)
			return
		}
		// index.html names the hashed assets, so it must never be served stale.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		seo.WriteShell(w, r, index)
	})), nil
}

// gzipPool reuses writers. The bundle is compressed on every uncached load, and
// a fresh gzip.Writer per request would allocate about a megabyte each time.
var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// compressed gzips text responses for clients that accept it.
//
// The bundle is about 350 KB of JavaScript and 33 KB of CSS. Nothing in front
// of this server compresses, and net/http does not compress on its own, so
// without this the bundle went out uncompressed. Over a slow link that decides
// whether the page loads before the reader gives up. gzip reduces it roughly
// 3.5x.
func compressed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || packed(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Vary is required. Without it a cache could store the gzipped body
		// under a key shared with a client that cannot decode gzip, and serve
		// that client unreadable bytes.
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		// Any Content-Length set upstream is the uncompressed length and would
		// be wrong on the wire. It is removed and the response is chunked.
		w.Header().Del("Content-Length")
		// A range request cannot be honoured on a body compressed on the way
		// out: the file server would set a Content-Length for a slice of the
		// uncompressed file over a gzipped body. HTTP allows a server to
		// ignore the range, so the whole asset is served.
		r.Header.Del("Range")

		zw, ok := gzipPool.Get().(*gzip.Writer)
		if !ok {
			// The pool's New only ever makes a *gzip.Writer, so any other type
			// is a programming error, not a reason to serve uncompressed.
			panic("spa: gzip pool holds something that is not a *gzip.Writer")
		}
		defer gzipPool.Put(zw)
		zw.Reset(w)
		defer zw.Close() //nolint:errcheck
		next.ServeHTTP(gzipResponse{ResponseWriter: w, w: zw}, r)
	})
}

// packedExt lists formats that are already compressed. Running them through
// gzip spends CPU on every uncached load to make the payload slightly larger,
// since woff2 is Brotli inside its container and PNG is DEFLATE, and it costs
// the response its Content-Length, so the browser loses its download progress.
var packedExt = []string{".woff2", ".woff", ".png", ".jpg", ".webp", ".gz", ".ico"}

func packed(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return slices.Contains(packedExt, ext)
}

// gzipResponse sends the body through the compressor while headers and status
// keep going to the real writer.
type gzipResponse struct {
	http.ResponseWriter
	w *gzip.Writer
}

func (g gzipResponse) Write(b []byte) (int, error) { return g.w.Write(b) }

func exists(dir fs.FS, name string) bool {
	f, err := dir.Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
