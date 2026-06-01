package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
	"golang.org/x/sync/singleflight"
)

const browserFlowTTL = 5 * time.Minute
const maxDevicePollInterval = 60 * time.Second

var flowIDRandRead = rand.Read
var readWinnerRailYield = runtime.Gosched

type redirectURISetter interface {
	SetRedirectURI(string)
}

type redirectURICloner interface {
	WithRedirectURI(string) Provider
}

type FlowAlreadyInProgressInfo struct {
	Method    FlowMethod
	FlowID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type flowInProgressError struct {
	info FlowAlreadyInProgressInfo
}

func (e *flowInProgressError) Error() string {
	return fmt.Sprintf("oauth flow already in progress: flow_id=%s method=%s", e.info.FlowID, e.info.Method)
}

func (e *flowInProgressError) Unwrap() error {
	return ErrFlowInProgress
}

func FlowInProgressInfo(err error) (FlowAlreadyInProgressInfo, bool) {
	var inProgress *flowInProgressError
	if !errors.As(err, &inProgress) {
		return FlowAlreadyInProgressInfo{}, false
	}
	return inProgress.info, true
}

type Coordinator struct {
	flowPtr              atomic.Pointer[Flow]
	closed               atomic.Bool
	startMu              sync.Mutex
	sf                   singleflight.Group
	provider             Provider
	accounts             accountStore
	logger               *slog.Logger
	clock                Clock
	bindLoopback         func(http.Handler) (*http.Server, bool, int, error)
	callbackHandlerMaker func(*Flow) http.Handler
	releasedMu           sync.RWMutex
	releasedFlows        map[string]*Flow
	latestReleasedFlowID string
}

type consumeCodeFunc func(context.Context, string, string, string, Rail) (*domain.UpstreamAccount, error)

type accountStore interface {
	InsertUpstreamAccount(ctx context.Context, account *domain.UpstreamAccount) (int64, error)
	GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
	UpdateCredentials(ctx context.Context, id int64, patch store.CredentialPatch) error
	UpdateStatusIfCurrent(ctx context.Context, id int64, status string, expectedLastRefresh time.Time) error
	UpdateUsage(ctx context.Context, id int64, primary, secondary *float64) error
	ListActive(ctx context.Context) ([]domain.UpstreamAccount, error)
}

func NewCoordinator(provider Provider, logger *slog.Logger) *Coordinator {
	return NewCoordinatorWithClock(realClock{}, provider, logger)
}

func NewCoordinatorWithClock(clock Clock, provider Provider, logger *slog.Logger) *Coordinator {
	if clock == nil {
		clock = realClock{}
	}
	if provider == nil {
		provider = &openAIProvider{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := &Coordinator{
		provider:      provider,
		logger:        logger.With("component", "oauth"),
		clock:         clock,
		bindLoopback:  BindLoopback,
		releasedFlows: make(map[string]*Flow),
	}
	c.callbackHandlerMaker = c.callbackHandler
	return c
}

func (c *Coordinator) SetCallbackHandler(factory func(*Flow) http.Handler) {
	if factory == nil {
		c.callbackHandlerMaker = c.callbackHandler
		return
	}
	c.callbackHandlerMaker = factory
}

func (c *Coordinator) SetAccountStore(accounts accountStore) {
	c.accounts = accounts
}

func (c *Coordinator) StartBrowser(ctx context.Context, provider string) (*Flow, error) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.closed.Load() {
		return nil, errors.New("oauth.StartBrowser: coordinator is closed")
	}
	if current := c.flowPtr.Load(); current != nil {
		return nil, flowInProgressFromCurrent(current)
	}

	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("oauth.StartBrowser: generate state: %w", err)
	}

	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("oauth.StartBrowser: generate verifier: %w", err)
	}

	flowID, err := generateFlowID()
	if err != nil {
		return nil, fmt.Errorf("oauth.StartBrowser: generate flow id: %w", err)
	}

	now := c.clock.Now()
	reqID := requestid.FromContext(ctx)
	flow := &Flow{
		ID:           flowID,
		Method:       FlowBrowser,
		ConsumedBy:   RailUnknown,
		State:        state,
		CodeVerifier: verifier,
		ExpiresAt:    now.Add(browserFlowTTL),
		Status:       FlowStatusPending,
		CreatedAt:    now,
		requestID:    reqID,
		terminalCh:   make(chan struct{}),
	}

	srv, bound, port, err := c.bindLoopback(c.callbackHandlerMaker(flow))
	if err != nil {
		return nil, fmt.Errorf("oauth.StartBrowser: bind loopback: %w", err)
	}
	flow.ListenerBound = bound
	flow.CallbackServer = srv
	flow.callbackURL = loopbackCallbackURL(port)

	flow.authorizeURL, err = c.buildAuthorizeURLForFlow(flow, state, verifier)
	if err != nil {
		if flow.ListenerBound && flow.CallbackServer != nil {
			_ = flow.CallbackServer.Close()
		}
		return nil, fmt.Errorf("oauth.StartBrowser: build authorize URL: %w", err)
	}

	if err := c.TryStartFlow(flow); err != nil {
		if flow.ListenerBound && flow.CallbackServer != nil {
			_ = flow.CallbackServer.Close()
		}
		return nil, err
	}
	if err := c.setProviderRedirectURI(flow.callbackURL); err != nil {
		c.ReleaseFlow(flow.ID)
		return nil, err
	}

	c.startExpiryReaper(flow)
	c.logger.Info("oauth_flow_started",
		"request_id", reqID,
		"flow_id", flow.ID,
		"method", flow.Method,
		"provider", provider,
		"expires_at", flow.ExpiresAt,
		"listener_bound", flow.ListenerBound,
	)
	return flow, nil
}

