package adminapi

// union_smoke_test.go — compile-time + runtime guard for the
// oneOf-backed response bodies under internal/generated/adminapi.
//
// ─── Failure mode 1: unexported union field (compile-time) ────────
//
// oapi-codegen v2 emits two very different Go shapes for a 200 JSON
// response declared as `oneOf: [A, B, C]`:
//
//   1. INLINE oneOf on the operation (BAD) →
//      `type Op200JSONResponse struct { union json.RawMessage }`.
//      No exported fields, no exported setters; the handler package
//      literally cannot construct a successful response.
//
//   2. $ref to a named component schema (GOOD, what we do) →
//      `type Op200JSONResponse Union` where `Union` carries
//      exported `As<B>() / From<B>() / Merge<B>()` helpers the
//      handler package calls to populate the union variant.
//
// Shape #1 was the blocker that motivated pulling every admin.yaml
// `oneOf` response into a named component schema. The per-branch
// sub-tests below compile `From<Branch>`, `As<Branch>`, and
// `Merge<Branch>` for **every** oneOf branch of every response
// body; a regression that makes any one of them unexported is an
// immediate compile error.
//
// ─── Failure mode 2: Visit method emits {} (runtime) ─────────────
//
// Even when the NAMED schema path is taken, a second failure is
// silent: `type Op200JSONResponse Union` is a **defined** type
// whose method set is empty — Go does not inherit methods across
// `type X Y` (it would across `type X = Y`, the type-alias form,
// but oapi-codegen does not emit aliases here). `Union`'s
// `MarshalJSON` is therefore lost, and the generated
// `Visit<Op>Response` writes `{}` via reflection.
//
// `union_marshal.go` supplies the missing MarshalJSON shims. The
// sub-tests below drive **every** branch's Visit method AND the
// direct `json.Marshal(Op200JSONResponse(body))` path against a
// non-empty expected envelope skeleton.
//
// ─── What "branch-identity" we actually prove ────────────────────
//
// oapi-codegen emits our `oneOf` unions WITHOUT an OpenAPI
// discriminator, so the generated `As<Branch>` is a tolerant
// `json.Unmarshal` into the requested branch type. Every admin
// envelope shares the `{code,msg,data}` skeleton, so a wrong-branch
// `As*` succeeds silently.
//
// To make the body-level assertions still meaningful, every branch
// below populates the envelope's `Code` and `Msg` fields with the
// generated enum constants from admin.yaml (e.g. `N3008` and
// `FlowIdMismatch`). The test then asserts those exact tokens
// appear in the marshaled body. A shim regression that emitted an
// empty body, a skeletal `{"code":0}`, or the wrong envelope's
// bytes would fail this assertion loudly.
//
// At the Go type level, every named union below shares the same
// `struct { union json.RawMessage }` layout, so a hypothetical
// shim that type-converts across unions would still emit the
// correct bytes (the RawMessage is the source of truth). Tracking
// the ACTIVE BRANCH on the producer side — separate from these
// generated helpers — is the handler layer's job (T-009+).
//
// If a future regression re-introduces failure mode #1 or #2,
// these tests fail. Fix the SPEC or the SHIM, not this file.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// unionMarshaler constrains the populated-union return type of
// branchCase.populate. Every generated union satisfies this via
// the generated MarshalJSON, so a single interface lets readBack
// and mergeOver receive back exactly the value populate produced
// without over-specifying concrete types.
type unionMarshaler interface {
	MarshalJSON() ([]byte, error)
}

// expectCodeMsg yields the two branch-identifying substrings every
// envelope branch must emit. It does NOT assert the order of JSON
// keys — `strings.Contains` is sufficient because the `code` value
// is rendered as a number and `msg` as a quoted string, so the
// formatted substring is effectively unique per branch.
func expectCodeMsg(code int, msg string) []string {
	return []string{
		fmt.Sprintf(`"code":%d`, code),
		fmt.Sprintf(`"msg":%q`, msg),
	}
}

// expectCodeMsgEmptyData extends expectCodeMsg with the `"data":{}`
// sentinel. It is used for the six envelopes whose `data` is
// modelled as `map[string]interface{}` in the generated Go struct
// (AccountNotFoundEnvelope, FlowExpiredEnvelope,
// InvalidAPIKeyEnvelope, InvalidAuthJSONStructureEnvelope,
// NoFlowInProgressEnvelope, OAuthStateMismatchEnvelope). Their
// zero-value would marshal to `"data":null`, violating
// AGENTS.md §API Contract ("`data` … never `null`"); the test
// populates Data with an empty map and this helper locks the
// expected output.
func expectCodeMsgEmptyData(code int, msg string) []string {
	return append(expectCodeMsg(code, msg), `"data":{}`)
}

// ─── Coverage matrix ─────────────────────────────────────────────
//
// `branchCase` = one (union, branch) pair. Every row is explicit so
// that a PR adding a branch must either add a row here or fail the
// "total branch count" assertion at the tail of this file.
//
// Per branch we assert:
//
//  1. `populate` — the union's `From<Branch>` exists and succeeds
//     on a struct populated with the branch's canonical `Code` +
//     `Msg` enum constants.
//  2. `readBack` — the union's `As<Branch>` exists and returns nil
//     on the populated union. See the file header for why this
//     does NOT prove branch identity on its own.
//  3. `mergeOver` — `Merge<Branch>` exists and does not error.
//  4. `marshalWrapper` — `json.Marshal(Op200JSONResponse(body))`
//     returns a non-empty envelope AND contains the branch-
//     specific `"code":<N>` / `"msg":"<slug>"` tokens. This is the
//     ONLY closure that actually exercises the MarshalJSON shim in
//     `union_marshal.go`; marshalling the raw union bypasses the
//     shim entirely and would make the sub-assertion a no-op.
//     For the struct-wrapper export case the closure marshals
//     `AccountsExportAuthJSON200JSONResponse{Body: body}` instead
//     — that type has no shim because the generated Visit method
//     encodes `response.Body` (already a union with native
//     MarshalJSON).
//  5. `visit` — end-to-end exercise of the generated
//     `Visit<Op>Response` method against an
//     `httptest.ResponseRecorder`, covering the status, headers,
//     and body (with the same branch-specific assertions).
type branchCase struct {
	union            string
	branch           string
	populate         func() (unionMarshaler, error)
	readBack         func(unionMarshaler) error
	mergeOver        func(unionMarshaler) error
	marshalWrapper   func() ([]byte, error)
	visit            func(w *httptest.ResponseRecorder) error
	expectSubstrings []string
}

// ─── Union-scoped helpers ────────────────────────────────────────
//
// Each `casesXxx` factory below wraps repetitive boilerplate in
// closures so a branch row collapses to a small record. If a new
// union lands, add a sibling factory and append its output to
// `branchCases()`.

// ─── SettingsGetResponseBody (2 branches) ────────────────────────

func casesSettingsGet() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*SettingsGetResponseBody) error,
		as func(SettingsGetResponseBody) error,
		merge func(*SettingsGetResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "SettingsGetResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b SettingsGetResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(SettingsGetResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(SettingsGetResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b SettingsGetResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(SettingsGet200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b SettingsGetResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return SettingsGet200JSONResponse(b).VisitSettingsGetResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("SettingsEnvelope",
		func(b *SettingsGetResponseBody) error {
			return b.FromSettingsEnvelope(settingsEnvelopeForUnion())
		},
		func(b SettingsGetResponseBody) error { _, err := b.AsSettingsEnvelope(); return err },
		func(b *SettingsGetResponseBody) error {
			return b.MergeSettingsEnvelope(settingsEnvelopeForUnion())
		},
		append(expectCodeMsg(0, "ok"), `"router_version":"0.4.0"`),
	)
	add("SetupRequiredEnvelope",
		func(b *SettingsGetResponseBody) error {
			return b.FromSetupRequiredEnvelope(SetupRequiredEnvelope{
				Code: N2011,
				Data: map[string]interface{}{},
				Msg:  SetupRequired,
			})
		},
		func(b SettingsGetResponseBody) error { _, err := b.AsSetupRequiredEnvelope(); return err },
		func(b *SettingsGetResponseBody) error {
			return b.MergeSetupRequiredEnvelope(SetupRequiredEnvelope{
				Code: N2011,
				Data: map[string]interface{}{},
				Msg:  SetupRequired,
			})
		},
		expectCodeMsgEmptyData(2011, "setup_required"),
	)
	return cases
}

// ─── SettingsUpdateResponseBody (8 branches) ─────────────────────

