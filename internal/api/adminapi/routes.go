package adminapi

import "net/http"

// RegisterRoutes wires the 002 admin JSON API endpoints into mux.
// The caller is responsible for wrapping mux with the gate middleware
// (internal/setup.Gate.Wrap) so setup-pending state is handled
// uniformly; RegisterRoutes itself is gate-agnostic.
//
// Route map (admin-api.md §Routing decision table):
//
//	GET  /api/admin/settings           — SettingsHandler.Get
//	POST /api/admin/settings/update    — SettingsHandler.Update
//	GET  /api/admin/health             — WrappedHandler.GetHealth (always reachable)
//	GET  /api/admin/accounts           — WrappedHandler.ListAccounts
//	POST /api/admin/accounts           — WrappedHandler.CreateAccount
//	GET  /api/admin/accounts/{id}      — WrappedHandler.GetAccount
//	POST /api/admin/accounts/{id}/enable   — EnableAccount
//	POST /api/admin/accounts/{id}/disable  — DisableAccount
//	POST /api/admin/accounts/{id}/delete   — DeleteAccount
//	GET  /api/admin/requests           — QueryRequests
//	GET  /api/admin/sessions/resolve   — ResolveSession
//
// Only settings is 002-native; every other route is 001 logic that
// has been envelope-wrapped by WrappedHandler.
func RegisterRoutes(mux *http.ServeMux, settings *SettingsHandler, wrapped *WrappedHandler) {
	mux.HandleFunc("GET /api/admin/settings", settings.Get)
	mux.HandleFunc("POST /api/admin/settings/update", settings.Update)

	mux.HandleFunc("GET /api/admin/health", wrapped.GetHealth)

	mux.HandleFunc("POST /api/admin/accounts", wrapped.CreateAccount)
	mux.HandleFunc("GET /api/admin/accounts", wrapped.ListAccounts)
	mux.HandleFunc("GET /api/admin/accounts/{id}", wrapped.GetAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/enable", wrapped.EnableAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/disable", wrapped.DisableAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/delete", wrapped.DeleteAccount)

	mux.HandleFunc("GET /api/admin/requests", wrapped.QueryRequests)
	mux.HandleFunc("GET /api/admin/sessions/resolve", wrapped.ResolveSession)
}