func (c *Coordinator) StartDevice(ctx context.Context, provider string) (*Flow, error) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.closed.Load() {
		return nil, errors.New("oauth.StartDevice: coordinator is closed")
	}
	if current := c.flowPtr.Load(); current != nil {
		return nil, flowInProgressFromCurrent(current)
	}

	deviceCode, err := c.provider.RequestDeviceCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("oauth.StartDevice: request device code: %w", err)
	}
	if deviceCode.DeviceAuthID == "" {
		return nil, errors.New("oauth.StartDevice: deviceAuthID is empty")
	}
	if deviceCode.UserCode == "" {
		return nil, errors.New("oauth.StartDevice: userCode is empty")
	}

	flowID, err := generateFlowID()
	if err != nil {
		return nil, fmt.Errorf("oauth.StartDevice: generate flow id: %w", err)
	}

	now := c.clock.Now()
	reqID := requestid.FromContext(ctx)
	interval := deviceCode.Interval
	if interval < 0 {
		return nil, errors.New("oauth.StartDevice: interval must be non-negative")
	}
	if interval == 0 {
		interval = defaultDevicePollInterval
	}
	expiresIn := deviceCode.ExpiresIn
	if expiresIn <= 0 {
		return nil, errors.New("oauth.StartDevice: expiresIn must be positive")
	}
	verificationURL := deviceCode.VerificationURL
	if verificationURL == "" {
		return nil, errors.New("oauth.StartDevice: verificationURL is empty")
	}

	flow := &Flow{
		ID:              flowID,
		Method:          FlowDevice,
		ConsumedBy:      RailUnknown,
		DeviceAuthID:    deviceCode.DeviceAuthID,
		UserCode:        deviceCode.UserCode,
		VerificationURL: verificationURL,
		ExpiresAt:       now.Add(expiresIn),
		PollInterval:    interval,
		Status:          FlowStatusPending,
		CreatedAt:       now,
		requestID:       reqID,
		terminalCh:      make(chan struct{}),
	}

	if err := c.TryStartFlow(flow); err != nil {
		return nil, err
	}
	c.startExpiryReaper(flow)
	c.logger.Info("oauth_flow_started",
		"request_id", reqID,
		"flow_id", flow.ID,
		"method", flow.Method,
		"provider", provider,
		"expires_at", flow.ExpiresAt,
	)
	c.startDevicePoller(flow)
	return flow, nil
}

func (c *Coordinator) TryStartFlow(flow *Flow) error {
	if flow == nil {
		return errors.New("oauth.TryStartFlow: flow is nil")
	}
	for {
		if c.flowPtr.CompareAndSwap(nil, flow) {
			c.cleanupReleasedFlowsOnStart()
			return nil
		}

		current := c.flowPtr.Load()
		if current == nil {
			runtime.Gosched()
			continue
		}
		return &flowInProgressError{
			info: FlowAlreadyInProgressInfo{
				Method:    current.Method,
				FlowID:    current.ID,
				ExpiresAt: current.ExpiresAt,
				CreatedAt: current.CreatedAt,
			},
		}
	}
}

func (c *Coordinator) CurrentFlow() *Flow {
	return c.flowPtr.Load()
}

func (c *Coordinator) Now() time.Time {
	return c.clock.Now()
}

func (c *Coordinator) ReleaseFlow(flowID string) {
	current := c.flowPtr.Load()
	if current == nil || current.ID != flowID {
		return
	}
	if !c.closed.Load() {
		c.storeReleasedFlow(current)
	}
	if !c.flowPtr.CompareAndSwap(current, nil) {
		return
	}

	if current.reaperStop != nil {
		current.reaperStop.Stop()
	}
	if current.pollStop != nil {
		current.pollStop.Stop()
	}
	if current.ListenerBound && current.CallbackServer != nil {
		go func(flow *Flow, srv *http.Server) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
			flow.mu.Lock()
			flow.CallbackServer = nil
			flow.ListenerBound = false
			flow.mu.Unlock()
		}(current, current.CallbackServer)
		return
	}
}

func (c *Coordinator) Shutdown() {
	if c == nil {
		return
	}
	c.closed.Store(true)
	if flow := c.flowPtr.Load(); flow != nil {
		c.ReleaseFlow(flow.ID)
	}
	c.releasedMu.Lock()
	clear(c.releasedFlows)
	c.latestReleasedFlowID = ""
	c.releasedMu.Unlock()
}

func (c *Coordinator) Cancel(flowID string) error {
	return c.cancel(context.Background(), flowID, "", false)
}

func (c *Coordinator) CancelWithContext(ctx context.Context, flowID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.cancel(ctx, flowID, requestid.FromContext(ctx), true)
}

func (c *Coordinator) CancelFromProviderError(ctx context.Context, flowID string, rail Rail, providerError string) error {
	return c.cancelFromRail(ctx, flowID, rail, providerError)
}

