package httpio

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
)

type probeDSN struct {
	Driver string `json:"driver"`
	DSN    string `json:"dsn"`
}

func newReq(body string, contentType string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/admin/test", bytes.NewBufferString(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func TestDecodeJSON_HappyPath(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := newReq(`{"driver":"sqlite","dsn":"file::memory:"}`, "application/json")

	out, ok := DecodeJSON[probeDSN](rec, r, "req-1", DefaultAdminBodyCap)
	if !ok {
		t.Fatalf("ok=false, want true. Body: %s", rec.Body.String())
	}
	if out.Driver != "sqlite" || out.DSN != "file::memory:" {
		t.Fatalf("decoded=%+v", out)
	}
	// On happy path the helper MUST NOT write anything; the handler
	// retains exclusive control of the success envelope.
	if rec.Body.Len() != 0 {
		t.Fatalf("helper wrote body on happy path: %s", rec.Body.String())
	}
}

func TestDecodeJSON_Oversized(t *testing.T) {
	t.Parallel()
	limit := int64(64)
	body := `{"driver":"sqlite","dsn":"` + strings.Repeat("x", int(limit)) + `"}` // > 64 bytes
	rec := httptest.NewRecorder()
	r := newReq(body, "application/json")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-2", limit)
	if ok {
		t.Fatal("ok=true on oversized body, want false")
	}
	data := testutil.AssertEnvelope(t, rec, errcode.RequestBodyTooLarge)
	if got := data["scope"]; got != "envelope" {
		t.Errorf("data.scope=%v, want %q", got, "envelope")
	}
	if got := data["limit_bytes"]; got != float64(limit) {
		t.Errorf("data.limit_bytes=%v, want %d", got, limit)
	}
}