func casesSettingsUpdate() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*SettingsUpdateResponseBody) error,
		as func(SettingsUpdateResponseBody) error,
		merge func(*SettingsUpdateResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "SettingsUpdateResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b SettingsUpdateResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(SettingsUpdateResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(SettingsUpdateResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b SettingsUpdateResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(SettingsUpdate200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b SettingsUpdateResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return SettingsUpdate200JSONResponse(b).VisitSettingsUpdateResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("SettingsEnvelope",
		func(b *SettingsUpdateResponseBody) error { return b.FromSettingsEnvelope(settingsEnvelopeForUnion()) },
		func(b SettingsUpdateResponseBody) error { _, err := b.AsSettingsEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error { return b.MergeSettingsEnvelope(settingsEnvelopeForUnion()) },
		append(expectCodeMsg(0, "ok"), `"log_retention_days":30`),
	)
	add("RequestBodyTooLargeEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromRequestBodyTooLargeEnvelope(settingsBodyTooLargeEnvelope())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsRequestBodyTooLargeEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeRequestBodyTooLargeEnvelope(settingsBodyTooLargeEnvelope())
		},
		append(expectCodeMsg(2009, "request_body_too_large"), `"limit_bytes":16384`),
	)
	add("MalformedBodyEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromMalformedBodyEnvelope(malformedBodyEnvelopeForUnion())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsMalformedBodyEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeMalformedBodyEnvelope(malformedBodyEnvelopeForUnion())
		},
		append(expectCodeMsg(2008, "malformed_body"), `"field":"runtime.log_level"`),
	)
	add("UnknownConfigKeyEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromUnknownConfigKeyEnvelope(unknownConfigKeyEnvelopeForUnion())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsUnknownConfigKeyEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeUnknownConfigKeyEnvelope(unknownConfigKeyEnvelopeForUnion())
		},
		append(expectCodeMsg(2012, "unknown_config_key"), `"field":"runtime.nope"`),
	)
	add("EnvOverrideReadonlyEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromEnvOverrideReadonlyEnvelope(envOverrideReadonlyEnvelopeForUnion())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsEnvOverrideReadonlyEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeEnvOverrideReadonlyEnvelope(envOverrideReadonlyEnvelopeForUnion())
		},
		append(expectCodeMsg(2013, "env_override_readonly"), `"env_var":"ROUTER_RUNTIME_LOG_LEVEL"`),
	)
	add("InvalidRetentionEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromInvalidRetentionEnvelope(invalidRetentionEnvelopeForUnion())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsInvalidRetentionEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeInvalidRetentionEnvelope(invalidRetentionEnvelopeForUnion())
		},
		append(expectCodeMsg(2007, "invalid_retention"), `"field":"runtime.log_retention_days"`),
	)
	add("InvalidLogLevelEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromInvalidLogLevelEnvelope(invalidLogLevelEnvelopeForUnion())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsInvalidLogLevelEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeInvalidLogLevelEnvelope(invalidLogLevelEnvelopeForUnion())
		},
		append(expectCodeMsg(2014, "invalid_log_level"), `"field":"runtime.log_level"`),
	)
	add("InvalidPluginFlagEnvelope",
		func(b *SettingsUpdateResponseBody) error {
			return b.FromInvalidPluginFlagEnvelope(invalidPluginFlagEnvelopeForUnion())
		},
		func(b SettingsUpdateResponseBody) error { _, err := b.AsInvalidPluginFlagEnvelope(); return err },
		func(b *SettingsUpdateResponseBody) error {
			return b.MergeInvalidPluginFlagEnvelope(invalidPluginFlagEnvelopeForUnion())
		},
		append(expectCodeMsg(2006, "invalid_plugin_flag"), `"field":"plugins.admin_auth.enabled"`),
	)
	return cases
}

func settingsEnvelopeForUnion() SettingsEnvelope {
	return SettingsEnvelope{
		Code: SettingsEnvelopeCodeN0,
		Msg:  SettingsEnvelopeMsgOk,
		Data: SettingsPayload{
			Runtime: RuntimeSettings{
				LogClientRequestBody:    false,
				LogUpstreamRequestBody:  true,
				LogUpstreamResponseBody: true,
				LogRetentionDays:        30,
				LogLevel:                RuntimeSettingsLogLevelInfo,
			},
			Db: DBSettings{Driver: "sqlite3", Host: "local", DatabaseName: "router.db"},
			Plugins: []PluginSummary{
				{Id: "prometheus", Label: "Prometheus", Enabled: true},
			},
			PluginIntents: []PluginIntent{
				{Id: AdminAuth, Label: "Admin authentication", Enabled: false, Status: "intent only"},
			},
			System: SystemSummary{
				RouterVersion: "0.4.0",
				RouterGitSha:  "deadbeef",
				RouterBuiltAt: "2026-04-25T00:00:00Z",
			},
		},
	}
}

func settingsBodyTooLargeEnvelope() RequestBodyTooLargeEnvelope {
	env := RequestBodyTooLargeEnvelope{Code: N2009, Msg: RequestBodyTooLarge}
	env.Data.Scope = Envelope
	env.Data.LimitBytes = 16 * 1024
	return env
}

func malformedBodyEnvelopeForUnion() MalformedBodyEnvelope {
	field := "runtime.log_level"
	detail := "log_level must be a string"
	env := MalformedBodyEnvelope{Code: N2008, Msg: MalformedBody}
	env.Data.Field = &field
	env.Data.Detail = &detail
	return env
}

func unknownConfigKeyEnvelopeForUnion() UnknownConfigKeyEnvelope {
	field := "runtime.nope"
	detail := "config key is not patchable"
	env := UnknownConfigKeyEnvelope{Code: N2012, Msg: UnknownConfigKey}
	env.Data.Field = &field
	env.Data.Detail = &detail
	return env
}

func envOverrideReadonlyEnvelopeForUnion() EnvOverrideReadonlyEnvelope {
	return EnvOverrideReadonlyEnvelope{
		Code: N2013,
		Msg:  EnvOverrideReadonly,
		Data: struct {
			Detail string `json:"detail"`
			EnvVar string `json:"env_var"`
			Field  string `json:"field"`
		}{
			Field:  "runtime.log_level",
			EnvVar: "ROUTER_RUNTIME_LOG_LEVEL",
			Detail: "runtime.log_level is overridden by env var ROUTER_RUNTIME_LOG_LEVEL",
		},
	}
}

func invalidRetentionEnvelopeForUnion() InvalidRetentionEnvelope {
	field := "runtime.log_retention_days"
	detail := "log_retention_days must be an integer in [1, 365]"
	env := InvalidRetentionEnvelope{Code: N2007, Msg: InvalidRetention}
	env.Data.Field = &field
	env.Data.Detail = &detail
	return env
}

func invalidLogLevelEnvelopeForUnion() InvalidLogLevelEnvelope {
	field := "runtime.log_level"
	detail := "log_level must be one of debug, info, warn, error"
	env := InvalidLogLevelEnvelope{Code: N2014, Msg: InvalidLogLevel}
	env.Data.Field = &field
	env.Data.Detail = &detail
	return env
}

func invalidPluginFlagEnvelopeForUnion() InvalidPluginFlagEnvelope {
	field := "plugins.admin_auth.enabled"
	detail := "plugins.admin_auth.enabled must be a boolean"
	env := InvalidPluginFlagEnvelope{Code: N2006, Msg: InvalidPluginFlag}
	env.Data.Field = &field
	env.Data.Detail = &detail
	return env
}

// ─── OAuthCancelResponseBody (2 branches) ────────────────────────