func (c *Coordinator) cancelFromRail(ctx context.Context, flowID string, rail Rail, providerError string) error {
	if providerError != "access_denied" {
		return fmt.Errorf("oauth.cancelFromRail: provider error %q cannot cancel flow", providerError)
	}

	flow := c.lookupFlow(flowID)
	if flow == nil {
		return ErrFlowNotFound
	}
	if flow.Consumed.Load() {
		return c.classifyConsumedFlow(ctx, flow, rail)
	}
	if c.clock.Now().After(flow.ExpiresAt) {
		c.logFlowExpiredIfActive(ctx, flow)
		return ErrFlowExpired
	}

	flow.mu.Lock()
	if !flow.Consumed.CompareAndSwap(false, true) {
		flow.mu.Unlock()
		if err := c.rejectConsumedRail(ctx, flow, rail); err != nil {
			return err
		}
		return ErrAlreadyConsumed
	}
	if flow.ConsumedBy != RailUnknown {
		flow.mu.Unlock()
		return c.failFlow(ctx, flow, rail, fmt.Errorf("oauth.cancelFromRail: consumed winner rail already published: %q", flow.ConsumedBy))
	}
	flow.ConsumedBy = rail
	flow.Status = FlowStatusError
	flow.terminalError = accessDeniedFlowError()
	flow.mu.Unlock()
	flow.signalTerminal()

	c.logger.Info("oauth_flow_cancelled",
		"request_id", requestIDForFlow(ctx, flow),
		"flow_id", flow.ID,
		"method", flow.Method,
		"rail", rail,
	)
	c.ReleaseFlow(flow.ID)
	return nil
}

func (c *Coordinator) WinnerRail(flowID string) (Rail, bool) {
	flow := c.lookupFlow(flowID)
	if flow == nil {
		return RailUnknown, false
	}
	winner, err := readWinnerRail(flow)
	if err != nil {
		return RailUnknown, false
	}
	return winner, true
}

func (c *Coordinator) cancel(ctx context.Context, flowID, requestID string, waitForConsumedTerminal bool) error {
	current := c.flowPtr.Load()
	if current == nil || current.ID != flowID {
		return ErrFlowNotFound
	}
	if !current.Consumed.CompareAndSwap(false, true) {
		if !waitForConsumedTerminal {
			return nil
		}
		if status, ok := waitForTerminalStatus(ctx, current); ok && (status == FlowStatusSuccess || status == FlowStatusError) {
			return nil
		}
		return fmt.Errorf("oauth.CancelWithContext: flow %q completion did not reach terminal status before context ended", flowID)
	}

	current.mu.Lock()
	current.Status = FlowStatusError
	current.terminalError = cancelledFlowError()
	current.mu.Unlock()
	current.signalTerminal()

	if requestID == "" {
		requestID = current.requestID
	}
	c.logger.Info("oauth_flow_cancelled",
		"request_id", requestID,
		"flow_id", current.ID,
		"method", current.Method,
	)
	c.ReleaseFlow(flowID)
	return nil
}

func (c *Coordinator) startExpiryReaper(flow *Flow) {
	if flow == nil {
		return
	}

	delay := flow.ExpiresAt.Sub(c.clock.Now())
	if delay < 0 {
		delay = 0
	}
	flow.reaperStop = c.clock.AfterFunc(delay, func() {
		if c.closed.Load() {
			return
		}
		if !flow.Consumed.CompareAndSwap(false, true) {
			return
		}

		flow.mu.Lock()
		flow.Status = FlowStatusError
		if flow.Method == FlowDevice {
			flow.terminalError = deviceExpiredFlowError()
		} else {
			flow.terminalError = browserExpiredFlowError()
		}
		flow.mu.Unlock()
		flow.signalTerminal()

		if flow.expiryLogged.CompareAndSwap(false, true) {
			c.logFlowExpired(flow.requestID, flow)
		}
		c.ReleaseFlow(flow.ID)
	})
}

func (c *Coordinator) setProviderRedirectURI(callbackURL string) error {
	if provider, ok := c.provider.(redirectURISetter); ok {
		provider.SetRedirectURI(callbackURL)
		return nil
	}
	if callbackURL != openAIRedirectURI {
		return fmt.Errorf("oauth.StartBrowser: provider does not support redirect override %q", callbackURL)
	}
	return nil
}

func (c *Coordinator) buildAuthorizeURLForFlow(flow *Flow, state, verifier string) (string, error) {
	if provider, ok := c.provider.(redirectURICloner); ok {
		return provider.WithRedirectURI(flow.callbackURL).BuildAuthorizeURL(state, verifier)
	}
	if flow.callbackURL != openAIRedirectURI {
		return "", fmt.Errorf("oauth.StartBrowser: provider does not support redirect override %q", flow.callbackURL)
	}
	return c.provider.BuildAuthorizeURL(state, verifier)
}

func loopbackCallbackURL(port int) string {
	if port < loopbackPortMin || port > loopbackPortMax {
		return openAIRedirectURI
	}
	return fmt.Sprintf("http://localhost:%d/auth/callback", port)
}

