// Package setup hosts the HTTP handlers for the 002 setup-wizard
// surface: /api/setup/status, /api/setup/probe-dsn, /api/setup/commit.
//
// The handlers are thin shims over internal/setup business logic —
// they own request decoding, envelope serialization, HTTP status
// mapping, and a single structured log line per request; every other
// responsibility lives in internal/setup (validator, probe, commit).
//
// Why a dedicated package:
//   - Keeps the admin handlers (/api/admin/*) and setup handlers
//     (/api/setup/*) physically separated so their routing tables and
//     middleware choices do not drift.
//   - Makes it obvious in code review which endpoints run while the
//     gate is closed (this package) vs. open (internal/api/admin).
//
// The package has ZERO dependencies on golang-migrate or xorm. All DB
// interaction happens through the narrow setup.MigratorFactory /
// setup.AccountCreator interfaces that BuildApp wires in.
package setup

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/setup"
)

// MaxBodyBytes bounds the accepted JSON payload for every setup
// endpoint. 8 KiB matches the explicit cap spelled out in setup-api.md
// §Rate limiting and body caps. Real commit payloads are ~2 KiB; the
// cap gives some breathing room without letting a log-paste-sized
// request reach the validator. Overflow surfaces as envelope code
// 2009 request_body_too_large.
const MaxBodyBytes = 8 << 10 // 8 KiB

// Handler bundles the three setup-wizard endpoints. It is stateless —
// every dependency is injected at construction — so one Handler
// instance safely serves every goroutine in the process.
//
// All dependency fields are non-optional. BuildApp constructs with
// concrete adapters; tests pass fakes.
type Handler struct {
	gate     setupGate
	cfgPath  string
	factory  setup.MigratorFactory
	creator  setup.AccountCreator
	reloader setup.PostCommitReloader
	logger   *slog.Logger
}

// setupGate is the narrow interface Handler needs to read gate state
// for the /api/setup/status endpoint. *setup.Gate satisfies this;
// tests pass a fake.
type setupGate interface {
	ProbeState() (open bool, probeErr error)
}

// NewHandler constructs a Handler. logger MUST be non-nil —
// /api/setup/commit logs one structured audit line per call so it
// cannot fall back to slog.Default().
func NewHandler(
	gate setupGate,
	cfgPath string,
	factory setup.MigratorFactory,
	creator setup.AccountCreator,
	reloader setup.PostCommitReloader,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		gate:     gate,
		cfgPath:  cfgPath,
		factory:  factory,
		creator:  creator,
		reloader: reloader,
		logger:   logger,
	}
}

// RegisterRoutes wires Handler's endpoints into mux using the Go
// stdlib pattern syntax (method + path). Callers are responsible for
// wrapping mux with the gate middleware; this function does NOT.
func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("GET /api/setup/status", h.Status)
	mux.HandleFunc("POST /api/setup/probe-dsn", h.ProbeDSN)
	mux.HandleFunc("POST /api/setup/commit", h.Commit)
}

// Status returns the current wizard state.
//
// Response body (setup-api.md §GET /api/setup/status):
//
//	{
//	  "code": 0,
//	  "msg": "ok",
//	  "data": {
//	    "state": "pending" | "done" | "error",
//	    "supported_drivers": ["sqlite3","postgres","mysql"],
//	    "error": string  // only when state="error"
//	  }
//	}
//
// The endpoint is INTENTIONALLY served under both setup-pending and
// setup-done states — the SPA shell polls it on every route change
// to decide between wizard and portal.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	open, probeErr := h.gate.ProbeState()

	switch {
	case probeErr != nil:
		// ProbeState failure means the setup gate cannot read the
		// config path (EACCES, config path is a directory, etc.).
		// Per setup-api.md §GET /api/setup/status, inability to
		// determine setup state is a *system* error — not a business
		// state — so we surface HTTP 500 + a registered system code
		// rather than inventing a third `state:"error"` value that
		// clients would have to special-case. The client can retry
		// or surface the message to the operator.
		h.logger.Error("setup status probe failed",
			"request_id", reqID, "error", probeErr)
		api.WriteSysErr(w, reqID, errcode.InternalError,
			errcode.Symbol(errcode.InternalError))
	case open:
		api.WriteOK(w, reqID, map[string]any{"state": "done"})
	default:
		api.WriteOK(w, reqID, map[string]any{
			"state":             "pending",
			"supported_drivers": setup.SupportedDrivers,
			"defaults": map[string]any{
				"db": map[string]any{
					"driver": "sqlite3",
					"url":    "router.db",
				},
				"upstream_provider": "openai",
			},
		})
	}
}