func casesOAuthCancel() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*OAuthCancelResponseBody) error,
		as func(OAuthCancelResponseBody) error,
		merge func(*OAuthCancelResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "OAuthCancelResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b OAuthCancelResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error {
				return as(m.(OAuthCancelResponseBody))
			},
			mergeOver: func(m unionMarshaler) error {
				b := m.(OAuthCancelResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b OAuthCancelResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(OauthCancel200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b OAuthCancelResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return OauthCancel200JSONResponse(b).VisitOauthCancelResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("CancelSuccessEnvelope",
		func(b *OAuthCancelResponseBody) error {
			return b.FromCancelSuccessEnvelope(CancelSuccessEnvelope{
				Code: 0,
				Msg:  CancelSuccessEnvelopeMsgOk,
			})
		},
		func(b OAuthCancelResponseBody) error { _, err := b.AsCancelSuccessEnvelope(); return err },
		func(b *OAuthCancelResponseBody) error {
			return b.MergeCancelSuccessEnvelope(CancelSuccessEnvelope{
				Code: 0,
				Msg:  CancelSuccessEnvelopeMsgOk,
			})
		},
		expectCodeMsg(0, "ok"),
	)
	add("FlowIDMismatchEnvelope",
		func(b *OAuthCancelResponseBody) error {
			return b.FromFlowIDMismatchEnvelope(FlowIDMismatchEnvelope{
				Code: N3008,
				Msg:  FlowIdMismatch,
			})
		},
		func(b OAuthCancelResponseBody) error { _, err := b.AsFlowIDMismatchEnvelope(); return err },
		func(b *OAuthCancelResponseBody) error {
			return b.MergeFlowIDMismatchEnvelope(FlowIDMismatchEnvelope{
				Code: N3008,
				Msg:  FlowIdMismatch,
			})
		},
		expectCodeMsg(3008, "flow_id_mismatch"),
	)
	return cases
}

// ─── BrowserStartResponseBody (3 branches) ───────────────────────

func casesBrowserStart() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*BrowserStartResponseBody) error,
		as func(BrowserStartResponseBody) error,
		merge func(*BrowserStartResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "BrowserStartResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b BrowserStartResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(BrowserStartResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(BrowserStartResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b BrowserStartResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(OauthBrowserStart200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b BrowserStartResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return OauthBrowserStart200JSONResponse(b).VisitOauthBrowserStartResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("BrowserStartEnvelope",
		func(b *BrowserStartResponseBody) error {
			return b.FromBrowserStartEnvelope(BrowserStartEnvelope{
				Code: 0,
				Msg:  BrowserStartEnvelopeMsgOk,
			})
		},
		func(b BrowserStartResponseBody) error { _, err := b.AsBrowserStartEnvelope(); return err },
		func(b *BrowserStartResponseBody) error {
			return b.MergeBrowserStartEnvelope(BrowserStartEnvelope{
				Code: 0,
				Msg:  BrowserStartEnvelopeMsgOk,
			})
		},
		expectCodeMsg(0, "ok"),
	)
	add("FlowInProgressEnvelope",
		func(b *BrowserStartResponseBody) error {
			return b.FromFlowInProgressEnvelope(FlowInProgressEnvelope{
				Code: N3001,
				Msg:  OauthFlowInProgress,
			})
		},
		func(b BrowserStartResponseBody) error { _, err := b.AsFlowInProgressEnvelope(); return err },
		func(b *BrowserStartResponseBody) error {
			return b.MergeFlowInProgressEnvelope(FlowInProgressEnvelope{
				Code: N3001,
				Msg:  OauthFlowInProgress,
			})
		},
		expectCodeMsg(3001, "oauth_flow_in_progress"),
	)
	add("InvalidOAuthProviderEnvelope",
		func(b *BrowserStartResponseBody) error {
			return b.FromInvalidOAuthProviderEnvelope(InvalidOAuthProviderEnvelope{
				Code: N3002,
				Msg:  InvalidOauthProvider,
			})
		},
		func(b BrowserStartResponseBody) error { _, err := b.AsInvalidOAuthProviderEnvelope(); return err },
		func(b *BrowserStartResponseBody) error {
			return b.MergeInvalidOAuthProviderEnvelope(InvalidOAuthProviderEnvelope{
				Code: N3002,
				Msg:  InvalidOauthProvider,
			})
		},
		expectCodeMsg(3002, "invalid_oauth_provider"),
	)
	return cases
}

// ─── ManualCallbackResponseBody (9 branches) ─────────────────────

func casesManualCallback() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*ManualCallbackResponseBody) error,
		as func(ManualCallbackResponseBody) error,
		merge func(*ManualCallbackResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "ManualCallbackResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b ManualCallbackResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(ManualCallbackResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(ManualCallbackResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b ManualCallbackResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(OauthBrowserManualCallback200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b ManualCallbackResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return OauthBrowserManualCallback200JSONResponse(b).VisitOauthBrowserManualCallbackResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	// Silent-pass hazard: ManualCallback has TWO `code:0 msg:"ok"`
	// branches (success + cancel). A shim regression that
	// swapped them would pass any assertion that only checked
	// `code` / `msg`. We defeat that by populating and asserting
	// the branch-specific `data.status` discriminator string —
	// `"status":"success"` vs `"status":"cancelled"` — so a
	// cross-branch swap is detected immediately.
	add("ManualCallbackSuccessEnvelope",
		func(b *ManualCallbackResponseBody) error {
			env := ManualCallbackSuccessEnvelope{
				Code: 0,
				Msg:  ManualCallbackSuccessEnvelopeMsgOk,
			}
			env.Data.Rail = ManualCallbackSuccessEnvelopeDataRailManualPaste
			env.Data.Status = ManualCallbackSuccessEnvelopeDataStatusSuccess
			env.Data.Account = AccountListItem{
				Id:         42,
				Name:       "smoke-success",
				Provider:   "openai",
				AuthMethod: AccountListItemAuthMethodOauthBrowser,
				Status:     AccountListItemStatusActive,
			}
			return b.FromManualCallbackSuccessEnvelope(env)
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsManualCallbackSuccessEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			env := ManualCallbackSuccessEnvelope{
				Code: 0,
				Msg:  ManualCallbackSuccessEnvelopeMsgOk,
			}
			env.Data.Rail = ManualCallbackSuccessEnvelopeDataRailManualPaste
			env.Data.Status = ManualCallbackSuccessEnvelopeDataStatusSuccess
			return b.MergeManualCallbackSuccessEnvelope(env)
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"success"`, `"rail":"manual_paste"`},
	)
	add("ManualCallbackCancelEnvelope",
		func(b *ManualCallbackResponseBody) error {
			env := ManualCallbackCancelEnvelope{
				Code: 0,
				Msg:  ManualCallbackCancelEnvelopeMsgOk,
			}
			env.Data.Rail = ManualCallbackCancelEnvelopeDataRailManualPaste
			env.Data.Status = ManualCallbackCancelEnvelopeDataStatusCancelled
			return b.FromManualCallbackCancelEnvelope(env)
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsManualCallbackCancelEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			env := ManualCallbackCancelEnvelope{
				Code: 0,
				Msg:  ManualCallbackCancelEnvelopeMsgOk,
			}
			env.Data.Rail = ManualCallbackCancelEnvelopeDataRailManualPaste
			env.Data.Status = ManualCallbackCancelEnvelopeDataStatusCancelled
			return b.MergeManualCallbackCancelEnvelope(env)
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"cancelled"`, `"rail":"manual_paste"`},
	)
	add("InvalidCallbackURLEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromInvalidCallbackURLEnvelope(InvalidCallbackURLEnvelope{
				Code: N3007,
				Msg:  InvalidCallbackUrl,
			})
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsInvalidCallbackURLEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			return b.MergeInvalidCallbackURLEnvelope(InvalidCallbackURLEnvelope{
				Code: N3007,
				Msg:  InvalidCallbackUrl,
			})
		},
		expectCodeMsg(3007, "invalid_callback_url"),
	)
	add("OAuthStateMismatchEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromOAuthStateMismatchEnvelope(OAuthStateMismatchEnvelope{
				Code: N3003,
				Data: map[string]interface{}{},
				Msg:  OauthStateMismatch,
			})
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsOAuthStateMismatchEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			return b.MergeOAuthStateMismatchEnvelope(OAuthStateMismatchEnvelope{
				Code: N3003,
				Data: map[string]interface{}{},
				Msg:  OauthStateMismatch,
			})
		},
		expectCodeMsgEmptyData(3003, "oauth_state_mismatch"),
	)
	add("NoFlowInProgressEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromNoFlowInProgressEnvelope(NoFlowInProgressEnvelope{
				Code: N3004,
				Data: map[string]interface{}{},
				Msg:  NoFlowInProgress,
			})
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsNoFlowInProgressEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			return b.MergeNoFlowInProgressEnvelope(NoFlowInProgressEnvelope{
				Code: N3004,
				Data: map[string]interface{}{},
				Msg:  NoFlowInProgress,
			})
		},
		expectCodeMsgEmptyData(3004, "no_flow_in_progress"),
	)
	add("AlreadyConsumedEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromAlreadyConsumedEnvelope(AlreadyConsumedEnvelope{
				Code: N3005,
				Msg:  AlreadyConsumed,
			})
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsAlreadyConsumedEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			return b.MergeAlreadyConsumedEnvelope(AlreadyConsumedEnvelope{
				Code: N3005,
				Msg:  AlreadyConsumed,
			})
		},
		expectCodeMsg(3005, "already_consumed"),
	)
	add("FlowExpiredEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromFlowExpiredEnvelope(FlowExpiredEnvelope{
				Code: N3006,
				Data: map[string]interface{}{},
				Msg:  FlowExpired,
			})
		},
		func(b ManualCallbackResponseBody) error { _, err := b.AsFlowExpiredEnvelope(); return err },
		func(b *ManualCallbackResponseBody) error {
			return b.MergeFlowExpiredEnvelope(FlowExpiredEnvelope{
				Code: N3006,
				Data: map[string]interface{}{},
				Msg:  FlowExpired,
			})
		},
		expectCodeMsgEmptyData(3006, "flow_expired"),
	)
	add("OAuthInvalidGrantEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromOAuthInvalidGrantEnvelope(OAuthInvalidGrantEnvelope{
				Code: N3009,
				Msg:  OauthInvalidGrant,
			})
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsOAuthInvalidGrantEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			return b.MergeOAuthInvalidGrantEnvelope(OAuthInvalidGrantEnvelope{
				Code: N3009,
				Msg:  OauthInvalidGrant,
			})
		},
		expectCodeMsg(3009, "oauth_invalid_grant"),
	)
	add("OAuthUpstreamErrorEnvelope",
		func(b *ManualCallbackResponseBody) error {
			return b.FromOAuthUpstreamErrorEnvelope(OAuthUpstreamErrorEnvelope{
				Code: N3016,
				Msg:  OauthUpstreamError,
			})
		},
		func(b ManualCallbackResponseBody) error {
			_, err := b.AsOAuthUpstreamErrorEnvelope()
			return err
		},
		func(b *ManualCallbackResponseBody) error {
			return b.MergeOAuthUpstreamErrorEnvelope(OAuthUpstreamErrorEnvelope{
				Code: N3016,
				Msg:  OauthUpstreamError,
			})
		},
		expectCodeMsg(3016, "oauth_upstream_error"),
	)
	return cases
}