func generateFlowID() (string, error) {
	buf := make([]byte, 16)
	n, err := flowIDRandRead(buf)
	if err != nil {
		return "", fmt.Errorf("read flow id bytes: %w", err)
	}
	if n != len(buf) {
		return "", fmt.Errorf("read flow id bytes: short read %d/%d", n, len(buf))
	}
	return "fl_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func (c *Coordinator) ConsumeCode(ctx context.Context, flowID, code, state string, rail Rail) (*domain.UpstreamAccount, error) {
	if c.closed.Load() {
		return nil, ErrFlowNotFound
	}
	flow := c.lookupFlow(flowID)
	if flow == nil {
		return nil, ErrFlowNotFound
	}
	if flow.Consumed.Load() {
		if !CompareStatesConstantTime(flow.State, state) {
			c.logger.Info("oauth_rail_rejected",
				"request_id", requestIDForFlow(ctx, flow),
				"flow_id", flow.ID,
				"method", flow.Method,
				"rail", rail,
				"error_code", "oauth_state_mismatch",
			)
			return nil, ErrStateMismatch
		}
		if err := c.classifyConsumedFlow(ctx, flow, rail); err != nil {
			return nil, err
		}
		return nil, ErrAlreadyConsumed
	}
	if c.clock.Now().After(flow.ExpiresAt) {
		c.logFlowExpiredIfActive(ctx, flow)
		return nil, ErrFlowExpired
	}
	if !CompareStatesConstantTime(flow.State, state) {
		c.logger.Info("oauth_rail_rejected",
			"request_id", requestIDForFlow(ctx, flow),
			"flow_id", flow.ID,
			"method", flow.Method,
			"rail", rail,
			"error_code", "oauth_state_mismatch",
		)
		return nil, ErrStateMismatch
	}
	flow.mu.Lock()
	if !flow.Consumed.CompareAndSwap(false, true) {
		flow.mu.Unlock()
		if err := c.rejectConsumedRail(ctx, flow, rail); err != nil {
			return nil, err
		}
		return nil, ErrAlreadyConsumed
	}
	if flow.ConsumedBy != RailUnknown {
		flow.mu.Unlock()
		return nil, c.failFlow(ctx, flow, rail, fmt.Errorf("oauth.ConsumeCode: consumed winner rail already published: %q", flow.ConsumedBy))
	}
	flow.ConsumedBy = rail
	flow.mu.Unlock()

	tokens, err := c.provider.ExchangeCode(ctx, code, flow.CodeVerifier)
	if err != nil {
		return nil, c.failFlow(ctx, flow, rail, err)
	}

	claims, err := ExtractClaims(tokens.IDToken)
	if err != nil {
		return nil, c.failFlow(ctx, flow, rail, &TokenExchangeError{
			code:    "invalid_response",
			message: err.Error(),
		})
	}

	account, err := c.persistConsumedFlow(ctx, flow, tokens, claims)
	if err != nil {
		return nil, c.failFlow(ctx, flow, rail, err)
	}

	reqID := requestIDForFlow(ctx, flow)
	flow.mu.Lock()
	flow.Status = FlowStatusSuccess
	flow.terminalAccount = cloneAccountForFlowSnapshot(account)
	flow.signalTerminal()
	c.logger.Info("oauth_flow_completed",
		"request_id", reqID,
		"flow_id", flow.ID,
		"method", flow.Method,
		"provider", domain.ProviderOpenAI,
		"account_id", account.ID,
		"email", stringValue(account.Email),
		"plan_type", stringValue(account.PlanType),
		"rail", rail,
	)
	c.ReleaseFlow(flow.ID)
	flow.mu.Unlock()
	return account, nil
}

func (c *Coordinator) startDevicePoller(flow *Flow) {
	if flow == nil {
		return
	}
	c.scheduleDevicePoll(flow, flow.PollInterval)
}

func (c *Coordinator) scheduleDevicePoll(flow *Flow, interval time.Duration) {
	if flow == nil {
		return
	}
	if interval <= 0 {
		interval = defaultDevicePollInterval
	}
	flow.mu.Lock()
	flow.PollInterval = interval
	flow.mu.Unlock()

	ctx := requestid.WithContext(context.Background(), flow.requestID)
	stopper := c.clock.AfterFunc(interval, func() {
		if c.closed.Load() || flow.Consumed.Load() || !c.clock.Now().Before(flow.ExpiresAt) {
			return
		}

		tokens, err := c.provider.PollDeviceCode(ctx, flow.DeviceAuthID, flow.UserCode)
		if err != nil {
			if next, pending := nextDevicePollInterval(interval, err); pending {
				c.scheduleDevicePoll(flow, next)
				return
			}
			_ = c.finishDeviceFailure(ctx, flow, err)
			return
		}
		if !flow.Consumed.CompareAndSwap(false, true) {
			return
		}
		c.finishDeviceSuccess(ctx, flow, tokens)
	})
	flow.mu.Lock()
	flow.pollStop = stopper
	flow.mu.Unlock()
}

