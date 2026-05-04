package adminapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/user/one-llm-router/internal/api/exportapi"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/store"
	"github.com/user/one-llm-router/internal/testsupport/appfixture"
)

func TestAdminMalformedProbe(t *testing.T) {
	fixture := appfixture.NewSteadyStateApp(t)
	handler := fixture.App.Handler()
	apiKeyAccount := fixture.SeedAPIKeyAccount(t, "probe-export-api-key")

	partLimit := int64(16 * 1024)
	envelopeLimit := int64(64 * 1024)

	importMalformedBody, importMalformedCT := multipartAuthJSONBody(t, []byte(`{"tokens":`))
	importEmptyBody, importEmptyCT := multipartEmptyBody(t)
	importPartTooLargeBody, importPartTooLargeCT := multipartAuthJSONBody(t, bytes.Repeat([]byte("a"), int(partLimit+1)))
	importPreambleFloodBody, importPreambleFloodCT := multipartPreambleFloodBody(t, int(envelopeLimit+8*1024))

	rows := testutil.AdminMalformedProbes(
		partLimit,
		envelopeLimit,
		importMalformedBody,
		importMalformedCT,
		importEmptyBody,
		importEmptyCT,
		importPartTooLargeBody,
		importPartTooLargeCT,
		importPreambleFloodBody,
		importPreambleFloodCT,
		apiKeyAccount.ID,
	)

	for _, row := range rows {
		row := row
		t.Run(row.Name, func(t *testing.T) {
			target := handler
			if row.UseFailingExportStore {
				mux := http.NewServeMux()
				exportapi.RegisterExportAuthJSONHandler(mux, exportapi.NewExportAuthJSONHandler(failingExportStore{}, fixture.Logger), nil)
				target = mux
			}
			testutil.RunMalformedProbe(t, target, row)
		})
	}

	if len(rows) < 8 {
		t.Fatalf("admin malformed probe matrix unexpectedly shrank: got %d rows, want >= 8", len(rows))
	}
}

func multipartAuthJSONBody(t *testing.T, payload []byte) ([]byte, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("auth_json", "auth.json")
	if err != nil {
		t.Fatalf("CreateFormFile(auth_json): %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("Write(auth_json): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func multipartEmptyBody(t *testing.T) ([]byte, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close empty multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func multipartPreambleFloodBody(t *testing.T, bytesBeforeBoundary int) ([]byte, string) {
	t.Helper()

	boundary := "preamble-flood-boundary"
	var body bytes.Buffer
	body.Write(bytes.Repeat([]byte("x"), bytesBeforeBoundary))
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString(`Content-Disposition: form-data; name="auth_json"; filename="auth.json"` + "\r\n")
	body.WriteString("Content-Type: application/json\r\n\r\n")
	body.WriteString("{}\r\n")
	body.WriteString("--" + boundary + "--\r\n")
	return body.Bytes(), "multipart/form-data; boundary=" + boundary
}

type failingExportStore struct{}

func (failingExportStore) GetForExport(context.Context, int64) (*store.ExportPayload, error) {
	return nil, sql.ErrConnDone
}

func (failingExportStore) GetProjectionByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}