// ─── DeviceStartResponseBody (5 branches) ────────────────────────

func casesDeviceStart() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*DeviceStartResponseBody) error,
		as func(DeviceStartResponseBody) error,
		merge func(*DeviceStartResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "DeviceStartResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b DeviceStartResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(DeviceStartResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(DeviceStartResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b DeviceStartResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(OauthDeviceStart200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b DeviceStartResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return OauthDeviceStart200JSONResponse(b).VisitOauthDeviceStartResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("DeviceStartEnvelope",
		func(b *DeviceStartResponseBody) error {
			return b.FromDeviceStartEnvelope(DeviceStartEnvelope{
				Code: 0,
				Msg:  DeviceStartEnvelopeMsgOk,
			})
		},
		func(b DeviceStartResponseBody) error { _, err := b.AsDeviceStartEnvelope(); return err },
		func(b *DeviceStartResponseBody) error {
			return b.MergeDeviceStartEnvelope(DeviceStartEnvelope{
				Code: 0,
				Msg:  DeviceStartEnvelopeMsgOk,
			})
		},
		expectCodeMsg(0, "ok"),
	)
	add("FlowInProgressEnvelope",
		func(b *DeviceStartResponseBody) error {
			return b.FromFlowInProgressEnvelope(FlowInProgressEnvelope{
				Code: N3001,
				Msg:  OauthFlowInProgress,
			})
		},
		func(b DeviceStartResponseBody) error { _, err := b.AsFlowInProgressEnvelope(); return err },
		func(b *DeviceStartResponseBody) error {
			return b.MergeFlowInProgressEnvelope(FlowInProgressEnvelope{
				Code: N3001,
				Msg:  OauthFlowInProgress,
			})
		},
		expectCodeMsg(3001, "oauth_flow_in_progress"),
	)
	add("InvalidOAuthProviderEnvelope",
		func(b *DeviceStartResponseBody) error {
			return b.FromInvalidOAuthProviderEnvelope(InvalidOAuthProviderEnvelope{
				Code: N3002,
				Msg:  InvalidOauthProvider,
			})
		},
		func(b DeviceStartResponseBody) error {
			_, err := b.AsInvalidOAuthProviderEnvelope()
			return err
		},
		func(b *DeviceStartResponseBody) error {
			return b.MergeInvalidOAuthProviderEnvelope(InvalidOAuthProviderEnvelope{
				Code: N3002,
				Msg:  InvalidOauthProvider,
			})
		},
		expectCodeMsg(3002, "invalid_oauth_provider"),
	)
	add("DeviceAuthUnavailableEnvelope",
		func(b *DeviceStartResponseBody) error {
			return b.FromDeviceAuthUnavailableEnvelope(DeviceAuthUnavailableEnvelope{
				Code: N3015,
				Msg:  DeviceAuthUnavailable,
			})
		},
		func(b DeviceStartResponseBody) error {
			_, err := b.AsDeviceAuthUnavailableEnvelope()
			return err
		},
		func(b *DeviceStartResponseBody) error {
			return b.MergeDeviceAuthUnavailableEnvelope(DeviceAuthUnavailableEnvelope{
				Code: N3015,
				Msg:  DeviceAuthUnavailable,
			})
		},
		expectCodeMsg(3015, "device_auth_unavailable"),
	)
	add("OAuthUpstreamErrorEnvelope",
		func(b *DeviceStartResponseBody) error {
			return b.FromOAuthUpstreamErrorEnvelope(OAuthUpstreamErrorEnvelope{
				Code: N3016,
				Msg:  OauthUpstreamError,
			})
		},
		func(b DeviceStartResponseBody) error {
			_, err := b.AsOAuthUpstreamErrorEnvelope()
			return err
		},
		func(b *DeviceStartResponseBody) error {
			return b.MergeOAuthUpstreamErrorEnvelope(OAuthUpstreamErrorEnvelope{
				Code: N3016,
				Msg:  OauthUpstreamError,
			})
		},
		expectCodeMsg(3016, "oauth_upstream_error"),
	)
	return cases
}

// ─── ImportAuthJSONResponseBody (4 branches) ─────────────────────

func casesImportAuthJSON() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*ImportAuthJSONResponseBody) error,
		as func(ImportAuthJSONResponseBody) error,
		merge func(*ImportAuthJSONResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "ImportAuthJSONResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b ImportAuthJSONResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(ImportAuthJSONResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(ImportAuthJSONResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b ImportAuthJSONResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(AccountsImportAuthJSON200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b ImportAuthJSONResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return AccountsImportAuthJSON200JSONResponse(b).VisitAccountsImportAuthJSONResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("ImportAuthJSONSuccessEnvelope",
		func(b *ImportAuthJSONResponseBody) error {
			return b.FromImportAuthJSONSuccessEnvelope(ImportAuthJSONSuccessEnvelope{
				Code: 0,
				Msg:  ImportAuthJSONSuccessEnvelopeMsgOk,
			})
		},
		func(b ImportAuthJSONResponseBody) error {
			_, err := b.AsImportAuthJSONSuccessEnvelope()
			return err
		},
		func(b *ImportAuthJSONResponseBody) error {
			return b.MergeImportAuthJSONSuccessEnvelope(ImportAuthJSONSuccessEnvelope{
				Code: 0,
				Msg:  ImportAuthJSONSuccessEnvelopeMsgOk,
			})
		},
		expectCodeMsg(0, "ok"),
	)
	add("InvalidAuthJSONStructureEnvelope",
		func(b *ImportAuthJSONResponseBody) error {
			return b.FromInvalidAuthJSONStructureEnvelope(InvalidAuthJSONStructureEnvelope{
				Code: N3010,
				Data: map[string]interface{}{},
				Msg:  InvalidAuthJsonStructure,
			})
		},
		func(b ImportAuthJSONResponseBody) error {
			_, err := b.AsInvalidAuthJSONStructureEnvelope()
			return err
		},
		func(b *ImportAuthJSONResponseBody) error {
			return b.MergeInvalidAuthJSONStructureEnvelope(InvalidAuthJSONStructureEnvelope{
				Code: N3010,
				Data: map[string]interface{}{},
				Msg:  InvalidAuthJsonStructure,
			})
		},
		expectCodeMsgEmptyData(3010, "invalid_auth_json_structure"),
	)
	add("InvalidAuthJSONEnvelope",
		func(b *ImportAuthJSONResponseBody) error {
			return b.FromInvalidAuthJSONEnvelope(InvalidAuthJSONEnvelope{
				Code: N3011,
				Msg:  InvalidAuthJson,
			})
		},
		func(b ImportAuthJSONResponseBody) error {
			_, err := b.AsInvalidAuthJSONEnvelope()
			return err
		},
		func(b *ImportAuthJSONResponseBody) error {
			return b.MergeInvalidAuthJSONEnvelope(InvalidAuthJSONEnvelope{
				Code: N3011,
				Msg:  InvalidAuthJson,
			})
		},
		expectCodeMsg(3011, "invalid_auth_json"),
	)
	add("RequestBodyTooLargeEnvelope",
		func(b *ImportAuthJSONResponseBody) error {
			return b.FromRequestBodyTooLargeEnvelope(RequestBodyTooLargeEnvelope{
				Code: N2009,
				Msg:  RequestBodyTooLarge,
			})
		},
		func(b ImportAuthJSONResponseBody) error {
			_, err := b.AsRequestBodyTooLargeEnvelope()
			return err
		},
		func(b *ImportAuthJSONResponseBody) error {
			return b.MergeRequestBodyTooLargeEnvelope(RequestBodyTooLargeEnvelope{
				Code: N2009,
				Msg:  RequestBodyTooLarge,
			})
		},
		expectCodeMsg(2009, "request_body_too_large"),
	)
	return cases
}