func (c *Coordinator) finishDeviceSuccess(ctx context.Context, flow *Flow, tokens Tokens) {
	if c.closed.Load() {
		c.ReleaseFlow(flow.ID)
		return
	}
	claims, err := ExtractClaims(tokens.IDToken)
	if err != nil {
		_ = c.failFlow(ctx, flow, RailUnknown, &TokenExchangeError{
			code:    "invalid_response",
			message: err.Error(),
		})
		return
	}

	account, err := c.persistConsumedFlow(ctx, flow, tokens, claims)
	if err != nil {
		_ = c.failFlow(ctx, flow, RailUnknown, err)
		return
	}

	reqID := requestIDForFlow(ctx, flow)
	flow.mu.Lock()
	flow.Status = FlowStatusSuccess
	flow.terminalAccount = cloneAccountForFlowSnapshot(account)
	flow.mu.Unlock()
	flow.signalTerminal()

	c.logger.Info("oauth_flow_completed",
		"request_id", reqID,
		"flow_id", flow.ID,
		"method", flow.Method,
		"provider", domain.ProviderOpenAI,
		"account_id", account.ID,
		"email", stringValue(account.Email),
		"plan_type", stringValue(account.PlanType),
	)
	c.ReleaseFlow(flow.ID)
}

func (c *Coordinator) finishDeviceFailure(ctx context.Context, flow *Flow, err error) error {
	if c.closed.Load() {
		c.ReleaseFlow(flow.ID)
		return err
	}
	var exchangeErr *TokenExchangeError
	if errors.As(err, &exchangeErr) {
		switch exchangeErr.Code() {
		case "access_denied":
			if !flow.Consumed.CompareAndSwap(false, true) {
				return err
			}
			flow.mu.Lock()
			flow.Status = FlowStatusError
			flow.terminalError = accessDeniedFlowError()
			flow.mu.Unlock()
			flow.signalTerminal()
			c.logger.Info("oauth_flow_cancelled",
				"request_id", requestIDForFlow(ctx, flow),
				"flow_id", flow.ID,
				"method", flow.Method,
			)
			c.ReleaseFlow(flow.ID)
			return err
		case "expired_token":
			if !flow.Consumed.CompareAndSwap(false, true) {
				return err
			}
			flow.mu.Lock()
			flow.Status = FlowStatusError
			flow.terminalError = deviceExpiredFlowError()
			flow.mu.Unlock()
			flow.signalTerminal()
			if flow.expiryLogged.CompareAndSwap(false, true) {
				c.logFlowExpired(requestIDForFlow(ctx, flow), flow)
			}
			c.ReleaseFlow(flow.ID)
			return err
		}
	}

	if !flow.Consumed.CompareAndSwap(false, true) {
		return err
	}
	return c.failFlow(ctx, flow, RailUnknown, err)
}

func nextDevicePollInterval(current time.Duration, err error) (time.Duration, bool) {
	var exchangeErr *TokenExchangeError
	if !errors.As(err, &exchangeErr) {
		return current, false
	}
	switch exchangeErr.Code() {
	case "authorization_pending":
		return current, true
	case "slow_down":
		next := current * 2
		if next <= 0 {
			next = current
		}
		if next < defaultDevicePollInterval {
			next = defaultDevicePollInterval
		}
		if next > maxDevicePollInterval {
			next = maxDevicePollInterval
		}
		return next, true
	}
	if isTransientDevicePollTransport(exchangeErr) {
		return current, true
	}
	return current, false
}

func isTransientDevicePollTransport(err *TokenExchangeError) bool {
	if err == nil {
		return false
	}
	switch err.Code() {
	case "http_403":
		return err.HTTPStatus() == http.StatusForbidden
	case "http_404":
		return err.HTTPStatus() == http.StatusNotFound
	default:
		return false
	}
}

func flowInProgressFromCurrent(current *Flow) error {
	if current == nil {
		return ErrFlowInProgress
	}
	return &flowInProgressError{
		info: FlowAlreadyInProgressInfo{
			Method:    current.Method,
			FlowID:    current.ID,
			ExpiresAt: current.ExpiresAt,
			CreatedAt: current.CreatedAt,
		},
	}
}

func (c *Coordinator) rejectConsumedRail(ctx context.Context, flow *Flow, rail Rail) error {
	winnerRail, err := readWinnerRail(flow)
	if err != nil {
		flow.mu.RLock()
		status := flow.Status
		flow.mu.RUnlock()
		if status == FlowStatusError {
			reqID := requestIDForFlow(ctx, flow)
			if rail == RailLoopback {
				c.logger.Debug("oauth_rail_rejected",
					"request_id", reqID,
					"flow_id", flow.ID,
					"method", flow.Method,
					"rail", rail,
					"error_code", "already_consumed",
				)
				return nil
			}
			c.logger.Info("oauth_rail_rejected",
				"request_id", reqID,
				"flow_id", flow.ID,
				"method", flow.Method,
				"rail", rail,
				"error_code", "already_consumed",
			)
			return nil
		}
		return err
	}

	reqID := requestIDForFlow(ctx, flow)
	if rail == RailLoopback {
		c.logger.Debug("oauth_rail_rejected",
			"request_id", reqID,
			"flow_id", flow.ID,
			"method", flow.Method,
			"rail", rail,
			"error_code", "already_consumed",
			"winner_rail", winnerRail,
		)
		return nil
	}

	c.logger.Info("oauth_rail_rejected",
		"request_id", reqID,
		"flow_id", flow.ID,
		"method", flow.Method,
		"rail", rail,
		"error_code", "already_consumed",
		"winner_rail", winnerRail,
	)
	return nil
}

