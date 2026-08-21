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
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
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
			// Hashed filenames, so the bytes at a given name never change: the
			// only correct cache lifetime is a long one, and without it every
			// visit re-downloads the whole bundle.
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			// Fonts are named by what they are rather than by their bytes, so
			// "immutable" would be a promise this server cannot keep — swapping
			// a face would leave the old one pinned in every reader's cache. A
			// long finite lifetime gets the same effect and expires.
			if strings.HasPrefix(p, "fonts/") {
				w.Header().Set("Cache-Control", "public, max-age=2592000")
			}
			files.ServeHTTP(w, r)
			return
		}
		// index.html names the hashed assets, so it must never be the stale one.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})), nil
}

// gzipPool reuses writers: the bundle is compressed on every uncached load, and
// a fresh gzip.Writer per request is a megabyte of allocation each time.
var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// compressed gzips text responses for clients that accept it.
//
// The bundle is ~350 KB of JavaScript and ~33 KB of CSS, and it was going out
// uncompressed: nothing in front of this server compresses, and net/http does
// not do it on its own. Over a slow link that is the difference between a page
// that loads and a page somebody gives up on. Roughly 3.5x here.
func compressed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || packed(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Vary is not optional: a cache that stores the gzipped body under a key
		// shared with a client that cannot read it serves that client garbage.
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		// The length is the uncompressed one and would be a lie on the wire;
		// the response is chunked instead.
		w.Header().Del("Content-Length")

		zw := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(zw)
		zw.Reset(w)
		defer zw.Close() //nolint:errcheck
		next.ServeHTTP(gzipResponse{ResponseWriter: w, w: zw}, r)
	})
}

// Formats that arrive compressed. Running them through gzip spends CPU on
// every uncached load to make the payload very slightly larger — woff2 is
// Brotli inside the container, and PNG is DEFLATE — and it costs the response
// its Content-Length, so the browser loses the progress it would have shown.
var packedExt = []string{".woff2", ".woff", ".png", ".jpg", ".webp", ".gz", ".ico"}

func packed(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range packedExt {
		if ext == e {
			return true
		}
	}
	return false
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