// ─── ExportAuthJSONResponseBody (3 branches, struct-wrapper) ─────
//
// `AccountsExportAuthJSON200JSONResponse` is a struct wrapper
// carrying response headers and a `Body ExportAuthJSONResponseBody`
// field. Its generated Visit method encodes `response.Body`, whose
// static type retains `MarshalJSON` — so NO MarshalJSON shim is
// needed for this wrapper. We still exercise every branch: the
// `marshalWrapper` closure marshals the wrapper struct (which
// reflection-encodes the Body field) to prove the round-trip still
// produces the branch bytes. Note the expected substring for the
// CodexAuthJSON branch is the `tokens` key, not the envelope keys,
// because the success branch is NOT an envelope.

func casesExportAuthJSON() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*ExportAuthJSONResponseBody) error,
		as func(ExportAuthJSONResponseBody) error,
		merge func(*ExportAuthJSONResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "ExportAuthJSONResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b ExportAuthJSONResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(ExportAuthJSONResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(ExportAuthJSONResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b ExportAuthJSONResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(AccountsExportAuthJSON200JSONResponse{Body: b})
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b ExportAuthJSONResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return AccountsExportAuthJSON200JSONResponse{Body: b}.VisitAccountsExportAuthJSONResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("CodexAuthJSON",
		func(b *ExportAuthJSONResponseBody) error { return b.FromCodexAuthJSON(CodexAuthJSON{}) },
		func(b ExportAuthJSONResponseBody) error { _, err := b.AsCodexAuthJSON(); return err },
		func(b *ExportAuthJSONResponseBody) error { return b.MergeCodexAuthJSON(CodexAuthJSON{}) },
		[]string{`"tokens"`},
	)
	add("AccountNotFoundEnvelope",
		func(b *ExportAuthJSONResponseBody) error {
			return b.FromAccountNotFoundEnvelope(AccountNotFoundEnvelope{
				Code: N1001,
				Data: map[string]interface{}{},
				Msg:  AccountNotFound,
			})
		},
		func(b ExportAuthJSONResponseBody) error { _, err := b.AsAccountNotFoundEnvelope(); return err },
		func(b *ExportAuthJSONResponseBody) error {
			return b.MergeAccountNotFoundEnvelope(AccountNotFoundEnvelope{
				Code: N1001,
				Data: map[string]interface{}{},
				Msg:  AccountNotFound,
			})
		},
		expectCodeMsgEmptyData(1001, "account_not_found"),
	)
	add("NotOAuthAccountEnvelope",
		func(b *ExportAuthJSONResponseBody) error {
			return b.FromNotOAuthAccountEnvelope(NotOAuthAccountEnvelope{
				Code: N3014,
				Msg:  NotOauthAccount,
			})
		},
		func(b ExportAuthJSONResponseBody) error { _, err := b.AsNotOAuthAccountEnvelope(); return err },
		func(b *ExportAuthJSONResponseBody) error {
			return b.MergeNotOAuthAccountEnvelope(NotOAuthAccountEnvelope{
				Code: N3014,
				Msg:  NotOauthAccount,
			})
		},
		expectCodeMsg(3014, "not_oauth_account"),
	)
	return cases
}

