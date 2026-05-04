// Package spa embeds the staged React SPA (`internal/spa/dist/*`),
// which is copied from `frontend/dist/*` before building the router
// binary, and exposes an http.Handler that serves it under `/setup/`
// and `/admin/`. Missing sub-paths fall back to `index.html` so that
// TanStack Router can handle client-side navigation without a hard
// 404 on deep links (e.g. a browser refresh on /admin/settings).
//
// The embed is the reason `make build` runs `pnpm -C frontend build`
// first and then stages the result into `internal/spa/dist`: without
// a populated staged bundle the embed would contain only the
// `.gitkeep` sentinel and `/setup/` would render as "router build is
// missing frontend assets".
//
// This package is deliberately free of any routing policy beyond the
// SPA fallback. The setup gate (internal/setup/gate.go) decides
// whether `/admin/*` is reachable at all; once it is, this handler
// serves the bytes.
package spa

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// BuildMissingMessage is the plain-text fallback served when the
// embed has no `index.html` — almost certainly because the operator
// ran `go build` directly without first staging the frontend bundle.
// We prefer a clear text banner to a silent 404 so the mistake is
// obvious in a terminal or smoke test.
const BuildMissingMessage = `router build is missing the frontend bundle.

Run 'make build' (or 'pnpm -C frontend build' followed by
'make go-build') to produce a binary with the SPA embedded.
`

// hashedAssetPrefix is the canonical Vite output directory for
// content-hashed JS/CSS/image bundles. Anything under this prefix
// MUST be served with long-lived cache headers; anything outside of
// it (including index.html) MUST be cacheless so that a redeploy
// flips UI versions on the very next fetch.
const hashedAssetPrefix = "assets/"

// Handler returns the SPA handler. It serves files from the embedded
// filesystem and falls back to `index.html` for any path that looks
// like a client-side route (no file extension) — required for
// TanStack Router's client-side routing on refresh or deep-link.
//
// Requests that look like static assets (contain a `.` in the final
// path segment) instead return HTTP 404 when missing so that a build
// mistake surfaces as a JS/CSS load error in DevTools rather than a
// confusing "index.html served as application/javascript" MIME mismatch.
//
// Only GET and HEAD are accepted. Any other method yields 405 with an
// `Allow: GET, HEAD` response header so operators diagnosing a bad
// curl against `/admin/` get an explicit protocol answer rather than
// a silently-ignored index.html body.
//
// The handler MUST be registered at both `/setup/` and `/admin/`
// because those prefixes are stripped from the request path before
// it reaches this handler (see internal/app/app.go).
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(BuildMissingMessage))
		})
	}

	fileServer := http.FileServer(http.FS(sub))

	index, indexErr := fs.ReadFile(sub, "index.html")
	hasIndex := indexErr == nil

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only idempotent safe methods are meaningful for a static
		// SPA. Everything else is a protocol mistake — respond 405
		// with the allow-list so clients get an actionable hint
		// (browsers already only send GET/HEAD; this is for curl
		// smoke tests, admin tooling, and misconfigured probes).
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}

		_, statErr := fs.Stat(sub, name)
		switch {
		case statErr == nil:
			setCacheHeaders(w, name)
			fileServer.ServeHTTP(w, r)
			return
		case errors.Is(statErr, fs.ErrNotExist):
			if looksLikeAsset(name) {
				// Missing JS/CSS/image: 404 explicitly so the
				// browser fails the script tag with a protocol
				// error instead of loading index.html and tripping
				// a MIME mismatch in the console.
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "asset not found", http.StatusNotFound)
				return
			}
			if !hasIndex {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(BuildMissingMessage))
				return
			}
			// SPA fallback — client-side router takes over.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(index)
			return
		default:
			http.Error(w, "spa read error", http.StatusInternalServerError)
			return
		}
	})
}

// setCacheHeaders stamps the response with the right Cache-Control
// semantics: content-hashed bundles are safe to cache forever (their
// filename changes on every build), but everything else — most
// importantly index.html — must be fetched fresh so a redeploy flips
// the UI on the next page load.
func setCacheHeaders(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, hashedAssetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
}

// looksLikeAsset reports whether `name` names a static asset rather
// than a client-side route. The heuristic is: the final path segment
// contains a `.`. Client-side routes in this app never include a dot
// (e.g. /admin/settings, /setup/db). This is intentionally a simple
// string check rather than an extension allow-list so the frontend
// team can add new asset types without coupling here.
func looksLikeAsset(name string) bool {
	base := path.Base(name)
	return strings.Contains(base, ".")
}

// IsEmpty reports whether the embed contains no SPA bundle. Boot
// logs call this to warn the operator explicitly rather than waiting
// for the first HTTP hit.
func IsEmpty() bool {
	_, err := fs.Stat(embedded, "dist/index.html")
	return err != nil
}