func readWinnerRail(flow *Flow) (Rail, error) {
	for attempt := 0; attempt < 2; attempt++ {
		flow.mu.RLock()
		winner := flow.ConsumedBy
		flow.mu.RUnlock()
		if winner != RailUnknown {
			return winner, nil
		}
		if attempt == 0 {
			readWinnerRailYield()
		}
	}
	return RailUnknown, errors.New("oauth.ConsumeCode: consumed flow winner rail not published")
}

func (c *Coordinator) failFlow(ctx context.Context, flow *Flow, rail Rail, err error) error {
	errorCode, errorMessage := oauthFailureDetails(err)
	reqID := requestIDForFlow(ctx, flow)

	flow.mu.Lock()
	flow.Status = FlowStatusError
	flow.terminalError = &FlowTerminalError{
		Code:    errorCode,
		Message: errorMessage,
	}
	flow.mu.Unlock()
	flow.signalTerminal()

	attrs := []any{
		"request_id", reqID,
		"flow_id", flow.ID,
		"method", flow.Method,
		"error_code", errorCode,
		"error_message", errorMessage,
	}
	if flow.Method == FlowBrowser {
		attrs = append(attrs, "rail", rail)
	}
	c.logger.Info("oauth_flow_failed", attrs...)
	c.ReleaseFlow(flow.ID)
	return err
}

func (c *Coordinator) lookupFlow(flowID string) *Flow {
	if c.closed.Load() {
		return nil
	}
	current := c.flowPtr.Load()
	if current != nil && current.ID == flowID {
		return current
	}
	return c.loadReleasedFlow(flowID)
}

func (c *Coordinator) persistConsumedFlow(ctx context.Context, flow *Flow, tokens Tokens, claims Claims) (*domain.UpstreamAccount, error) {
	if c.accounts == nil {
		return nil, errors.New("oauth.ConsumeCode: account store is nil")
	}

	if tokens.LastRefresh.IsZero() {
		return nil, &TokenExchangeError{
			code:    "invalid_response",
			message: "missing last_refresh",
		}
	}
	lastRefresh := tokens.LastRefresh.UTC()
	accessExpiresAt := lastRefresh.Add(tokens.ExpiresIn)
	email := stringPointer(claims.Email)
	planType := stringPointer(claims.PlanType)
	chatGPTAccountID := stringPointer(claims.ChatGPTAccountID)

	name, err := deriveOAuthAccountName(claims)
	if err != nil {
		return nil, &TokenExchangeError{
			code:    "invalid_response",
			message: err.Error(),
		}
	}

	baseURL := domain.ProviderDefaultURLs[domain.ProviderOpenAI]
	account := &domain.UpstreamAccount{
		Name:             name,
		Provider:         domain.ProviderOpenAI,
		BaseURL:          &baseURL,
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      cloneBytes(tokens.AccessToken),
		RefreshToken:     cloneBytes(tokens.RefreshToken),
		IDToken:          cloneBytes(tokens.IDToken),
		LastRefresh:      &lastRefresh,
		AccessExpiresAt:  &accessExpiresAt,
		Email:            email,
		PlanType:         planType,
		ChatGPTAccountID: chatGPTAccountID,
	}
	if flow.Method == FlowDevice {
		account.AuthMethod = domain.AuthMethodOAuthDevice
	}
	id, err := c.accounts.InsertUpstreamAccount(ctx, account)
	if err != nil {
		return nil, &StoreError{Op: "insert account", Err: err}
	}
	account.ID = id
	return account, nil
}

type refreshResult struct {
	accessToken  []byte
	usedFallback bool
}

func (c *Coordinator) RefreshIfStale(ctx context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
	for attempt := 0; ; attempt++ {
		accessToken, usedFallback, err := c.refreshIfStaleOnce(ctx, acct)
		if !errors.Is(err, errConcurrentRefreshConflict) {
			return accessToken, usedFallback, err
		}
		if attempt > 0 {
			return nil, false, errConcurrentRefreshConflict
		}
		acct, err = c.reloadRefreshConflictAccount(ctx, acct.ID)
		if err != nil {
			return nil, false, err
		}
	}
}