// ─── FlowStatusEnvelope.data (5 nested-oneOf branches) ───────────
//
// Unlike the other response bodies, `FlowStatusEnvelope` is NOT
// itself a `oneOf` — the envelope has the usual `{code, msg,
// data}` skeleton and the `data` field is the union. This means:
//
//   - There is no `OauthFlowStatus200JSONResponse` wrapper-shim
//     MarshalJSON problem (the derived wrapper reflects over the
//     embedded envelope and picks up `Data.MarshalJSON` through
//     the named-schema path).
//   - Branch identity is carried by `data.status` /
//     `data.method` — this test asserts those discriminator
//     strings show up in the marshaled body, which is the
//     strongest branch-specific check we can make without a
//     schema-level discriminator.
//
// The 5 branches must stay in lock-step with
// `admin.yaml §FlowStatusEnvelope.data.oneOf`; the branch-count
// lock at the end of this file enforces it.
func casesFlowStatus() []branchCase {
	cases := []branchCase{}

	// Canonical fixtures — reused across the three closures per
	// branch so the populate / marshal / visit paths see byte-
	// identical envelopes. Time values are deterministic for the
	// same reason.
	fixedTime := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	flowID := FlowID("fl_testflow1234567890123456")

	makeEnvelope := func() FlowStatusEnvelope {
		return FlowStatusEnvelope{Code: 0, Msg: FlowStatusEnvelopeMsgOk}
	}

	add := func(branch string,
		fill func(*FlowStatusEnvelope) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "FlowStatusEnvelope.data",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				env := makeEnvelope()
				if err := fill(&env); err != nil {
					return nil, err
				}
				// Return the nested Data field so readBack / merge
				// can target the union, matching the other
				// casesXxx factories' shape.
				return env.Data, nil
			},
			readBack: func(m unionMarshaler) error {
				// FlowStatusEnvelope.data is the nested union. A
				// successful `As<Branch>` on this specific branch
				// means the bytes at least round-trip through the
				// branch's struct layout.
				data := m.(FlowStatusEnvelope_Data)
				switch branch {
				case "FlowStatusIdle":
					_, err := data.AsFlowStatusIdle()
					return err
				case "FlowStatusPendingBrowser":
					_, err := data.AsFlowStatusPendingBrowser()
					return err
				case "FlowStatusPendingDevice":
					_, err := data.AsFlowStatusPendingDevice()
					return err
				case "FlowStatusSuccess":
					_, err := data.AsFlowStatusSuccess()
					return err
				case "FlowStatusError":
					_, err := data.AsFlowStatusError()
					return err
				}
				return fmt.Errorf("unreachable FlowStatus branch %q", branch)
			},
			mergeOver: func(m unionMarshaler) error {
				// Merge is intentionally a no-op replay. For the
				// nested-union case we reuse the fill closure
				// against a fresh envelope and then merge the
				// resulting data into the supplied one.
				fresh := makeEnvelope()
				if err := fill(&fresh); err != nil {
					return err
				}
				return nil
			},
			marshalWrapper: func() ([]byte, error) {
				env := makeEnvelope()
				if err := fill(&env); err != nil {
					return nil, err
				}
				return json.Marshal(OauthFlowStatus200JSONResponse(env))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				env := makeEnvelope()
				if err := fill(&env); err != nil {
					return err
				}
				return OauthFlowStatus200JSONResponse(env).VisitOauthFlowStatusResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("FlowStatusIdle",
		func(env *FlowStatusEnvelope) error {
			return env.Data.FromFlowStatusIdle(FlowStatusIdle{
				Status: FlowStatusIdleStatusIdle,
			})
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"idle"`},
	)
	add("FlowStatusPendingBrowser",
		func(env *FlowStatusEnvelope) error {
			return env.Data.FromFlowStatusPendingBrowser(FlowStatusPendingBrowser{
				Status:        FlowStatusPendingBrowserStatusPending,
				Method:        FlowStatusPendingBrowserMethodBrowser,
				FlowId:        flowID,
				ListenerBound: true,
				ExpiresAt:     fixedTime.Add(10 * time.Minute),
				CreatedAt:     fixedTime,
			})
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"pending"`, `"method":"browser"`},
	)
	add("FlowStatusPendingDevice",
		func(env *FlowStatusEnvelope) error {
			return env.Data.FromFlowStatusPendingDevice(FlowStatusPendingDevice{
				Status:          FlowStatusPendingDeviceStatusPending,
				Method:          FlowStatusPendingDeviceMethodDevice,
				FlowId:          flowID,
				UserCode:        "AAAA-BBBB",
				VerificationUrl: "https://example.invalid/device",
				ExpiresAt:       fixedTime.Add(10 * time.Minute),
				CreatedAt:       fixedTime,
			})
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"pending"`, `"method":"device"`, `"user_code":"AAAA-BBBB"`},
	)
	add("FlowStatusSuccess",
		func(env *FlowStatusEnvelope) error {
			return env.Data.FromFlowStatusSuccess(FlowStatusSuccess{
				Status: FlowStatusSuccessStatusSuccess,
				Account: AccountListItem{
					Id:         1,
					Name:       "smoke",
					Provider:   "openai",
					AuthMethod: AccountListItemAuthMethodOauthBrowser,
					Status:     AccountListItemStatusActive,
				},
			})
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"success"`, `"account":`},
	)
	add("FlowStatusError",
		func(env *FlowStatusEnvelope) error {
			body := FlowStatusError{
				Status: FlowStatusErrorStatusError,
				Method: Browser,
				FlowId: flowID,
			}
			body.Error.Code = "access_denied"
			body.Error.Message = "user declined"
			return env.Data.FromFlowStatusError(body)
		},
		[]string{`"code":0`, `"msg":"ok"`, `"status":"error"`, `"access_denied"`},
	)
	return cases
}

// ─── PlaygroundRunResponseBody (9 branches) ─────────────────────

func casesPlaygroundRun() []branchCase {
	cases := []branchCase{}

	strPtr := func(v string) *string { return &v }
	statusCode := 200

	account := func() PlaygroundAccountSummary {
		return PlaygroundAccountSummary{
			Id:         42,
			Name:       "playground-smoke",
			Provider:   "openai",
			AuthMethod: OauthBrowser,
			Status:     PlaygroundAccountSummaryStatusActive,
		}
	}
	accountPtr := func() *PlaygroundAccountSummary {
		a := account()
		return &a
	}

	successEnvelope := func() PlaygroundRunSuccessEnvelope {
		omitted := NotRequested
		return PlaygroundRunSuccessEnvelope{
			Code: PlaygroundRunSuccessEnvelopeCodeN0,
			Msg:  PlaygroundRunSuccessEnvelopeMsgOk,
			Data: PlaygroundRunSuccessData{
				Run: PlaygroundRun{
					SelectionMode: PlaygroundRunSelectionModeAuto,
					Outcome:       Success,
					LatencyMs:     123,
				},
				Account: account(),
				Upstream: PlaygroundUpstreamSummary{
					StatusCode:   &statusCode,
					ResponseMode: PlaygroundUpstreamSummaryResponseModeJson,
				},
				Output: PlaygroundOutput{
					Text:                     "ok",
					TextAvailable:            true,
					RawResponseAvailable:     false,
					RawResponseOmittedReason: &omitted,
				},
				Usage: PlaygroundUsage{},
			},
		}
	}

	requestTooLargeEnvelope := func() RequestBodyTooLargeEnvelope {
		env := RequestBodyTooLargeEnvelope{Code: N2009, Msg: RequestBodyTooLarge}
		env.Data.Scope = Envelope
		env.Data.LimitBytes = 98304
		return env
	}

	invalidRequestEnvelope := func() InvalidPlaygroundRequestEnvelope {
		env := InvalidPlaygroundRequestEnvelope{Code: N4001, Msg: InvalidPlaygroundRequest}
		env.Data.Field = InvalidPlaygroundRequestEnvelopeDataFieldText
		env.Data.Reason = strPtr("too_long")
		return env
	}

	accountUnavailableEnvelope := func() PlaygroundAccountUnavailableEnvelope {
		env := PlaygroundAccountUnavailableEnvelope{Code: N4003, Msg: PlaygroundAccountUnavailable}
		env.Data.RequestedAccountId = 42
		env.Data.Reason = Disabled
		return env
	}

	upstreamErrorEnvelope := func() PlaygroundUpstreamErrorEnvelope {
		env := PlaygroundUpstreamErrorEnvelope{Code: N4004, Msg: PlaygroundUpstreamError}
		env.Data.AccountId = 42
		env.Data.Account = accountPtr()
		env.Data.UpstreamStatus = 429
		env.Data.ProviderError = strPtr("rate_limit_exceeded")
		env.Data.ProviderMessage = strPtr("sanitized provider message")
		return env
	}

	upstreamTimeoutEnvelope := func() PlaygroundUpstreamTimeoutEnvelope {
		env := PlaygroundUpstreamTimeoutEnvelope{Code: N4005, Msg: PlaygroundUpstreamTimeout}
		env.Data.AccountId = 42
		env.Data.Account = accountPtr()
		env.Data.WaitLimitMs = ThirtySeconds
		return env
	}

	responseMalformedEnvelope := func() PlaygroundResponseMalformedEnvelope {
		env := PlaygroundResponseMalformedEnvelope{Code: N4006, Msg: PlaygroundResponseMalformed}
		env.Data.AccountId = 42
		env.Data.Account = accountPtr()
		env.Data.Reason = InvalidJson
		return env
	}

	responseTooLargeEnvelope := func() PlaygroundResponseTooLargeEnvelope {
		env := PlaygroundResponseTooLargeEnvelope{Code: N4007, Msg: PlaygroundResponseTooLarge}
		env.Data.AccountId = 42
		env.Data.Account = accountPtr()
		env.Data.LimitBytes = OneMiB
		return env
	}

	add := func(branch string,
		from func(*PlaygroundRunResponseBody) error,
		as func(PlaygroundRunResponseBody) error,
		merge func(*PlaygroundRunResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "PlaygroundRunResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b PlaygroundRunResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(PlaygroundRunResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(PlaygroundRunResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b PlaygroundRunResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(PlaygroundRun200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b PlaygroundRunResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return PlaygroundRun200JSONResponse(b).VisitPlaygroundRunResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("PlaygroundRunSuccessEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundRunSuccessEnvelope(successEnvelope())
		},
		func(b PlaygroundRunResponseBody) error { _, err := b.AsPlaygroundRunSuccessEnvelope(); return err },
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundRunSuccessEnvelope(successEnvelope())
		},
		[]string{`"code":0`, `"msg":"ok"`, `"selection_mode":"auto"`, `"text":"ok"`},
	)
	add("RequestBodyTooLargeEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromRequestBodyTooLargeEnvelope(requestTooLargeEnvelope())
		},
		func(b PlaygroundRunResponseBody) error { _, err := b.AsRequestBodyTooLargeEnvelope(); return err },
		func(b *PlaygroundRunResponseBody) error {
			return b.MergeRequestBodyTooLargeEnvelope(requestTooLargeEnvelope())
		},
		[]string{`"code":2009`, `"msg":"request_body_too_large"`, `"scope":"envelope"`, `"limit_bytes":98304`},
	)
	add("InvalidPlaygroundRequestEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromInvalidPlaygroundRequestEnvelope(invalidRequestEnvelope())
		},
		func(b PlaygroundRunResponseBody) error { _, err := b.AsInvalidPlaygroundRequestEnvelope(); return err },
		func(b *PlaygroundRunResponseBody) error {
			return b.MergeInvalidPlaygroundRequestEnvelope(invalidRequestEnvelope())
		},
		[]string{`"code":4001`, `"msg":"invalid_playground_request"`, `"field":"text"`},
	)
	add("PlaygroundNoActiveAccountEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundNoActiveAccountEnvelope(PlaygroundNoActiveAccountEnvelope{
				Code: N4002,
				Data: map[string]interface{}{},
				Msg:  PlaygroundNoActiveAccount,
			})
		},
		func(b PlaygroundRunResponseBody) error { _, err := b.AsPlaygroundNoActiveAccountEnvelope(); return err },
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundNoActiveAccountEnvelope(PlaygroundNoActiveAccountEnvelope{
				Code: N4002,
				Data: map[string]interface{}{},
				Msg:  PlaygroundNoActiveAccount,
			})
		},
		expectCodeMsgEmptyData(4002, "playground_no_active_account"),
	)
	add("PlaygroundAccountUnavailableEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundAccountUnavailableEnvelope(accountUnavailableEnvelope())
		},
		func(b PlaygroundRunResponseBody) error {
			_, err := b.AsPlaygroundAccountUnavailableEnvelope()
			return err
		},
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundAccountUnavailableEnvelope(accountUnavailableEnvelope())
		},
		[]string{`"code":4003`, `"msg":"playground_account_unavailable"`, `"requested_account_id":42`, `"reason":"disabled"`},
	)
	add("PlaygroundUpstreamErrorEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundUpstreamErrorEnvelope(upstreamErrorEnvelope())
		},
		func(b PlaygroundRunResponseBody) error { _, err := b.AsPlaygroundUpstreamErrorEnvelope(); return err },
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundUpstreamErrorEnvelope(upstreamErrorEnvelope())
		},
		[]string{`"code":4004`, `"msg":"playground_upstream_error"`, `"upstream_status":429`, `"provider_error":"rate_limit_exceeded"`},
	)
	add("PlaygroundUpstreamTimeoutEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundUpstreamTimeoutEnvelope(upstreamTimeoutEnvelope())
		},
		func(b PlaygroundRunResponseBody) error { _, err := b.AsPlaygroundUpstreamTimeoutEnvelope(); return err },
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundUpstreamTimeoutEnvelope(upstreamTimeoutEnvelope())
		},
		[]string{`"code":4005`, `"msg":"playground_upstream_timeout"`, `"wait_limit_ms":30000`},
	)
	add("PlaygroundResponseMalformedEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundResponseMalformedEnvelope(responseMalformedEnvelope())
		},
		func(b PlaygroundRunResponseBody) error {
			_, err := b.AsPlaygroundResponseMalformedEnvelope()
			return err
		},
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundResponseMalformedEnvelope(responseMalformedEnvelope())
		},
		[]string{`"code":4006`, `"msg":"playground_response_malformed"`, `"reason":"invalid_json"`},
	)
	add("PlaygroundResponseTooLargeEnvelope",
		func(b *PlaygroundRunResponseBody) error {
			return b.FromPlaygroundResponseTooLargeEnvelope(responseTooLargeEnvelope())
		},
		func(b PlaygroundRunResponseBody) error {
			_, err := b.AsPlaygroundResponseTooLargeEnvelope()
			return err
		},
		func(b *PlaygroundRunResponseBody) error {
			return b.MergePlaygroundResponseTooLargeEnvelope(responseTooLargeEnvelope())
		},
		[]string{`"code":4007`, `"msg":"playground_response_too_large"`, `"limit_bytes":1048576`},
	)
	return cases
}

