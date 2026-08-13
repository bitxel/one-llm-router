package admin

import "net/http"

func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("POST /admin/accounts", h.CreateAccount)
	mux.HandleFunc("GET /admin/accounts", h.ListAccounts)
	mux.HandleFunc("GET /admin/accounts/{id}", h.GetAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/update", h.UpdateAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/enable", h.EnableAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/disable", h.DisableAccount)
	mux.HandleFunc("POST /admin/accounts/{id}/delete", h.DeleteAccount)

	mux.HandleFunc("GET /admin/requests", h.QueryRequests)
	mux.HandleFunc("GET /admin/sessions/resolve", h.ResolveSession)
	mux.HandleFunc("GET /admin/health", h.GetHealth)
}
