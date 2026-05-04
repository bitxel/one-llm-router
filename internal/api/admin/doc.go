// Package admin implements JSON handlers for /api/admin/* (router-owned admin
// APIs). Browser HTML under /admin/* is served by the SPA, not this package.
//
// 002 layers the envelope adapter (internal/api/adminapi.WrappedHandler) on
// top of these handlers; the raw JSON shapes here are the "inner" 001 format
// that wrap.go translates onto {code,msg,data}. Keep the native shapes
// untouched — the adapter relies on the status+body contract documented on
// each handler.
package admin
