package oauth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

const (
	openAIAuthBaseURL             = "https://auth.openai.com"
	openAIAuthorizeURL            = openAIAuthBaseURL + "/oauth/authorize"
	openAITokenURL                = openAIAuthBaseURL + "/oauth/token"
	openAIDeviceCodeURL           = openAIAuthBaseURL + "/api/accounts/deviceauth/usercode"
	openAIDeviceTokenURL          = openAIAuthBaseURL + "/api/accounts/deviceauth/token"
	openAIDeviceVerificationURL   = openAIAuthBaseURL + "/codex/device"
	openAIClientID                = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIOriginator              = "codex_cli_rs"
	openAIScope                   = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	openAIRedirectURI             = "http://localhost:1455/auth/callback"
	openAIAuthorizeResponseType   = "code"
	openAICodeChallengeMethodS256 = "S256"
)

type FlowMethod string

const (
	FlowBrowser FlowMethod = "browser"
	FlowDevice  FlowMethod = "device"
)

type Rail string

const (
	RailUnknown     Rail = ""
	RailLoopback    Rail = "loopback"
	RailManualPaste Rail = "manual_paste"
)

type FlowStatus string

const (
	FlowStatusIdle    FlowStatus = "idle"
	FlowStatusPending FlowStatus = "pending"
	FlowStatusSuccess FlowStatus = "success"
	FlowStatusError   FlowStatus = "error"
)

// Flow is the in-memory record of a single active OAuth flow.
type Flow struct {
	// ID backs the `flow_id` value carried on the admin APIs. The
	// data-model attribute table omits it, but plan.md and the
	// contracts rely on Flow.ID for cancel/poll/mismatch handling.
	ID string

	Method          FlowMethod
	ListenerBound   bool
	Consumed        atomic.Bool
	expiryLogged    atomic.Bool
	ConsumedBy      Rail
	State           string
	CodeVerifier    string
	DeviceAuthID    string
	UserCode        string
	VerificationURL string
	ExpiresAt       time.Time
	PollInterval    time.Duration
	CallbackServer  *http.Server
	Status          FlowStatus
	CreatedAt       time.Time

	authorizeURL     string
	callbackURL      string
	requestID        string
	reaperStop       Stopper
	pollStop         Stopper
	terminalCh       chan struct{}
	terminalOnce     sync.Once
	terminalAccount  *domain.UpstreamAccount
	terminalError    *FlowTerminalError
	terminalReported bool
	mu               sync.RWMutex
}

type Provider interface {
	BuildAuthorizeURL(state, verifier string) (string, error)
	ExchangeCode(ctx context.Context, code, verifier string) (Tokens, error)
	Refresh(ctx context.Context, refreshToken []byte) (Tokens, error)
	RequestDeviceCode(ctx context.Context) (DeviceCode, error)
	PollDeviceCode(ctx context.Context, deviceAuthID, userCode string) (Tokens, error)
}

type DeviceCode struct {
	DeviceAuthID    string
	UserCode        string
	VerificationURL string
	Interval        time.Duration
	ExpiresIn       time.Duration
}

type Tokens struct {
	AccessToken  []byte
	RefreshToken []byte
	IDToken      []byte
	ExpiresIn    time.Duration
	LastRefresh  time.Time
}

func (t Tokens) String() string {
	return "<redacted>"
}

func (t Tokens) MarshalJSON() ([]byte, error) {
	return []byte(`"<redacted>"`), nil
}

func (f *Flow) AuthorizeURL() string {
	if f == nil {
		return ""
	}
	return f.authorizeURL
}

func (f *Flow) CallbackURL() string {
	if f == nil {
		return ""
	}
	return f.callbackURL
}

func (c *Coordinator) callbackHandler(flow *Flow) http.Handler {
	return c.callbackHandlerWithConsume(flow, c.ConsumeCode)
}

