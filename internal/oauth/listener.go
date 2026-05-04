package oauth

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	loopbackPortMin = 1455
	loopbackPortMax = 1455
)

func BindLoopback(handler http.Handler) (*http.Server, bool, int, error) {
	if handler == nil {
		return nil, false, 0, errors.New("oauth.BindLoopback: handler is nil")
	}

	for port := loopbackPortMin; port <= loopbackPortMax; port++ {
		listeners := bindLoopbackPort(port)
		if len(listeners) == 0 {
			continue
		}

		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}

		serveLoopbackListeners(srv, listeners)
		return srv, true, port, nil
	}

	return nil, false, 0, nil
}

func ParseLoopbackCallbackURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("oauth.ParseLoopbackCallbackURL: parse url: %w", err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "localhost" || parsed.Path != "/auth/callback" {
		return nil, errors.New("oauth.ParseLoopbackCallbackURL: url prefix mismatch")
	}
	port := parsed.Port()
	if port == "" {
		return nil, errors.New("oauth.ParseLoopbackCallbackURL: url prefix mismatch")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < loopbackPortMin || portNumber > loopbackPortMax {
		return nil, errors.New("oauth.ParseLoopbackCallbackURL: url prefix mismatch")
	}
	return parsed, nil
}

func bindLoopbackPort(port int) []net.Listener {
	addresses := []struct {
		network string
		address string
	}{
		{network: "tcp4", address: fmt.Sprintf("127.0.0.1:%d", port)},
		{network: "tcp6", address: fmt.Sprintf("[::1]:%d", port)},
	}

	listeners := make([]net.Listener, 0, len(addresses))
	for _, candidate := range addresses {
		ln, err := net.Listen(candidate.network, candidate.address)
		if err != nil {
			continue
		}
		listeners = append(listeners, ln)
	}
	return listeners
}

func serveLoopbackListeners(srv *http.Server, listeners []net.Listener) {
	for _, ln := range listeners {
		go func(listener net.Listener) {
			err := srv.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				_ = listener.Close()
			}
		}(ln)
	}
}