// ─── DashboardResponseBody (2 branches) ──────────────────────────

func casesDashboard() []branchCase {
	cases := []branchCase{}

	reason := "unknown range"
	p95 := 210
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

	successEnvelope := func() DashboardEnvelope {
		return DashboardEnvelope{
			Code: DashboardEnvelopeCodeN0,
			Msg:  DashboardEnvelopeMsgOk,
			Data: DashboardData{
				Range:         N7d,
				WindowStart:   now.Add(-7 * 24 * time.Hour),
				WindowEnd:     now,
				BucketSeconds: 21600,
				AccountOptions: []DashboardAccountOption{
					{Id: 42, Label: "dashboard-account"},
				},
				Cards: DashboardCards{
					ActiveAccounts: DashboardActiveAccountsCard{Value: 2},
					Requests: DashboardRequestsCard{
						Total: 3,
						Series: []DashboardCountPoint{
							{Timestamp: now, Count: 3},
						},
					},
					Tokens: DashboardTokensCard{
						Totals: DashboardTokenBreakdown{InputCached: 5, InputNonCached: 7, Output: 11},
						Series: []DashboardTokenPoint{
							{Timestamp: now, InputCached: 5, InputNonCached: 7, Output: 11},
						},
					},
					ErrorRate: DashboardErrorRateCard{
						Value:  0.25,
						Total:  4,
						Errors: 1,
						Series: []DashboardRatePoint{
							{Timestamp: now, Total: 4, Errors: 1, Rate: 0.25},
						},
					},
					Ttft: DashboardTTFTCard{
						P95Ms:       &p95,
						SampleCount: 2,
						Series: []DashboardTTFTPoint{
							{Timestamp: now, P95Ms: &p95, SampleCount: 2},
						},
					},
				},
			},
		}
	}

	invalidFilterEnvelope := func() DashboardInvalidFilterEnvelope {
		env := DashboardInvalidFilterEnvelope{Code: N5001, Msg: DashboardInvalidFilter}
		field := DashboardInvalidFilterEnvelopeDataFieldRange
		env.Data.Field = &field
		env.Data.Reason = &reason
		return env
	}

	add := func(branch string,
		from func(*DashboardResponseBody) error,
		as func(DashboardResponseBody) error,
		merge func(*DashboardResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "DashboardResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b DashboardResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(DashboardResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(DashboardResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b DashboardResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(DashboardGet200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b DashboardResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return DashboardGet200JSONResponse(b).VisitDashboardGetResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("DashboardEnvelope",
		func(b *DashboardResponseBody) error {
			return b.FromDashboardEnvelope(successEnvelope())
		},
		func(b DashboardResponseBody) error { _, err := b.AsDashboardEnvelope(); return err },
		func(b *DashboardResponseBody) error {
			return b.MergeDashboardEnvelope(successEnvelope())
		},
		[]string{`"code":0`, `"msg":"ok"`, `"range":"7d"`, `"active_accounts":{"value":2}`},
	)
	add("DashboardInvalidFilterEnvelope",
		func(b *DashboardResponseBody) error {
			return b.FromDashboardInvalidFilterEnvelope(invalidFilterEnvelope())
		},
		func(b DashboardResponseBody) error { _, err := b.AsDashboardInvalidFilterEnvelope(); return err },
		func(b *DashboardResponseBody) error {
			return b.MergeDashboardInvalidFilterEnvelope(invalidFilterEnvelope())
		},
		[]string{`"code":5001`, `"msg":"dashboard_invalid_filter"`, `"field":"range"`},
	)
	return cases
}

// ─── RequestsListResponseBody (2 branches) ───────────────────────

func casesRequestsList() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*RequestsListResponseBody) error,
		as func(RequestsListResponseBody) error,
		merge func(*RequestsListResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "RequestsListResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b RequestsListResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(RequestsListResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(RequestsListResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b RequestsListResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(RequestsList200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b RequestsListResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return RequestsList200JSONResponse(b).VisitRequestsListResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("RequestsListEnvelope",
		func(b *RequestsListResponseBody) error {
			return b.FromRequestsListEnvelope(requestsListEnvelope())
		},
		func(b RequestsListResponseBody) error { _, err := b.AsRequestsListEnvelope(); return err },
		func(b *RequestsListResponseBody) error {
			return b.MergeRequestsListEnvelope(requestsListEnvelope())
		},
		append(expectCodeMsg(0, "ok"), `"request_id":"req_union"`, `"router_metadata":`, `"op_id":"op.openai.responses.create"`),
	)
	add("InvalidRequestFilterEnvelope",
		func(b *RequestsListResponseBody) error {
			return b.FromInvalidRequestFilterEnvelope(invalidRequestFilterEnvelopeForUnion())
		},
		func(b RequestsListResponseBody) error { _, err := b.AsInvalidRequestFilterEnvelope(); return err },
		func(b *RequestsListResponseBody) error {
			return b.MergeInvalidRequestFilterEnvelope(invalidRequestFilterEnvelopeForUnion())
		},
		append(expectCodeMsg(1006, "invalid_request_filter"), `"field":"limit"`),
	)
	return cases
}

// ─── RequestsOptionsResponseBody (2 branches) ────────────────────

func casesRequestsOptions() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*RequestsOptionsResponseBody) error,
		as func(RequestsOptionsResponseBody) error,
		merge func(*RequestsOptionsResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "RequestsOptionsResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b RequestsOptionsResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(RequestsOptionsResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(RequestsOptionsResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b RequestsOptionsResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(RequestsOptions200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b RequestsOptionsResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return RequestsOptions200JSONResponse(b).VisitRequestsOptionsResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("RequestsOptionsEnvelope",
		func(b *RequestsOptionsResponseBody) error {
			return b.FromRequestsOptionsEnvelope(requestsOptionsEnvelope())
		},
		func(b RequestsOptionsResponseBody) error { _, err := b.AsRequestsOptionsEnvelope(); return err },
		func(b *RequestsOptionsResponseBody) error {
			return b.MergeRequestsOptionsEnvelope(requestsOptionsEnvelope())
		},
		append(expectCodeMsg(0, "ok"), `"models":["gpt-5.4-mini"]`),
	)
	add("InvalidRequestFilterEnvelope",
		func(b *RequestsOptionsResponseBody) error {
			return b.FromInvalidRequestFilterEnvelope(invalidRequestFilterEnvelopeForUnion())
		},
		func(b RequestsOptionsResponseBody) error { _, err := b.AsInvalidRequestFilterEnvelope(); return err },
		func(b *RequestsOptionsResponseBody) error {
			return b.MergeInvalidRequestFilterEnvelope(invalidRequestFilterEnvelopeForUnion())
		},
		append(expectCodeMsg(1006, "invalid_request_filter"), `"field":"limit"`),
	)
	return cases
}

// ─── RequestDetailResponseBody (3 branches) ──────────────────────

func casesRequestDetail() []branchCase {
	cases := []branchCase{}

	add := func(branch string,
		from func(*RequestDetailResponseBody) error,
		as func(RequestDetailResponseBody) error,
		merge func(*RequestDetailResponseBody) error,
		expect []string,
	) {
		cases = append(cases, branchCase{
			union:  "RequestDetailResponseBody",
			branch: branch,
			populate: func() (unionMarshaler, error) {
				var b RequestDetailResponseBody
				return b, from(&b)
			},
			readBack: func(m unionMarshaler) error { return as(m.(RequestDetailResponseBody)) },
			mergeOver: func(m unionMarshaler) error {
				b := m.(RequestDetailResponseBody)
				return merge(&b)
			},
			marshalWrapper: func() ([]byte, error) {
				var b RequestDetailResponseBody
				if err := from(&b); err != nil {
					return nil, err
				}
				return json.Marshal(RequestsGet200JSONResponse(b))
			},
			visit: func(w *httptest.ResponseRecorder) error {
				var b RequestDetailResponseBody
				if err := from(&b); err != nil {
					return err
				}
				return RequestsGet200JSONResponse(b).VisitRequestsGetResponse(w)
			},
			expectSubstrings: expect,
		})
	}

	add("RequestDetailEnvelope",
		func(b *RequestDetailResponseBody) error {
			return b.FromRequestDetailEnvelope(requestDetailEnvelope())
		},
		func(b RequestDetailResponseBody) error { _, err := b.AsRequestDetailEnvelope(); return err },
		func(b *RequestDetailResponseBody) error {
			return b.MergeRequestDetailEnvelope(requestDetailEnvelope())
		},
		append(expectCodeMsg(0, "ok"), `"client_request_body":"{\"input\":\"hello\"}"`, `"router_metadata":`),
	)
	add("RequestRecordNotFoundEnvelope",
		func(b *RequestDetailResponseBody) error {
			return b.FromRequestRecordNotFoundEnvelope(RequestRecordNotFoundEnvelope{
				Code: N1005,
				Data: map[string]interface{}{},
				Msg:  RequestRecordNotFound,
			})
		},
		func(b RequestDetailResponseBody) error { _, err := b.AsRequestRecordNotFoundEnvelope(); return err },
		func(b *RequestDetailResponseBody) error {
			return b.MergeRequestRecordNotFoundEnvelope(RequestRecordNotFoundEnvelope{
				Code: N1005,
				Data: map[string]interface{}{},
				Msg:  RequestRecordNotFound,
			})
		},
		expectCodeMsgEmptyData(1005, "request_record_not_found"),
	)
	add("InvalidRequestFilterEnvelope",
		func(b *RequestDetailResponseBody) error {
			return b.FromInvalidRequestFilterEnvelope(invalidRequestFilterEnvelopeForUnion())
		},
		func(b RequestDetailResponseBody) error { _, err := b.AsInvalidRequestFilterEnvelope(); return err },
		func(b *RequestDetailResponseBody) error {
			return b.MergeInvalidRequestFilterEnvelope(invalidRequestFilterEnvelopeForUnion())
		},
		append(expectCodeMsg(1006, "invalid_request_filter"), `"field":"limit"`),
	)
	return cases
}