func (c *Coordinator) callbackHandlerWithConsume(flow *Flow, consume consumeCodeFunc) http.Handler {
	if flow == nil {
		return http.NotFoundHandler()
	}
	if consume == nil {
		consume = c.ConsumeCode
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" {
			http.NotFound(w, r)
			return
		}

		query := r.URL.Query()
		state := query.Get("state")
		providerError := query.Get("error")
		if providerError != "" {
			if !CompareStatesConstantTime(flow.State, state) {
				c.logLoopbackRejected(r.Context(), flow, "oauth_state_mismatch")
				writeLoopbackPlain(w, http.StatusBadRequest, "authentication was rejected - state mismatch\n")
				return
			}

			if providerError == "access_denied" {
				err := c.cancelFromRail(r.Context(), flow.ID, RailLoopback, providerError)
				switch {
				case err == nil:
					writeLoopbackHTML(w, http.StatusOK, loopbackCancelPageHTML())
				case errors.Is(err, ErrAlreadyConsumed):
					writeLoopbackLostResult(r.Context(), w, flow)
				case errors.Is(err, ErrFlowExpired), errors.Is(err, ErrFlowNotFound):
					writeLoopbackPlain(w, http.StatusGone, "authentication has expired\n")
				default:
					writeLoopbackPlain(w, http.StatusBadGateway, "Authentication failed.\n")
				}
				return
			}

			c.logLoopbackRejected(r.Context(), flow, providerError)
			writeLoopbackHTML(w, http.StatusUnprocessableEntity, loopbackProviderErrorPageHTML(providerError, query.Get("error_description")))
			return
		}

		code := query.Get("code")
		if code == "" {
			writeLoopbackPlain(w, http.StatusBadRequest, "authentication was rejected - missing code\n")
			return
		}

		_, err := consume(r.Context(), flow.ID, code, state, RailLoopback)
		switch {
		case err == nil:
			writeLoopbackHTML(w, http.StatusOK, loopbackSuccessPageHTML(false))
		case errors.Is(err, ErrStateMismatch):
			writeLoopbackPlain(w, http.StatusBadRequest, "authentication was rejected - state mismatch\n")
		case errors.Is(err, ErrAlreadyConsumed):
			writeLoopbackLostResult(r.Context(), w, flow)
		case errors.Is(err, ErrFlowExpired), errors.Is(err, ErrFlowNotFound):
			writeLoopbackPlain(w, http.StatusGone, "authentication has expired\n")
		default:
			writeLoopbackPlain(w, http.StatusBadGateway, "Authentication failed.\n")
		}
	})
}

func (c *Coordinator) logLoopbackRejected(ctx context.Context, flow *Flow, errorCode string) {
	c.logger.Info("oauth_rail_rejected",
		"request_id", requestIDForFlow(ctx, flow),
		"flow_id", flow.ID,
		"method", flow.Method,
		"rail", RailLoopback,
		"error_code", errorCode,
	)
}

func writeLoopbackLostResult(ctx context.Context, w http.ResponseWriter, flow *Flow) {
	status, ok := waitForTerminalStatus(ctx, flow)
	if !ok {
		writeLoopbackPlain(w, http.StatusBadGateway, "Authentication failed.\n")
		return
	}
	if status == FlowStatusError {
		writeLoopbackHTML(w, http.StatusOK, loopbackCancelPageHTML())
		return
	}
	if status == FlowStatusSuccess {
		writeLoopbackHTML(w, http.StatusOK, loopbackSuccessPageHTML(true))
		return
	}
	writeLoopbackPlain(w, http.StatusBadGateway, "Authentication failed.\n")
}

func snapshotFlowStatus(flow *Flow) FlowStatus {
	if flow == nil {
		return FlowStatusIdle
	}
	flow.mu.RLock()
	defer flow.mu.RUnlock()
	return flow.Status
}

func waitForTerminalStatus(ctx context.Context, flow *Flow) (FlowStatus, bool) {
	status := snapshotFlowStatus(flow)
	if status == FlowStatusSuccess || status == FlowStatusError {
		return status, true
	}
	if flow == nil || flow.terminalCh == nil {
		return status, false
	}

	select {
	case <-flow.terminalCh:
		status = snapshotFlowStatus(flow)
		return status, status == FlowStatusSuccess || status == FlowStatusError
	case <-ctx.Done():
		return snapshotFlowStatus(flow), false
	}
}

func (f *Flow) signalTerminal() {
	if f == nil || f.terminalCh == nil {
		return
	}
	f.terminalOnce.Do(func() {
		close(f.terminalCh)
	})
}

func writeLoopbackHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func writeLoopbackPlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func loopbackSuccessPageHTML(lost bool) string {
	rail := "won"
	message := "Authentication completed. You can close this tab."
	if lost {
		rail = "lost"
		message = "Authentication completed in another tab. You can close this tab."
	}

	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Authentication completed</title></head><body data-rail=\"" + rail + "\" data-status=\"success\"><main><h1>Authentication completed</h1><p>" + message + "</p></main><script>window.close()</script></body></html>"
}

func loopbackCancelPageHTML() string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Authentication cancelled</title></head><body data-status=\"cancelled\"><main><h1>Authentication cancelled</h1><p>You can close this tab.</p></main><script>window.close()</script></body></html>"
}

func loopbackProviderErrorPageHTML(providerError, description string) string {
	body := "<!doctype html><html><head><meta charset=\"utf-8\"><title>Authentication rejected</title></head><body data-status=\"rejected\" data-error=\"" + html.EscapeString(providerError) + "\"><main><h1>Authentication rejected</h1><p>Provider returned error: <code>" + html.EscapeString(providerError) + "</code></p>"
	if description != "" {
		body += "<p>" + html.EscapeString(description) + "</p>"
	}
	body += "</main></body></html>"
	return body
}

