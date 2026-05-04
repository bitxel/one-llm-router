package api

import (
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/provider"
	"github.com/user/one-llm-router/internal/provider/openai"
)

type bridgeSelectionFixture struct {
	method     string
	path       string
	websocket  bool
	credential openai.CredentialClass
}

var bridgeSelectionFixtures = []bridgeSelectionFixture{
	{method: http.MethodPost, path: "/v1/responses", credential: openai.CredentialClassAPIKey},
	{method: http.MethodPost, path: "/v1/responses", credential: openai.CredentialClassOAuth},
	{method: http.MethodGet, path: "/v1/responses", websocket: true, credential: openai.CredentialClassOAuth},
	{method: http.MethodPost, path: "/v1/chat/completions", credential: openai.CredentialClassAPIKey},
	{method: http.MethodPost, path: "/v1/chat/completions", credential: openai.CredentialClassOAuth},
	{method: http.MethodGet, path: "/v1/models", credential: openai.CredentialClassAPIKey},
	{method: http.MethodGet, path: "/v1/models", credential: openai.CredentialClassOAuth},
	{method: http.MethodPost, path: "/backend-api/codex/responses", credential: openai.CredentialClassOAuth},
	{method: http.MethodGet, path: "/backend-api/codex/responses", websocket: true, credential: openai.CredentialClassOAuth},
	{method: http.MethodPost, path: "/backend-api/transcribe", credential: openai.CredentialClassOAuth},
}

func TestBridgeSelectionOverhead(t *testing.T) {
	t.Parallel()

	const iterations = 1000
	baselineSamples := make([]time.Duration, 0, iterations*len(bridgeSelectionFixtures))
	samples := make([]time.Duration, 0, iterations*len(bridgeSelectionFixtures))
	for range iterations {
		for _, fixture := range bridgeSelectionFixtures {
			baselineStart := time.Now()
			baselineRoute := classifyGatewayRoute(fixture.method, fixture.path, fixture.websocket)
			baselineSamples = append(baselineSamples, time.Since(baselineStart))
			if baselineRoute.Kind != gatewayRouteKindSupported {
				t.Fatalf("route not classified for %s %s", fixture.method, fixture.path)
			}

			start := time.Now()
			route := classifyGatewayRoute(fixture.method, fixture.path, fixture.websocket)
			bridge, ok := selectGatewayBridge(route, fixture.credential)
			metadata := gatewayBridgeMetadata(bridge, fixture.credential)
			routerMetadata := provider.MergeBridgeRouterMetadata(nil, metadata)
			samples = append(samples, time.Since(start))
			if !ok {
				t.Fatalf("bridge not selected for %s %s credential=%s", fixture.method, fixture.path, fixture.credential)
			}
			if bridge == nil {
				t.Fatalf("nil bridge for %s %s credential=%s", fixture.method, fixture.path, fixture.credential)
			}
			if _, ok := routerMetadata[provider.RouterMetadataBridgeKey]; !ok {
				t.Fatalf("bridge metadata missing for %s %s credential=%s", fixture.method, fixture.path, fixture.credential)
			}
		}
	}

	baselineMedian := medianDuration(baselineSamples)
	median := medianDuration(samples)
	p95 := percentileDuration(samples, 95)
	t.Logf("bridge selection + metadata timing: samples=%d baseline_median=%s median=%s p95=%s delta_median=%s", len(samples), baselineMedian, median, p95, durationDelta(median, baselineMedian))
	if median > time.Millisecond {
		t.Fatalf("bridge selection + metadata median overhead %s exceeds 1ms budget", median)
	}
}

func BenchmarkBridgeSelection(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fixture := bridgeSelectionFixtures[i%len(bridgeSelectionFixtures)]
		route := classifyGatewayRoute(fixture.method, fixture.path, fixture.websocket)
		bridge, ok := selectGatewayBridge(route, fixture.credential)
		if !ok || bridge == nil {
			b.Fatalf("bridge not selected for %s %s credential=%s", fixture.method, fixture.path, fixture.credential)
		}
		metadata := gatewayBridgeMetadata(bridge, fixture.credential)
		routerMetadata := provider.MergeBridgeRouterMetadata(nil, metadata)
		if _, ok := routerMetadata[provider.RouterMetadataBridgeKey]; !ok {
			b.Fatalf("bridge metadata missing for %s %s credential=%s", fixture.method, fixture.path, fixture.credential)
		}
	}
}

func medianDuration(samples []time.Duration) time.Duration {
	return percentileDuration(samples, 50)
}

func percentileDuration(samples []time.Duration, percentile int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (len(sorted) * percentile) / 100
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func durationDelta(current, baseline time.Duration) string {
	if current >= baseline {
		return fmt.Sprintf("+%s", current-baseline)
	}
	return fmt.Sprintf("-%s", baseline-current)
}
