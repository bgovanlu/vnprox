// Package webui embeds the built frontend (web/dist) into the vnproxd
// binary so it can be served without any files on disk at runtime.
//
// Go's //go:embed cannot reference a path containing ".." (e.g.
// internal/api can't embed "../../web/dist" directly), and it fails to
// compile against a missing directory. This tiny shim exists to satisfy
// both constraints at once: it lives next to web/dist (no ".." needed) and
// web/dist always contains at least a placeholder index.html (see
// web/dist/index.html) so the //go:embed directive below always has
// something to embed, even before T-005's real frontend build exists.
//
// Contract for T-005: `npm run build` (wired into `make build`) must emit
// its production output directly into web/dist/, overwriting the
// placeholder in place (index.html included). No changes to this file, to
// internal/api, or to cmd/vnproxd are required when that lands — DistFS
// below will simply start serving the real app.
package webui

import "embed"

// DistFS is the embedded contents of web/dist, rooted at "dist" (i.e.
// DistFS's "dist/index.html" is the site root). Callers should take
// fs.Sub(DistFS, "dist") to get an fs.FS rooted at the site root itself.
//
//go:embed all:dist
var DistFS embed.FS