func (c *Coordinator) refreshIfStaleOnce(ctx context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
	if acct == nil {
		return nil, false, errors.New("oauth.RefreshIfStale: account is nil")
	}
	if acct.ID <= 0 {
		return nil, false, fmt.Errorf("oauth.RefreshIfStale: invalid account id %d", acct.ID)
	}

	method := acct.AuthMethod
	switch method {
	case domain.AuthMethodAPIKey:
		if acct.APIKey == "" {
			return nil, false, fmt.Errorf("oauth.RefreshIfStale: api_key account %d has empty api_key: %w", acct.ID, domain.ErrInvalidAccountShape)
		}
		return []byte(acct.APIKey), false, nil
	case domain.AuthMethodOAuthBrowser, domain.AuthMethodOAuthDevice, domain.AuthMethodOAuthImport:
	default:
		return nil, false, fmt.Errorf("oauth.RefreshIfStale: account %d has unknown auth_method %q: %w", acct.ID, acct.AuthMethod, domain.ErrUnknownAuthMethod)
	}

	if err := acct.Validate(); err != nil {
		return nil, false, fmt.Errorf("oauth.RefreshIfStale: validate account %d: %w", acct.ID, err)
	}

	lastRefresh := acct.LastRefresh.UTC()
	accessExpiresAt := acct.AccessExpiresAt.UTC()
	if accessExpiresAt.Before(lastRefresh) {
		return nil, false, fmt.Errorf(
			"oauth.RefreshIfStale: account %d access_expires_at %s precedes last_refresh %s: %w",
			acct.ID, accessExpiresAt.Format(time.RFC3339Nano), lastRefresh.Format(time.RFC3339Nano), domain.ErrInvalidAccountShape,
		)
	}

	expiresIn := accessExpiresAt.Sub(lastRefresh)
	refreshAfter := lastRefresh.Add(expiresIn / 2)
	if !c.clock.Now().After(refreshAfter) {
		return cloneBytes(acct.AccessToken), false, nil
	}
	if c.accounts == nil {
		return nil, false, errors.New("oauth.RefreshIfStale: account store is nil")
	}

	providerName := acct.Provider
	if providerName == "" {
		providerName = domain.ProviderOpenAI
	}

	resultAny, err, _ := c.sf.Do(fmt.Sprintf("%d", acct.ID), func() (result any, err error) {
		reqID := requestid.FromContext(ctx)
		start := c.clock.Now()
		c.logger.Info("oauth_refresh_started",
			"request_id", reqID,
			"account_id", acct.ID,
			"provider", providerName,
			"last_refresh", lastRefresh,
		)

		defer func() {
			if recovered := recover(); recovered != nil {
				result = nil
				err = fmt.Errorf("oauth.RefreshIfStale: panic: %v", recovered)
			}
		}()

		tokens, err := c.provider.Refresh(ctx, cloneBytes(acct.RefreshToken))
		if err != nil {
			return c.handleRefreshFailure(ctx, acct, providerName, lastRefresh, err)
		}

		claims, err := ExtractClaims(tokens.IDToken)
		if err != nil {
			return c.handleRefreshFailure(ctx, acct, providerName, lastRefresh, &TokenExchangeError{
				code:    "invalid_response",
				message: err.Error(),
			})
		}

		refreshedAt := c.clock.Now().UTC()
		newExpiresAt := refreshedAt.Add(tokens.ExpiresIn)
		patch := store.CredentialPatch{
			AuthMethod:          method,
			AccessToken:         cloneBytes(tokens.AccessToken),
			RefreshToken:        cloneBytes(tokens.RefreshToken),
			IDToken:             cloneBytes(tokens.IDToken),
			LastRefresh:         &refreshedAt,
			ExpectedLastRefresh: &lastRefresh,
			AccessExpiresAt:     &newExpiresAt,
			Email:               stringPointer(claims.Email),
			PlanType:            stringPointer(claims.PlanType),
			ChatGPTAccountID:    stringPointer(claims.ChatGPTAccountID),
		}
		if err := c.accounts.UpdateCredentials(ctx, acct.ID, patch); err != nil {
			if errors.Is(err, store.ErrConditionalUpdateConflict) {
				return nil, errConcurrentRefreshConflict
			}
			return nil, &StoreError{Op: "update credentials", Err: err}
		}

		c.logger.Info("oauth_refresh_ok",
			"request_id", reqID,
			"account_id", acct.ID,
			"provider", providerName,
			"refresh_elapsed_ms", c.clock.Since(start).Milliseconds(),
		)
		return refreshResult{
			accessToken:  cloneBytes(tokens.AccessToken),
			usedFallback: false,
		}, nil
	})
	if err != nil {
		return nil, false, err
	}

	result, ok := resultAny.(refreshResult)
	if !ok {
		return nil, false, fmt.Errorf("oauth.RefreshIfStale: unexpected singleflight result type %T", resultAny)
	}
	return cloneBytes(result.accessToken), result.usedFallback, nil
}

func deriveOAuthAccountName(claims Claims) (string, error) {
	if claims.Email != "" {
		return claims.Email, nil
	}
	if claims.ChatGPTAccountID != "" {
		return claims.ChatGPTAccountID, nil
	}
	return "", errors.New("missing email and chatgpt_account_id")
}

func oauthFailureDetails(err error) (string, string) {
	var exchangeErr *TokenExchangeError
	if errors.As(err, &exchangeErr) {
		return exchangeErr.Code(), exchangeErr.Message()
	}
	var storeErr *StoreError
	if errors.As(err, &storeErr) {
		return "oauth_store_failed", storeErr.Error()
	}
	return "oauth_internal_error", err.Error()
}

func (c *Coordinator) handleRefreshFailure(ctx context.Context, acct *domain.UpstreamAccount, provider string, expectedLastRefresh time.Time, err error) (any, error) {
	errorCode, permanent := refreshErrorCode(err)
	if permanent {
		if err := c.accounts.UpdateStatusIfCurrent(ctx, acct.ID, domain.AccountStatusDisabled, expectedLastRefresh); err != nil {
			if errors.Is(err, store.ErrConditionalUpdateConflict) {
				return nil, errConcurrentRefreshConflict
			}
			return nil, &StoreError{Op: "disable account", Err: err}
		}
		c.logger.Warn("oauth_refresh_failed",
			"request_id", requestid.FromContext(ctx),
			"account_id", acct.ID,
			"provider", provider,
			"error_code", errorCode,
		)
		return nil, err
	}
	return c.refreshTransientFallbackResult(ctx, acct, provider, err)
}

