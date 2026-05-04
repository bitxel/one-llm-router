package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/config"
)

func TestResolveLogLevel_Mapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"debug", slog.LevelDebug},
		{"  DEBUG ", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"bogus", slog.LevelInfo},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			got := ResolveLogLevel(c.in)
			if got != c.want {
				t.Fatalf("ResolveLogLevel(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestApplyLogLevel_LiveSwap(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv}))

	logger.Debug("masked pre-change")
	if strings.Contains(buf.String(), "masked pre-change") {
		t.Fatalf("debug line leaked while level=Warn: %q", buf.String())
	}

	applyLogLevel(lv, &config.Config{Runtime: config.RuntimeConfig{LogLevel: "debug"}})
	logger.Debug("visible after swap")
	if !strings.Contains(buf.String(), "visible after swap") {
		t.Fatalf("debug line dropped after flip to debug: %q", buf.String())
	}

	applyLogLevel(lv, &config.Config{Runtime: config.RuntimeConfig{LogLevel: "error"}})
	buf.Reset()
	logger.Warn("masked after second swap")
	if buf.Len() != 0 {
		t.Fatalf("warn line leaked at level=Error: %q", buf.String())
	}
}

func TestApplyLogLevel_NilInputsAreNoop(t *testing.T) {
	t.Parallel()
	applyLogLevel(nil, &config.Config{Runtime: config.RuntimeConfig{LogLevel: "debug"}})
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelError)
	applyLogLevel(lv, nil)
	if lv.Level() != slog.LevelError {
		t.Fatalf("nil cfg must not mutate level; got %v", lv.Level())
	}
}