// probeRequest is the decoded shape of POST /api/setup/probe-dsn.
// The payload nests db under a top-level key (setup-api.md §POST
// /api/setup/probe-dsn) so the shape matches /api/setup/commit's
// `db` sub-object exactly. Clients can therefore build one DB block
// and post it to both endpoints without reshaping.
type probeRequest struct {
	DB DBRequestBlockDTO `json:"db"`
}

// DBRequestBlockDTO is the wire form for {driver, url}. Separate from
// setup.DBRequestBlock purely to keep HTTP-layer decoding independent
// from the setup business-logic struct (so schema drift between the
// two surfaces is caught at the handler boundary rather than silently
// round-tripping through shared types).
type DBRequestBlockDTO struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// ProbeDSN runs a single-shot DB connectivity check WITHOUT writing
// config.json and WITHOUT running migrations. Used by the wizard's
// "Step 1: Connect to your database" button.
//
// Success body:
//
//	data: { "ok": true, "latency_ms": 3, "server_version": "3.43.2" }
//
// Failure paths map to (all HTTP 200):
//
//	2002 invalid_driver        — driver not in SupportedDrivers
//	2003 invalid_dsn           — URL empty / too long OR probe failed
//	2008 malformed_body        — JSON decode error
//	2009 request_body_too_large — body exceeds MaxBodyBytes
//
// setup-api.md §2.0 — network failure is a *business* error (code
// 2003) not a system error. `{ok:false, latency_ms, hint}` carries
// driver-specific remediation in the data block.
func (h *Handler) ProbeDSN(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())

	// Short-circuit if setup is already done. Mirrors Commit's guard
	// (handler.go §Commit) so the wizard UI gets a dedicated 2001
	// setup_already_done envelope instead of silently probing an
	// arbitrary DSN post-setup — probing in that state is a no-op
	// from the router's perspective and risks exposing operator
	// credentials supplied by a second tab that never heard about
	// the completed commit. Body parsing is intentionally skipped
	// so an attacker cannot consume MaxBodyBytes worth of work just
	// to trigger this refusal.
	if open, _ := h.gate.ProbeState(); open {
		api.WriteBizErr(w, reqID, errcode.SetupAlreadyDone,
			errcode.Symbol(errcode.SetupAlreadyDone), map[string]any{})
		return
	}

	var req probeRequest
	if failure := decodeJSON(r, &req, nil); failure != nil {
		h.writeFailure(w, reqID, failure, "probe_dsn")
		return
	}

	v := setup.NewValidator()
	if err := v.DriverEnum(req.DB.Driver); err != nil {
		api.WriteBizErr(w, reqID, err.Code, errcode.Symbol(err.Code),
			map[string]any{"field": err.Field, "detail": err.Msg})
		return
	}
	if err := v.DSN(req.DB.URL); err != nil {
		api.WriteBizErr(w, reqID, err.Code, errcode.Symbol(err.Code),
			map[string]any{"field": err.Field, "detail": err.Msg})
		return
	}

	latency, version, probeErr := setup.ProbeDSN(r.Context(), req.DB.Driver, req.DB.URL)
	if probeErr != nil {
		h.logger.Info("probe_dsn failed",
			"request_id", reqID,
			"driver", req.DB.Driver,
			"latency_ms", latency,
			"error", probeErr,
		)
		api.WriteBizErr(w, reqID, errcode.InvalidDSN,
			errcode.Symbol(errcode.InvalidDSN),
			map[string]any{
				"ok":         false,
				"latency_ms": latency,
				"hint": map[string]any{
					"driver":     req.DB.Driver,
					"timeout_ms": setup.ProbeTimeout.Milliseconds(),
					"message":    probeErr.Error(),
				},
			})
		return
	}

	h.logger.Info("probe_dsn ok",
		"request_id", reqID,
		"driver", req.DB.Driver,
		"latency_ms", latency,
	)
	api.WriteOK(w, reqID, map[string]any{
		"ok":             true,
		"latency_ms":     latency,
		"server_version": version,
	})
}