func (c *Coordinator) reloadRefreshConflictAccount(ctx context.Context, accountID int64) (*domain.UpstreamAccount, error) {
	if c.accounts == nil {
		return nil, errors.New("oauth.RefreshIfStale: account store is nil")
	}
	latest, err := c.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("oauth.RefreshIfStale: get account %d: %w", accountID, err)
	}
	provider := latest.Provider
	if provider == "" {
		provider = domain.ProviderOpenAI
	}
	c.logger.Info("oauth_refresh_conflict_retry",
		"request_id", requestid.FromContext(ctx),
		"account_id", accountID,
		"provider", provider,
	)
	if latest.Status != domain.AccountStatusActive {
		return nil, fmt.Errorf("oauth.RefreshIfStale: account %d status %q is not active", accountID, latest.Status)
	}
	return latest, nil
}

func (c *Coordinator) refreshTransientFallbackResult(ctx context.Context, acct *domain.UpstreamAccount, provider string, err error) (refreshResult, error) {
	errorCode, _ := refreshErrorCode(err)
	lastRefresh := acct.LastRefresh.UTC()
	c.logger.Warn("oauth_refresh_transient_fallback",
		"request_id", requestid.FromContext(ctx),
		"account_id", acct.ID,
		"provider", provider,
		"error_code", errorCode,
		"last_refresh_age_seconds", c.clock.Since(lastRefresh).Seconds(),
	)
	return refreshResult{
		accessToken:  cloneBytes(acct.AccessToken),
		usedFallback: true,
	}, nil
}

func refreshErrorCode(err error) (code string, permanent bool) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "refresh_timeout", false
	case errors.Is(err, context.Canceled):
		return "refresh_cancelled", false
	}

	var exchangeErr *TokenExchangeError
	if errors.As(err, &exchangeErr) {
		switch exchangeErr.Code() {
		case "invalid_grant", "account_deactivated", "invalid_client":
			return exchangeErr.Code(), true
		default:
			return exchangeErr.Code(), false
		}
	}

	return "oauth_internal_error", false
}

func requestIDForFlow(ctx context.Context, flow *Flow) string {
	reqID := requestid.FromContext(ctx)
	if reqID != "" {
		return reqID
	}
	if flow != nil {
		return flow.requestID
	}
	return ""
}

func (c *Coordinator) classifyConsumedFlow(ctx context.Context, flow *Flow, rail Rail) error {
	if flow == nil {
		return ErrFlowNotFound
	}
	flow.mu.RLock()
	status := flow.Status
	winner := flow.ConsumedBy
	flow.mu.RUnlock()
	if winner != RailUnknown || status == FlowStatusSuccess || !c.clock.Now().After(flow.ExpiresAt) {
		if err := c.rejectConsumedRail(ctx, flow, rail); err != nil {
			return err
		}
		return ErrAlreadyConsumed
	}
	c.logFlowExpiredIfActive(ctx, flow)
	return ErrFlowExpired
}

func (c *Coordinator) logFlowExpiredIfActive(ctx context.Context, flow *Flow) {
	if flow == nil {
		return
	}
	current := c.flowPtr.Load()
	if current == nil || current.ID != flow.ID {
		return
	}
	if !flow.expiryLogged.CompareAndSwap(false, true) {
		return
	}
	c.logFlowExpired(requestIDForFlow(ctx, flow), flow)
}

func (c *Coordinator) logFlowExpired(requestID string, flow *Flow) {
	if flow == nil {
		return
	}
	attrs := []any{
		"request_id", requestID,
		"flow_id", flow.ID,
		"method", flow.Method,
	}
	if flow.Method == FlowBrowser {
		attrs = append(attrs, "listener_bound", flow.ListenerBound)
	}
	c.logger.Info("oauth_flow_expired", attrs...)
}

func (c *Coordinator) storeReleasedFlow(flow *Flow) {
	if flow == nil {
		return
	}
	c.releasedMu.Lock()
	defer c.releasedMu.Unlock()
	c.releasedFlows[flow.ID] = flow
	c.latestReleasedFlowID = flow.ID
}

func (c *Coordinator) loadReleasedFlow(flowID string) *Flow {
	if flowID == "" {
		return nil
	}
	c.releasedMu.RLock()
	defer c.releasedMu.RUnlock()
	return c.releasedFlows[flowID]
}

func (c *Coordinator) latestReleasedFlow() *Flow {
	c.releasedMu.RLock()
	defer c.releasedMu.RUnlock()
	if c.latestReleasedFlowID == "" {
		return nil
	}
	return c.releasedFlows[c.latestReleasedFlowID]
}

func (c *Coordinator) cleanupReleasedFlowsOnStart() {
	c.releasedMu.Lock()
	defer c.releasedMu.Unlock()
	for id, flow := range c.releasedFlows {
		if flow == nil || !flow.ListenerBound {
			delete(c.releasedFlows, id)
			if c.latestReleasedFlowID == id {
				c.latestReleasedFlowID = ""
			}
		}
	}
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dup := make([]byte, len(src))
	copy(dup, src)
	return dup
}
