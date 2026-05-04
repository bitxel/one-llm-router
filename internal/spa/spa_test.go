package spa

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesIndexAtRoot(t *testing.T) {
	if IsEmpty() {
		t.Skip("SPA embed missing (only .gitkeep sentinel); run `make spa-stage` to populate")
	}
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("root: got status %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "<!doctype html>") {
		t.Errorf("root body does not look like an HTML doc; got %q (first 120)", truncate(string(body)))
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("root Cache-Control = %q, want no-store", cc)
	}
}

func TestHandler_FallsBackToIndexForUnknownClientRoute(t *testing.T) {
	if IsEmpty() {
		t.Skip("SPA embed missing (frontend/dist not built); fallback coverage requires a built bundle")
	}
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/deep/link/that/does/not/exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("deep link: got status %d, want 200 (SPA fallback)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", ct)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store (so browsers re-fetch after deploy)", cache)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q, want nosniff", nosniff)
	}
}

// TestHandler_MissingAssetReturns404 locks in the F006 rule: a
// request for a hashed asset that no longer exists (e.g. after a
// redeploy with a stale HTML cached on the client) MUST 404, not
// fall back to index.html. Returning HTML with Content-Type
// text/html against a <script src=".../app.js"> tag triggers a
// MIME-mismatch error in the browser and hides the real cause.
func TestHandler_MissingAssetReturns404(t *testing.T) {
	if IsEmpty() {
		t.Skip("SPA embed missing")
	}
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/assets/stale-hash.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type: got %q, want text/plain", ct)
	}
}

// TestHandler_HashedAssetGetsImmutableCache verifies the Cache-Control
// contract for content-hashed bundles: max-age=1y + immutable so
// browsers stop revalidating, but index.html stays no-store so the
// operator's next redeploy is visible on the first navigation.
func TestHandler_HashedAssetGetsImmutableCache(t *testing.T) {
	if IsEmpty() {
		t.Skip("SPA embed missing")
	}
	h := Handler()
	// Probe the actual embed for a real asset filename.
	asset := findFirstHashedAsset(t)
	if asset == "" {
		t.Skip("no content-hashed asset found in embed")
	}
	req := httptest.NewRequest(http.MethodGet, "/"+asset, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset %s: status %d, want 200", asset, rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("hashed asset Cache-Control = %q, want max-age=31536000, immutable", cc)
	}
}

func TestHandler_RejectsNonGetMethods(t *testing.T) {
	h := Handler()
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(m, func(t *testing.T) {
			req := httptest.NewRequest(m, "/", strings.NewReader(""))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s /: status %d, want 405", m, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s / Allow = %q, want GET, HEAD", m, allow)
			}
		})
	}
}

func TestHandler_HeadRequestSucceeds(t *testing.T) {
	if IsEmpty() {
		t.Skip("SPA embed missing (only .gitkeep sentinel); run `make spa-stage` to populate")
	}
	h := Handler()
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /: status %d, want 200", rec.Code)
	}
}

// findFirstHashedAsset walks dist/assets/ looking for any embedded
// file. Returns "" when the embed is empty or lacks an assets dir
// so callers can t.Skip.
func findFirstHashedAsset(t *testing.T) string {
	t.Helper()
	entries, err := embedded.ReadDir("dist/assets")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		return "assets/" + e.Name()
	}
	return ""
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
