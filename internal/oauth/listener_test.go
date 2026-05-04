package oauth

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBindLoopback(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		expectedPort, ok := firstAvailableLoopbackPort(t)
		if !ok {
			t.Skip("loopback port 1455 unavailable on this host")
		}

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/auth/callback" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte("ok"))
		})

		srv, bound, port, err := BindLoopback(handler)
		if !assert.NoError(t, err) {
			return
		}
		if !assert.True(t, bound) {
			return
		}
		assert.Equal(t, expectedPort, port)

		assert.Eventually(t, func() bool {
			return fetchLoopback(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/auth/callback") == "ok"
		}, 2*time.Second, 20*time.Millisecond)

		if supportsIPv6Loopback(t) {
			assert.Eventually(t, func() bool {
				return fetchLoopback(t, "http://[::1]:"+strconv.Itoa(port)+"/auth/callback") == "ok"
			}, 2*time.Second, 20*time.Millisecond)
		}

		assert.NoError(t, srv.Close())
	})

	t.Run("nil handler rejected", func(t *testing.T) {
		srv, bound, port, err := BindLoopback(nil)
		assert.Error(t, err)
		assert.Nil(t, srv)
		assert.False(t, bound)
		assert.Zero(t, port)
	})

	t.Run("all candidate ports occupied", func(t *testing.T) {
		reserveLoopbackPort(t, 1455)

		srv, bound, port, err := BindLoopback(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		assert.NoError(t, err)
		assert.Nil(t, srv)
		assert.False(t, bound)
		assert.Zero(t, port)
	})

	t.Run("does not bind fallback port when 1455 is occupied", func(t *testing.T) {
		reserveLoopbackPort(t, 1455)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("unexpected"))
		})

		srv, bound, port, err := BindLoopback(handler)
		assert.NoError(t, err)
		assert.Nil(t, srv)
		assert.False(t, bound)
		assert.Zero(t, port)
	})

	t.Run("single family success still binds port", func(t *testing.T) {
		if !supportsIPv6Loopback(t) {
			t.Skip("ipv6 loopback unavailable on this host")
		}

		ln, err := net.Listen("tcp4", "127.0.0.1:1455")
		if err != nil {
			t.Skipf("unable to reserve ipv4 1455: %v", err)
		}
		defer func() { _ = ln.Close() }()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ipv6-only"))
		})

		srv, bound, port, err := BindLoopback(handler)
		if !assert.NoError(t, err) {
			return
		}
		if !assert.True(t, bound) {
			return
		}
		assert.Equal(t, 1455, port)
		assert.Eventually(t, func() bool {
			return fetchLoopback(t, "http://[::1]:1455/auth/callback") == "ipv6-only"
		}, 2*time.Second, 20*time.Millisecond)
		assert.NoError(t, srv.Close())
	})
}

func reserveLoopbackPort(t *testing.T, port int) {
	t.Helper()

	candidates := []struct {
		network string
		address string
	}{
		{network: "tcp4", address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))},
		{network: "tcp6", address: "[::1]:" + strconv.Itoa(port)},
	}

	for _, candidate := range candidates {
		ln, err := net.Listen(candidate.network, candidate.address)
		if err != nil {
			continue
		}
		t.Cleanup(func() {
			_ = ln.Close()
		})
	}
}

func supportsIPv6Loopback(t *testing.T) bool {
	t.Helper()

	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func fetchLoopback(t *testing.T, target string) string {
	t.Helper()

	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}

	resp, err := client.Get(target)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

func firstAvailableLoopbackPort(t *testing.T) (int, bool) {
	t.Helper()

	for port := loopbackPortMin; port <= loopbackPortMax; port++ {
		if canBindLoopbackPort(port) {
			return port, true
		}
	}
	return 0, false
}

func canBindLoopbackPort(port int) bool {
	addresses := []struct {
		network string
		address string
	}{
		{network: "tcp4", address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))},
		{network: "tcp6", address: "[::1]:" + strconv.Itoa(port)},
	}

	opened := make([]net.Listener, 0, len(addresses))
	for _, candidate := range addresses {
		ln, err := net.Listen(candidate.network, candidate.address)
		if err != nil {
			continue
		}
		opened = append(opened, ln)
	}
	for _, ln := range opened {
		_ = ln.Close()
	}
	return len(opened) > 0
}
