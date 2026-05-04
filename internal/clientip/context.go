// Package clientip stores the router-observed downstream client IP in context.
package clientip

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
)

type contextKey struct{}

func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(contextKey{}).(string)
	return v
}

// WithContext stores a normalized IP address on ctx. Empty or unparsable values
// are stored as "" so downstream request-log records do not carry forged text.
func WithContext(ctx context.Context, ip string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, FromRemoteAddr(ip))
}

// FromRequest returns the client IP already installed on the request context,
// falling back to Request.RemoteAddr when a handler is exercised without the
// normal middleware chain.
func FromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := FromContext(r.Context()); ip != "" {
		return ip
	}
	return FromRemoteAddr(r.RemoteAddr)
}

// FromRemoteAddr extracts the host component from net/http Request.RemoteAddr.
// It deliberately ignores X-Forwarded-For and X-Real-IP because this project
// does not yet model trusted proxy boundaries; trusting those headers would let
// arbitrary clients forge request-log audit data.
func FromRemoteAddr(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return ""
	}
	if addrPort, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return normalizeAddr(addrPort.Addr())
	}
	if strings.HasPrefix(remoteAddr, "[") || strings.HasSuffix(remoteAddr, "]") {
		if !(strings.HasPrefix(remoteAddr, "[") && strings.HasSuffix(remoteAddr, "]")) {
			return ""
		}
		remoteAddr = strings.TrimPrefix(strings.TrimSuffix(remoteAddr, "]"), "[")
	}
	addr, err := netip.ParseAddr(remoteAddr)
	if err != nil {
		return ""
	}
	return normalizeAddr(addr)
}

func normalizeAddr(addr netip.Addr) string {
	if !addr.IsValid() || addr.Zone() != "" {
		return ""
	}
	return addr.Unmap().String()
}
