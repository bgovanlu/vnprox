package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// newSPAHandler serves static files out of distFS, falling back to
// index.html for any path that isn't a real file in the tree — the
// standard "SPA fallback" so client-side routes (e.g. /topology, /sdn)
// work on a hard refresh instead of 404ing. distFS is expected to be
// rooted at the frontend build output (i.e. index.html is at its root),
// not at a "dist" prefix — callers strip that prefix (see cmd/vnproxd).
func newSPAHandler(distFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if distFS == nil {
			http.NotFound(w, r)
			return
		}

		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" || clean == "." {
			clean = "index.html"
		}

		if info, err := fs.Stat(distFS, clean); err != nil || info.IsDir() {
			// Unknown route (or a directory without an index): let the SPA's
			// router handle it client-side.
			serveIndex(w, r, fileServer)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fileServer http.Handler) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	fileServer.ServeHTTP(w, r2)
}