func TestDecodeJSON_EmptyBody(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := newReq("", "application/json")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-3", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on empty body, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

func TestDecodeJSON_MalformedJSON(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := newReq(`{`, "application/json")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-4", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on malformed JSON, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

func TestDecodeJSON_WrongContentType(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	// Valid JSON body but wrong Content-Type — the check runs BEFORE
	// decode to block text/plain→JSON sneaking paths.
	r := newReq(`{"driver":"sqlite","dsn":"x"}`, "text/plain")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-5", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on wrong Content-Type, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

func TestDecodeJSON_MissingContentType(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	// Valid JSON body but NO Content-Type header at all — the contract
	// requires a positive application/json declaration, otherwise we
	// 2008 to prevent curl-without-headers from sneaking past the gate.
	r := newReq(`{"driver":"sqlite","dsn":"x"}`, "")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-5b", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on missing Content-Type, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

func TestDecodeJSON_TruncatedBody(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	// A body that starts well then is cut off mid-token (io.ErrUnexpectedEOF
	// inside json.Decoder).
	r := newReq(`{"driver":"sqli`, "application/json")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-6", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on truncated body, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

// TestDecodeJSON_CapZero — boundary: cap=0 rejects any non-empty body
// as 2009 (rather than silently dropping it on the floor).
func TestDecodeJSON_CapZero(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := newReq(`{"driver":"sqlite"}`, "application/json")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-7", 0)
	if ok {
		t.Fatal("ok=true with cap=0 + non-empty body, want false")
	}
	data := testutil.AssertEnvelope(t, rec, errcode.RequestBodyTooLarge)
	if got := data["scope"]; got != "envelope" {
		t.Errorf("data.scope=%v, want %q", got, "envelope")
	}
	if got := data["limit_bytes"]; got != float64(0) {
		t.Errorf("data.limit_bytes=%v, want 0", got)
	}
}

func TestDecodeJSON_TrailingBytes(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	// Two JSON objects in a row — the envelope contract is "one doc
	// per request", so the second object is a 2008 malformed_body.
	r := newReq(`{"driver":"sqlite","dsn":"x"}{"driver":"postgres"}`, "application/json")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-8", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on trailing bytes, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

func TestDecodeJSON_ContentTypeWithCharset(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := newReq(`{"driver":"sqlite","dsn":"x"}`, "application/json; charset=utf-8")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-9", DefaultAdminBodyCap)
	if !ok {
		t.Fatalf("ok=false, want true (Content-Type with charset). Body: %s", rec.Body.String())
	}
}

func TestWriteMalformedBody(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteMalformedBody(rec, "req-10")
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

// TestDecodeJSON_NegativeCapPanics proves the misuse guard: a negative
// maxBytes is silently no-op for http.MaxBytesReader in the stdlib,
// which would defeat the entire purpose of the cap. DecodeJSON panics
// so the bug surfaces at the test bench, not in production.
func TestDecodeJSON_NegativeCapPanics(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := newReq(`{"driver":"sqlite"}`, "application/json")

	defer func() {
		rv := recover()
		if rv == nil {
			t.Fatal("DecodeJSON did not panic on maxBytes<0; negative caps must be caller bugs")
		}
		msg, ok := rv.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T=%v", rv, rv)
		}
		if !strings.Contains(msg, "maxBytes must be >= 0") {
			t.Errorf("panic message=%q; want diagnostic mentioning 'maxBytes must be >= 0'", msg)
		}
	}()

	_, _ = DecodeJSON[probeDSN](rec, r, "req-neg", -1)
}

// TestDecodeJSON_ContentTypeCaseInsensitive — the HTTP spec says
// media types are case-insensitive. Verify the helper honours that
// via mime.ParseMediaType's normalisation.
func TestDecodeJSON_ContentTypeCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, ct := range []string{
		"APPLICATION/JSON",
		"Application/json",
		"Application/JSON; charset=UTF-8",
		"application/json; charset=utf-8",
		"application/json ; charset=utf-8",
		"  application/json  ",
	} {
		ct := ct
		t.Run(ct, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			r := newReq(`{"driver":"sqlite","dsn":"x"}`, ct)
			_, ok := DecodeJSON[probeDSN](rec, r, "req-ct", DefaultAdminBodyCap)
			if !ok {
				t.Fatalf("ok=false for Content-Type %q; should be accepted (case-insensitive match)", ct)
			}
		})
	}
}

// TestDecodeJSON_DuplicateContentTypeRejected — round-2 review
// finding: a request with TWO Content-Type headers is ambiguous.
// Go's Header.Get only returns the first value, so a `json` +
// `text/plain` double-header would previously pass the gate while
// a proxy or downstream library might read the second one. We
// reject any request whose `Content-Type` is not exactly one value.
func TestDecodeJSON_DuplicateContentTypeRejected(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/test", bytes.NewBufferString(`{"driver":"sqlite","dsn":"x"}`))
	r.Header.Add("Content-Type", "application/json")
	r.Header.Add("Content-Type", "text/plain")

	_, ok := DecodeJSON[probeDSN](rec, r, "req-dup-ct", DefaultAdminBodyCap)
	if ok {
		t.Fatal("ok=true on duplicate Content-Type header, want false")
	}
	testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
}

// TestDecodeJSON_ContentTypeLookalikesRejected — the external expert
// reviewer (round-1 Codex MCP) flagged that a HasPrefix-on-
// "application/json" check accepts application/jsonp,
// application/json-seq, application/json5, and friends — all distinct
// media types whose parsers DO NOT agree with encoding/json. Accepting
// them would let a client with a trick Content-Type bypass the
// single-decoder contract this package is designed to enforce. We use
// mime.ParseMediaType to require an EXACT bare type of
// "application/json" (parameters are still allowed). This test locks
// that behaviour.
func TestDecodeJSON_ContentTypeLookalikesRejected(t *testing.T) {
	t.Parallel()
	for _, ct := range []string{
		"application/jsonp",
		"application/json-seq",
		"application/json5",
		"application/jsonx",
		"application/json+xml",
		"text/json",
		"application/ld+json",
		// Malformed media-type strings — mime.ParseMediaType errors,
		// and the helper must 2008 on any such value too.
		"application/",
		"application",
		"/",
	} {
		ct := ct
		t.Run(ct, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			r := newReq(`{"driver":"sqlite","dsn":"x"}`, ct)
			_, ok := DecodeJSON[probeDSN](rec, r, "req-lookalike", DefaultAdminBodyCap)
			if ok {
				t.Fatalf("ok=true for Content-Type %q; lookalike must be rejected", ct)
			}
			testutil.AssertEnvelope(t, rec, errcode.MalformedBody)
		})
	}
}
