package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/exportapi"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/store"
	"github.com/user/one-llm-router/internal/testsupport/appfixture"
)

func TestEnvelopeParity(t *testing.T) {
	fixture := appfixture.NewSteadyStateApp(t)
	oauthAccount := fixture.SeedOAuthAccount(t, "exportable-oauth", domain.AuthMethodOAuthImport)
	apiKeyAccount := fixture.SeedAPIKeyAccount(t, "export-blocked-api-key")

	probes := testutil.EnvelopeParityProbes(oauthAccount.ID, apiKeyAccount.ID)

	covered := map[string]struct{}{}
	executedProbeNames := map[string]struct{}{}
	allowedRaw := 0
	for _, probe := range probes {
		probe := probe
		t.Run(probe.Name, func(t *testing.T) {
			executedProbeNames[probe.Name] = struct{}{}
			var recorder *httptest.ResponseRecorder
			if probe.UseFailingExportStore {
				mux := http.NewServeMux()
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				exportapi.RegisterExportAuthJSONHandler(mux, exportapi.NewExportAuthJSONHandler(failingExportStore{}, logger), nil)
				recorder = runParityProbe(t, mux, probe.Method, probe.Path, probe.Body, probe.Headers)
			} else {
				recorder = runParityProbe(t, fixture.App.Handler(), probe.Method, probe.Path, probe.Body, probe.Headers)
			}

			if recorder.Code != probe.ExpectedStatus {
				t.Fatalf("%s: status=%d want=%d body=%s", probe.OperationID, recorder.Code, probe.ExpectedStatus, recorder.Body.String())
			}
			if probe.AllowedRawReason != "" {
				if strings.HasPrefix(strings.TrimSpace(recorder.Body.String()), `{"code":`) {
					t.Fatalf("%s: expected allowed raw response, got envelope body=%s", probe.OperationID, recorder.Body.String())
				}
				covered[probe.OperationID] = struct{}{}
				allowedRaw++
				t.Logf("%s: skipped envelope assertion (%s)", probe.OperationID, probe.AllowedRawReason)
				return
			}

			if probe.ExpectedEnvelope {
				data := testutil.AssertEnvelope(t, recorder, probe.ExpectedCode)
				if ct := recorder.Header().Get("Content-Type"); ct != probe.ExpectedContentType {
					t.Fatalf("%s: Content-Type=%q want=%q", probe.OperationID, ct, probe.ExpectedContentType)
				}
				if data == nil {
					t.Fatalf("%s: envelope data map is nil", probe.OperationID)
				}
			} else {
				if ct := recorder.Header().Get("Content-Type"); ct != probe.ExpectedContentType {
					t.Fatalf("%s: Content-Type=%q want=%q", probe.OperationID, ct, probe.ExpectedContentType)
				}
				if got := recorder.Header().Get("Content-Disposition"); !strings.HasPrefix(got, probe.ExpectedDispositionStart) {
					t.Fatalf("%s: Content-Disposition=%q want prefix %q", probe.OperationID, got, probe.ExpectedDispositionStart)
				}
				if recorder.Header().Get("Cache-Control") == "" {
					t.Fatalf("%s: Cache-Control header missing on raw export success", probe.OperationID)
				}
				if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatalf("%s: X-Content-Type-Options=%q want nosniff", probe.OperationID, recorder.Header().Get("X-Content-Type-Options"))
				}
			}
			covered[probe.OperationID] = struct{}{}
		})
	}

	declared := testutil.DeclaredAdminOperationIDs(t)
	var missing []string
	for _, op := range declared {
		if _, ok := covered[op]; !ok {
			missing = append(missing, op)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("operation not covered by envelope parity probe table: %s", strings.Join(missing, ", "))
	}

	var missingProbeNames []string
	for _, name := range testutil.RequiredEnvelopeParityProbeNames() {
		if _, ok := executedProbeNames[name]; !ok {
			missingProbeNames = append(missingProbeNames, name)
		}
	}
	if len(missingProbeNames) > 0 {
		sort.Strings(missingProbeNames)
		t.Fatalf("required envelope parity probes missing from matrix: %s", strings.Join(missingProbeNames, ", "))
	}

	sort.Strings(declared)
	t.Logf("envelope parity coverage: declared=%d covered=%d allowed_raw=%d ops=%v", len(declared), len(covered), allowedRaw, declared)
}

func runParityProbe(t *testing.T, handler http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

type failingExportStore struct{}

func (failingExportStore) GetForExport(context.Context, int64) (*store.ExportPayload, error) {
	return nil, sql.ErrConnDone
}

func (failingExportStore) GetProjectionByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}