// Commit is the terminal wizard step. It runs the full
// validate → probe → migrate → insert → write → reload sequence
// (internal/setup.Commit) and returns the resulting config version +
// seeded account id.
//
// After a successful commit the setup gate latches open on the next
// request (since WriteAtomic puts config.json on disk and the gate's
// ProbeState re-stats before latching).
func (h *Handler) Commit(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())

	// Short-circuit if setup is already done. We could rely on the
	// commit flow's validator chain catching it, but emitting the
	// dedicated 2001 code earlier gives the wizard a cleaner UI.
	if open, _ := h.gate.ProbeState(); open {
		api.WriteBizErr(w, reqID, errcode.SetupAlreadyDone,
			errcode.Symbol(errcode.SetupAlreadyDone), map[string]any{})
		return
	}

	raw := map[string]any{}
	var req setup.CommitRequest
	if failure := decodeJSON(r, &req, raw); failure != nil {
		h.writeFailure(w, reqID, failure, "commit")
		return
	}
	req.RawTop = raw
	if m, ok := raw["runtime"].(map[string]any); ok {
		req.RawRuntime = m
	}
	if m, ok := raw["plugins"].(map[string]any); ok {
		req.RawPlugins = m
		// Validate plugin block structure:
		//   (a) every plugin id must be known to 002 (admin_auth, client_keys);
		//   (b) every known plugin MUST carry an `enabled` boolean.
		//
		// The typed CommitRequest has a PluginsRequestBlock so missing
		// `enabled` fields silently default to Go's zero-value (false),
		// which would accept `{"plugins":{"admin_auth":{}}}` as valid
		// and record disabled intent — contradicting setup-api.md
		// §Validation which requires explicit operator intent.
		// Inspecting the raw map here enforces presence + type.
		if failure := validatePluginBlock(m); failure != nil {
			api.WriteBizErr(w, reqID, failure.code, failure.msg,
				map[string]any{"field": failure.field, "detail": failure.detail})
			return
		}
	} else {
		// Missing `plugins` entirely. The contract requires both
		// plugin ids to be present with explicit operator intent, so
		// we surface 2008 malformed_body (matches the "partial-runtime
		// → 2008" pattern).
		api.WriteBizErr(w, reqID, errcode.MalformedBody,
			errcode.Symbol(errcode.MalformedBody),
			map[string]any{
				"field":  "plugins",
				"detail": "plugins block is required; both admin_auth.enabled and client_keys.enabled must be present",
			})
		return
	}

	result, failure := setup.Commit(
		r.Context(), &req, h.cfgPath,
		h.factory, h.creator, h.reloader, h.logger,
	)
	if failure != nil {
		h.writeCommitFailure(w, reqID, failure)
		return
	}

	// Exactly one `setup_committed` INFO event per contract.
	// account_seeded distinguishes a wizard that skipped upstream
	// account seeding (account_id=0) from one that registered a key.
	h.logger.Info("setup_committed",
		"request_id", reqID,
		"driver", req.DB.Driver,
		"account_id", result.AccountID,
		"account_name", req.FirstAccount.Name,
		"account_seeded", !req.FirstAccount.IsEmpty(),
		"api_key_fp", result.APIKeyFP,
		"admin_auth_enabled", req.Plugins.AdminAuth.Enabled,
		"client_keys_enabled", req.Plugins.ClientKeys.Enabled,
	)
	body := map[string]any{
		"redirect":       "/admin/",
		"config_version": result.ConfigVersion,
	}
	if result.AccountID > 0 {
		body["account_id"] = result.AccountID
	}
	api.WriteOK(w, reqID, body)
}

// decodeFailure is the internal signal shape used by decodeJSON to
// communicate "how did the decode fail" back to the handler. Kept
// unexported because only this file maps it onto envelope codes.
type decodeFailure struct {
	code   int
	msg    string
	detail string
}

// decodeJSON reads r.Body (capped at MaxBodyBytes), decodes into v,
// and optionally populates raw with the original key→value map so
// callers can detect unknown-key cases that typed structs silently
// discard. Returns nil on success.
//
// The function is shared between /probe-dsn and /commit so the
// MaxBodyBytes / DisallowUnknownFields policies stay aligned.
func decodeJSON(r *http.Request, v any, raw map[string]any) *decodeFailure {
	body := http.MaxBytesReader(nil, r.Body, MaxBodyBytes)
	defer body.Close() //nolint:errcheck

	data, err := io.ReadAll(body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &decodeFailure{
				code:   errcode.RequestBodyTooLarge,
				msg:    errcode.Symbol(errcode.RequestBodyTooLarge),
				detail: "request body exceeds 8 KiB",
			}
		}
		return &decodeFailure{
			code:   errcode.MalformedBody,
			msg:    errcode.Symbol(errcode.MalformedBody),
			detail: err.Error(),
		}
	}

	if raw != nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return &decodeFailure{
				code:   errcode.MalformedBody,
				msg:    errcode.Symbol(errcode.MalformedBody),
				detail: err.Error(),
			}
		}
	}
	if err := json.Unmarshal(data, v); err != nil {
		return &decodeFailure{
			code:   errcode.MalformedBody,
			msg:    errcode.Symbol(errcode.MalformedBody),
			detail: err.Error(),
		}
	}
	return nil
}

