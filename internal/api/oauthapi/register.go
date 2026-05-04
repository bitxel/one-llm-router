package oauthapi

import (
	"net/http"

	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
)

var browserRouteInventory = []string{
	"/api/admin/oauth/browser/start",
	"/api/admin/oauth/browser/manual-callback",
	"/api/admin/oauth/flow",
	"/api/admin/oauth/cancel",
}

func RegisterBrowserHandlers(mux *http.ServeMux, coord *oauth.Coordinator, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}

	innerMux := http.NewServeMux()
	root := generatedadminapi.HandlerFromMuxWithEnvelope(NewHandler(coord), innerMux)

	registerBrowserRoute(mux, chain, "POST "+browserRouteInventory[0], root)
	registerBrowserRoute(mux, chain, "POST "+browserRouteInventory[1], root)
	registerBrowserRoute(mux, chain, "GET "+browserRouteInventory[2], root)
	registerBrowserRoute(mux, chain, "POST "+browserRouteInventory[3], root)
}

func BrowserRouteInventory() []string {
	return append([]string(nil), browserRouteInventory...)
}

func registerBrowserRoute(mux *http.ServeMux, chain func(http.Handler) http.Handler, pattern string, root http.Handler) {
	mux.Handle(pattern, chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root.ServeHTTP(w, r)
	})))
}