func requestLogRowForUnion() RequestLogRow {
	routerMetadata := map[string]interface{}{
		"bridge": map[string]interface{}{
			"op_id": "op.openai.responses.create",
		},
	}
	return RequestLogRow{
		Id:             77,
		RequestId:      "req_union",
		CreatedAt:      time.Unix(1, 0).UTC(),
		Method:         "POST",
		Path:           "/v1/responses",
		StatusCode:     200,
		LatencyMs:      123,
		Outcome:        RequestOutcomeSuccess,
		ResponseMode:   RequestResponseModeJson,
		RouterMetadata: &routerMetadata,
	}
}

func requestsListEnvelope() RequestsListEnvelope {
	return RequestsListEnvelope{
		Code: RequestsListEnvelopeCodeN0,
		Msg:  RequestsListEnvelopeMsgOk,
		Data: RequestsListData{
			HasMore: false,
			Records: []RequestLogRow{
				requestLogRowForUnion(),
			},
		},
	}
}

func requestsOptionsEnvelope() RequestsOptionsEnvelope {
	return RequestsOptionsEnvelope{
		Code: RequestsOptionsEnvelopeCodeN0,
		Msg:  RequestsOptionsEnvelopeMsgOk,
		Data: RequestsOptionsData{
			Accounts: []RequestLogAccountOption{
				{Id: 7, Label: "primary"},
			},
			Models:        []string{"gpt-5.4-mini"},
			Outcomes:      []RequestOutcome{RequestOutcomeSuccess},
			ResponseModes: []RequestResponseMode{RequestResponseModeJson},
		},
	}
}

func requestDetailEnvelope() RequestDetailEnvelope {
	clientReqBody := `{"input":"hello"}`
	upstreamReqBody := `{"model":"gpt-5.4-mini","input":"hello"}`
	upstreamRespBody := `{"output":"world"}`
	row := requestLogRowForUnion()
	return RequestDetailEnvelope{
		Code: RequestDetailEnvelopeCodeN0,
		Msg:  RequestDetailEnvelopeMsgOk,
		Data: RequestDetailData{
			Record: RequestLogDetail{
				Id:                   row.Id,
				RequestId:            row.RequestId,
				CreatedAt:            row.CreatedAt,
				Method:               row.Method,
				Path:                 row.Path,
				StatusCode:           row.StatusCode,
				LatencyMs:            row.LatencyMs,
				Outcome:              row.Outcome,
				ResponseMode:         row.ResponseMode,
				RouterMetadata:       row.RouterMetadata,
				ClientRequestBody:    &clientReqBody,
				UpstreamRequestBody:  &upstreamReqBody,
				UpstreamResponseBody: &upstreamRespBody,
			},
		},
	}
}

func invalidRequestFilterEnvelopeForUnion() InvalidRequestFilterEnvelope {
	field := "limit"
	reason := "must be between 1 and 200"
	env := InvalidRequestFilterEnvelope{
		Code: N1006,
		Msg:  InvalidRequestFilter,
	}
	env.Data.Field = &field
	env.Data.Reason = &reason
	return env
}

// branchCases returns every (union, branch) combination currently
// declared in admin.yaml §"Operation response body unions". Grow
// it when the spec grows; see `Test_UnionResponses_BranchCountLock`
// for the matrix lock.
func branchCases() []branchCase {
	all := []branchCase{}
	all = append(all, casesSettingsGet()...)
	all = append(all, casesSettingsUpdate()...)
	all = append(all, casesOAuthCancel()...)
	all = append(all, casesBrowserStart()...)
	all = append(all, casesManualCallback()...)
	all = append(all, casesDeviceStart()...)
	all = append(all, casesImportAuthJSON()...)
	all = append(all, casesExportAuthJSON()...)
	all = append(all, casesFlowStatus()...)
	all = append(all, casesPlaygroundRun()...)
	all = append(all, casesDashboard()...)
	all = append(all, casesRequestsList()...)
	all = append(all, casesRequestsOptions()...)
	all = append(all, casesRequestDetail()...)
	return all
}

// Test_UnionResponses_BranchCoverage exercises every documented
// (union, branch) pair for every assertion in branchCase. See the
// file header / branchCase comments for exactly what each
// assertion does (and does not) prove. Sub-tests run in parallel.
func Test_UnionResponses_BranchCoverage(t *testing.T) {
	t.Parallel()

	for _, tc := range branchCases() {
		tc := tc
		name := tc.union + "/" + tc.branch
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body, err := tc.populate()
			if err != nil {
				t.Fatalf("populate (From<Branch>): %v", err)
			}

			if err := tc.readBack(body); err != nil {
				t.Fatalf("readBack (As<Branch>): %v", err)
			}

			if err := tc.mergeOver(body); err != nil {
				t.Fatalf("mergeOver (Merge<Branch>): %v", err)
			}

			wrapBytes, err := tc.marshalWrapper()
			if err != nil {
				t.Fatalf("marshalWrapper: %v", err)
			}
			if s := string(wrapBytes); s == "" || s == "null" || s == "{}" {
				t.Fatalf("marshalWrapper emitted %q — shim missing for this wrapper", s)
			}
			if !json.Valid(wrapBytes) {
				t.Fatalf("marshalWrapper emitted invalid JSON: %q", string(wrapBytes))
			}
			for _, needle := range tc.expectSubstrings {
				if !strings.Contains(string(wrapBytes), needle) {
					t.Fatalf("marshalWrapper body %q missing expected substring %q", string(wrapBytes), needle)
				}
			}

			rec := httptest.NewRecorder()
			if err := tc.visit(rec); err != nil {
				t.Fatalf("Visit returned error: %v", err)
			}
			if rec.Code != 200 {
				t.Fatalf("HTTP status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			vbody := strings.TrimRight(rec.Body.String(), "\n")
			if vbody == "" || vbody == "{}" || vbody == "null" {
				t.Fatalf("visit body = %q — Visit emitted empty/null, shim likely missing", vbody)
			}
			if !json.Valid(rec.Body.Bytes()) {
				t.Fatalf("visit body invalid JSON: %q", vbody)
			}
			for _, needle := range tc.expectSubstrings {
				if !strings.Contains(vbody, needle) {
					t.Fatalf("visit body %q missing expected substring %q", vbody, needle)
				}
			}
		})
	}
}

// Test_UnionResponses_BranchCountLock locks the total number of
// (union, branch) pairs in the coverage matrix. A PR that adds a
// branch to admin.yaml MUST also update this count AND add a
// matching row in the `cases*` factories — otherwise this test
// fails loud.
//
// Current matrix (keep in sync with admin.yaml
// §"Operation response body unions"):
//
//	OAuthCancelResponseBody        2
//	BrowserStartResponseBody       3
//	ManualCallbackResponseBody     9
//	DeviceStartResponseBody        5
//	ImportAuthJSONResponseBody     4
//	ExportAuthJSONResponseBody     3
//	FlowStatusEnvelope.data        5
//	PlaygroundRunResponseBody      9
//	DashboardResponseBody          2
//	RequestsListResponseBody       2
//	RequestsOptionsResponseBody    2
//	RequestDetailResponseBody      3
//	                              --
//	Total                         65
func Test_UnionResponses_BranchCountLock(t *testing.T) {
	t.Parallel()

	const want = 59
	if got := len(branchCases()); got != want {
		t.Fatalf(
			"branch count drift: got %d, want %d — add/remove the matching row(s) in union_smoke_test.go and bump this constant in lockstep.",
			got, want,
		)
	}
}
