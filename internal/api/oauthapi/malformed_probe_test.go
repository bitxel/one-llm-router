package oauthapi_test

import (
	"testing"

	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/testsupport/appfixture"
)

func TestOAuthMalformedProbe(t *testing.T) {
	fixture := appfixture.NewSteadyStateApp(t)
	handler := fixture.App.Handler()

	limitJSON := int64(8 * 1024)
	rows := testutil.OAuthMalformedProbes(limitJSON)

	for _, row := range rows {
		row := row
		t.Run(row.Name, func(t *testing.T) {
			testutil.RunMalformedProbe(t, handler, row)
		})
	}

	if len(rows) < 16 {
		t.Fatalf("oauth malformed probe matrix unexpectedly shrank: got %d rows, want >= 16", len(rows))
	}
}