// pluginValidationFailure carries the per-field diagnostic emitted by
// validatePluginBlock so the handler can shape the envelope uniformly.
type pluginValidationFailure struct {
	code   int
	msg    string
	field  string
	detail string
}

// supportedPluginIDs is the allow-list of plugin identifiers the 002
// commit path understands. Adding a new plugin requires (a) a matching
// config.PluginsConfig field AND (b) a validator branch below. Both
// sites are intentionally kept in sync via this shared list so a
// schema drift between the wire contract and the config marshaller
// surfaces at code review.
var supportedPluginIDs = []string{"admin_auth", "client_keys"}

// validatePluginBlock enforces the presence + type contract for the
// wizard commit's `plugins` block. See setup-api.md §POST /api/setup/
// commit and data-model.md §Validation Rules for the full spec.
//
// The function returns nil on success; a *pluginValidationFailure
// on the first offending field. It is called with the decoded raw
// map (`raw["plugins"].(map[string]any)`), never with the typed
// PluginsRequestBlock — the typed shape silently zero-fills absent
// booleans and therefore cannot be used to detect missing keys.
func validatePluginBlock(m map[string]any) *pluginValidationFailure {
	// Reject unknown plugin ids first so the operator sees the most
	// actionable diagnostic ("you typed admin_ath").
	known := make(map[string]bool, len(supportedPluginIDs))
	for _, id := range supportedPluginIDs {
		known[id] = true
	}
	for id := range m {
		if !known[id] {
			return &pluginValidationFailure{
				code:   errcode.InvalidPluginFlag,
				msg:    errcode.Symbol(errcode.InvalidPluginFlag),
				field:  "plugins." + id,
				detail: "unknown plugin id; supported: admin_auth, client_keys",
			}
		}
	}
	// Every supported plugin must be present with an explicit
	// {enabled: bool} body.
	for _, id := range supportedPluginIDs {
		raw, ok := m[id]
		if !ok {
			return &pluginValidationFailure{
				code:   errcode.InvalidPluginFlag,
				msg:    errcode.Symbol(errcode.InvalidPluginFlag),
				field:  "plugins." + id,
				detail: "plugin is missing; supply {\"enabled\": <bool>}",
			}
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			return &pluginValidationFailure{
				code:   errcode.InvalidPluginFlag,
				msg:    errcode.Symbol(errcode.InvalidPluginFlag),
				field:  "plugins." + id,
				detail: "plugin must be an object {\"enabled\": <bool>}",
			}
		}
		enabled, ok := obj["enabled"]
		if !ok {
			return &pluginValidationFailure{
				code:   errcode.InvalidPluginFlag,
				msg:    errcode.Symbol(errcode.InvalidPluginFlag),
				field:  "plugins." + id + ".enabled",
				detail: "`enabled` flag is required",
			}
		}
		if _, ok := enabled.(bool); !ok {
			return &pluginValidationFailure{
				code:   errcode.InvalidPluginFlag,
				msg:    errcode.Symbol(errcode.InvalidPluginFlag),
				field:  "plugins." + id + ".enabled",
				detail: "`enabled` must be a boolean",
			}
		}
	}
	return nil
}

// writeFailure surfaces a decodeFailure on the envelope. Separate
// helper because both probe and commit want identical behaviour.
func (h *Handler) writeFailure(w http.ResponseWriter, reqID string, f *decodeFailure, endpoint string) {
	h.logger.Info("setup decode failure",
		"request_id", reqID,
		"endpoint", endpoint,
		"code", f.code,
	)
	api.WriteBizErr(w, reqID, f.code, f.msg, map[string]any{"detail": f.detail})
}

// writeCommitFailure maps a setup.CommitFailure onto the envelope.
// SysErr → HTTP 500, else HTTP 200 + business code.
func (h *Handler) writeCommitFailure(w http.ResponseWriter, reqID string, cf *setup.CommitFailure) {
	data := map[string]any{}
	if cf.Hint != "" {
		data["hint"] = cf.Hint
	}
	h.logger.Info("setup commit failed",
		"request_id", reqID,
		"code", cf.Code,
		"error", cf.Err,
	)
	if cf.SysErr {
		api.WriteSysErr(w, reqID, cf.Code, errcode.Symbol(cf.Code))
		return
	}
	api.WriteBizErr(w, reqID, cf.Code, errcode.Symbol(cf.Code), data)
}
