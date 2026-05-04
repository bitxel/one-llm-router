package setup

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// T-026: classifyProbeError deadline branch (wizard timeout copy).
func TestClassifyProbeError_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := classifyProbeError(ctx, context.DeadlineExceeded)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), ProbeTimeout.String()) {
		t.Fatalf("expected timeout hint (%s) in error, got: %v", ProbeTimeout.String(), err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrap of context.DeadlineExceeded, got: %v", err)
	}
}

// T-026: classifyProbeError cancellation branch.
func TestClassifyProbeError_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := classifyProbeError(ctx, context.Canceled)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrap of context.Canceled, got: %v", err)
	}
}

// T-026: generic probe error path (no deadline/cancel sentinel).
func TestClassifyProbeError_Generic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	under := errors.New("boom")
	err := classifyProbeError(ctx, under)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.HasPrefix(err.Error(), "probe:") {
		t.Fatalf("expected probe: prefix, got: %v", err)
	}
}

// T-026 / data-model: queryServerVersion returns "" for unknown driver.
func TestQueryServerVersion_UnknownDriver(t *testing.T) {
	t.Parallel()
	if got := queryServerVersion(context.Background(), nil, "oracle"); got != "" {
		t.Fatalf("version = %q, want empty", got)
	}
}