type openAIProvider struct {
	httpClient     *http.Client
	logger         *slog.Logger
	redirectURI    string
	authorizeURL   string
	tokenURL       string
	deviceCodeURL  string
	deviceTokenURL string
	now            func() time.Time
}

var _ Provider = (*openAIProvider)(nil)

type OpenAIProviderConfig struct {
	HTTPClient     *http.Client
	Logger         *slog.Logger
	RedirectURI    string
	AuthorizeURL   string
	TokenURL       string
	DeviceCodeURL  string
	DeviceTokenURL string
	Now            func() time.Time
}

func NewOpenAIProvider(cfg OpenAIProviderConfig) (Provider, error) {
	if err := validateOpenAIProviderConfig(cfg); err != nil {
		return nil, err
	}
	return &openAIProvider{
		httpClient:     cfg.HTTPClient,
		logger:         cfg.Logger,
		redirectURI:    cfg.RedirectURI,
		authorizeURL:   cfg.AuthorizeURL,
		tokenURL:       cfg.TokenURL,
		deviceCodeURL:  cfg.DeviceCodeURL,
		deviceTokenURL: cfg.DeviceTokenURL,
		now:            cfg.Now,
	}, nil
}

func validateOpenAIProviderConfig(cfg OpenAIProviderConfig) error {
	if cfg.RedirectURI != "" && cfg.RedirectURI != openAIRedirectURI {
		return fmt.Errorf("oauth.NewOpenAIProvider: redirect_uri must be %q", openAIRedirectURI)
	}

	type endpointField struct {
		name  string
		value string
	}
	fields := []endpointField{
		{name: "authorize_url", value: cfg.AuthorizeURL},
		{name: "token_url", value: cfg.TokenURL},
		{name: "device_code_url", value: cfg.DeviceCodeURL},
		{name: "device_token_url", value: cfg.DeviceTokenURL},
	}

	nonEmpty := 0
	for _, field := range fields {
		if field.value != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 0 && nonEmpty != len(fields) {
		var missing []string
		for _, field := range fields {
			if field.value == "" {
				missing = append(missing, field.name)
			}
		}
		return errors.New("oauth.NewOpenAIProvider: partial endpoint override config is not allowed; missing " + strings.Join(missing, ", "))
	}

	for _, field := range fields {
		if field.value == "" {
			continue
		}
		parsed, err := url.Parse(field.value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("oauth.NewOpenAIProvider: %s must be an absolute URL", field.name)
		}
	}
	return nil
}

func (p *openAIProvider) SetRedirectURI(redirectURI string) {
	if p == nil {
		return
	}
	p.redirectURI = redirectURI
}

func (p openAIProvider) WithRedirectURI(redirectURI string) Provider {
	p.redirectURI = redirectURI
	return &p
}

func (p openAIProvider) BuildAuthorizeURL(state, verifier string) (string, error) {
	if state == "" {
		return "", errors.New("oauth.BuildAuthorizeURL: state is empty")
	}
	if verifier == "" {
		return "", errors.New("oauth.BuildAuthorizeURL: verifier is empty")
	}

	u, err := url.Parse(p.authorizeEndpoint())
	if err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("client_id", openAIClientID)
	q.Set("originator", openAIOriginator)
	q.Set("redirect_uri", p.redirectURIOrDefault())
	q.Set("scope", openAIScope)
	q.Set("response_type", openAIAuthorizeResponseType)
	q.Set("code_challenge_method", openAICodeChallengeMethodS256)
	q.Set("code_challenge", CodeChallenge(verifier))
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p openAIProvider) redirectURIOrDefault() string {
	return openAIRedirectURI
}

func (p openAIProvider) authorizeEndpoint() string {
	if p.authorizeURL != "" {
		return p.authorizeURL
	}
	return openAIAuthorizeURL
}

func (p openAIProvider) authBaseURL() string {
	type endpointCandidate struct {
		raw    string
		suffix string
	}
	for _, candidate := range []endpointCandidate{
		{raw: p.authorizeURL, suffix: "/oauth/authorize"},
		{raw: p.tokenURL, suffix: "/oauth/token"},
		{raw: p.deviceCodeURL, suffix: "/api/accounts/deviceauth/usercode"},
		{raw: p.deviceTokenURL, suffix: "/api/accounts/deviceauth/token"},
	} {
		raw := candidate.raw
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		base := parsed.Scheme + "://" + parsed.Host
		if candidate.suffix == "" {
			return base
		}
		pathPrefix := strings.TrimSuffix(parsed.EscapedPath(), candidate.suffix)
		if pathPrefix == parsed.EscapedPath() {
			return base
		}
		return strings.TrimRight(base+pathPrefix, "/")
	}
	return openAIAuthBaseURL
}

func (p openAIProvider) deviceVerificationEndpoint() string {
	return p.authBaseURL() + "/codex/device"
}
