package oauthapi

import (
	"net/http"

	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
)

var deviceRouteInventory = []string{
	"/api/admin/oauth/device/start",
}

func RegisterDeviceHandlers(mux *http.ServeMux, coord *oauth.Coordinator, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}

	innerMux := http.NewServeMux()
	root := generatedadminapi.HandlerFromMuxWithEnvelope(NewHandler(coord), innerMux)
	registerBrowserRoute(mux, chain, "POST "+deviceRouteInventory[0], root)
}

func DeviceRouteInventory() []string {
	return append([]string(nil), deviceRouteInventory...)
}
