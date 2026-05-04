package oauth

import (
	"fmt"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

const (
	flowCancelledCode    = "cancelled"
	flowCancelledMessage = "Authentication cancelled"
	flowExpiredCode      = "flow_expired"
	flowExpiredMessage   = "Authentication expired"
	accessDeniedCode     = "access_denied"
	accessDeniedMessage  = "User denied consent"
	expiredTokenCode     = "expired_token"
	expiredTokenMessage  = "Device code expired"
)

type FlowTerminalError struct {
	Code    string
	Message string
}

type BrowserCallbackSnapshot struct {
	FlowID    string
	State     string
	ExpiresAt time.Time
	Status    FlowStatus
	Consumed  bool
	Winner    Rail
	Error     *FlowTerminalError
}

type FlowSnapshot struct {
	Status          FlowStatus
	Method          FlowMethod
	FlowID          string
	ListenerBound   bool
	ExpiresAt       time.Time
	CreatedAt       time.Time
	UserCode        string
	VerificationURL string
	Rail            Rail
	Account         *domain.UpstreamAccount
	Error           *FlowTerminalError
}

func (c *Coordinator) BrowserCallbackSnapshot(flowID string) (BrowserCallbackSnapshot, bool) {
	if flowID != "" {
		flow := c.lookupFlow(flowID)
		if flow == nil || flow.Method != FlowBrowser {
			return BrowserCallbackSnapshot{}, false
		}
		return snapshotBrowserCallbackFlow(flow), true
	}

	if current := c.flowPtr.Load(); current != nil {
		if current.Method != FlowBrowser {
			return BrowserCallbackSnapshot{}, false
		}
		return snapshotBrowserCallbackFlow(current), true
	}

	released := c.latestReleasedFlow()
	if released == nil || released.Method != FlowBrowser {
		return BrowserCallbackSnapshot{}, false
	}
	return snapshotBrowserCallbackFlow(released), true
}

func (s FlowSnapshot) Validate() error {
	switch s.Status {
	case FlowStatusIdle:
		return nil
	case FlowStatusPending:
		if s.FlowID == "" {
			return errorsForSnapshot("pending flow_id is empty")
		}
		if s.CreatedAt.IsZero() {
			return errorsForSnapshot("pending created_at is zero")
		}
		if s.ExpiresAt.IsZero() {
			return errorsForSnapshot("pending expires_at is zero")
		}
		switch s.Method {
		case FlowBrowser:
			return nil
		case FlowDevice:
			if s.UserCode == "" {
				return errorsForSnapshot("pending device user_code is empty")
			}
			if s.VerificationURL == "" {
				return errorsForSnapshot("pending device verification_url is empty")
			}
			return nil
		default:
			return errorsForSnapshot(fmt.Sprintf("pending method %q is unsupported", s.Method))
		}
	case FlowStatusSuccess:
		if s.FlowID == "" {
			return errorsForSnapshot("success flow_id is empty")
		}
		if s.Account == nil {
			return errorsForSnapshot("success account is nil")
		}
		switch s.Method {
		case FlowBrowser:
			if s.Rail == RailUnknown {
				return errorsForSnapshot("browser success rail is empty")
			}
			return nil
		case FlowDevice:
			return nil
		default:
			return errorsForSnapshot(fmt.Sprintf("success method %q is unsupported", s.Method))
		}
	case FlowStatusError:
		if s.FlowID == "" {
			return errorsForSnapshot("error flow_id is empty")
		}
		if s.Error == nil {
			return errorsForSnapshot("error payload is nil")
		}
		if s.Error.Code == "" {
			return errorsForSnapshot("error code is empty")
		}
		if s.Error.Message == "" {
			return errorsForSnapshot("error message is empty")
		}
		switch s.Method {
		case FlowBrowser, FlowDevice:
			return nil
		default:
			return errorsForSnapshot(fmt.Sprintf("error method %q is unsupported", s.Method))
		}
	default:
		return errorsForSnapshot(fmt.Sprintf("unknown status %q", s.Status))
	}
}

func snapshotBrowserCallbackFlow(flow *Flow) BrowserCallbackSnapshot {
	if flow == nil {
		return BrowserCallbackSnapshot{}
	}

	flow.mu.RLock()
	defer flow.mu.RUnlock()
	return BrowserCallbackSnapshot{
		FlowID:    flow.ID,
		State:     flow.State,
		ExpiresAt: flow.ExpiresAt,
		Status:    flow.Status,
		Consumed:  flow.Consumed.Load(),
		Winner:    flow.ConsumedBy,
		Error:     cloneFlowTerminalError(flow.terminalError),
	}
}

func (c *Coordinator) GetFlow() (FlowSnapshot, error) {
	if current := c.flowPtr.Load(); current != nil {
		return snapshotCurrentFlow(current)
	}

	released := c.latestReleasedFlow()
	if released == nil {
		return idleFlowSnapshot(), nil
	}
	return snapshotReleasedFlowForPoll(released)
}

func snapshotCurrentFlow(flow *Flow) (FlowSnapshot, error) {
	if flow == nil {
		return idleFlowSnapshot(), nil
	}

	flow.mu.RLock()
	status := flow.Status
	flow.mu.RUnlock()

	if status == FlowStatusSuccess || status == FlowStatusError {
		return snapshotTerminalFlowForPoll(flow)
	}

	flow.mu.RLock()
	defer flow.mu.RUnlock()
	snapshot := flow.snapshotLocked()
	return snapshot, snapshot.Validate()
}

func snapshotReleasedFlowForPoll(flow *Flow) (FlowSnapshot, error) {
	if flow == nil {
		return idleFlowSnapshot(), nil
	}
	return snapshotTerminalFlowForPoll(flow)
}

func snapshotTerminalFlowForPoll(flow *Flow) (FlowSnapshot, error) {
	flow.mu.Lock()
	defer flow.mu.Unlock()

	if flow.Status != FlowStatusSuccess && flow.Status != FlowStatusError {
		snapshot := flow.snapshotLocked()
		return snapshot, snapshot.Validate()
	}
	if flow.terminalReported {
		return idleFlowSnapshot(), nil
	}

	snapshot := flow.snapshotLocked()
	if err := snapshot.Validate(); err != nil {
		return FlowSnapshot{}, err
	}
	flow.terminalReported = true
	return snapshot, nil
}

func idleFlowSnapshot() FlowSnapshot {
	return FlowSnapshot{Status: FlowStatusIdle}
}

func (f *Flow) snapshotLocked() FlowSnapshot {
	if f == nil {
		return idleFlowSnapshot()
	}

	snapshot := FlowSnapshot{
		Status:          f.Status,
		Method:          f.Method,
		FlowID:          f.ID,
		ListenerBound:   f.ListenerBound,
		ExpiresAt:       f.ExpiresAt,
		CreatedAt:       f.CreatedAt,
		UserCode:        f.UserCode,
		VerificationURL: f.VerificationURL,
		Rail:            f.ConsumedBy,
		Account:         cloneAccountForFlowSnapshot(f.terminalAccount),
		Error:           cloneFlowTerminalError(f.terminalError),
	}
	return snapshot
}

func cloneAccountForFlowSnapshot(src *domain.UpstreamAccount) *domain.UpstreamAccount {
	if src == nil {
		return nil
	}

	dup := *src
	dup.APIKey = ""
	dup.AccessToken = nil
	dup.RefreshToken = nil
	dup.IDToken = nil
	dup.BaseURL = dupStringPointer(src.BaseURL)
	dup.LastRefresh = dupTimePointer(src.LastRefresh)
	dup.AccessExpiresAt = dupTimePointer(src.AccessExpiresAt)
	dup.Email = dupStringPointer(src.Email)
	dup.PlanType = dupStringPointer(src.PlanType)
	dup.ChatGPTAccountID = dupStringPointer(src.ChatGPTAccountID)
	return &dup
}

func cloneFlowTerminalError(src *FlowTerminalError) *FlowTerminalError {
	if src == nil {
		return nil
	}
	dup := *src
	return &dup
}

func dupStringPointer(src *string) *string {
	if src == nil {
		return nil
	}
	dup := *src
	return &dup
}

func dupTimePointer(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	dup := *src
	return &dup
}

func browserExpiredFlowError() *FlowTerminalError {
	return &FlowTerminalError{
		Code:    flowExpiredCode,
		Message: flowExpiredMessage,
	}
}

func deviceExpiredFlowError() *FlowTerminalError {
	return &FlowTerminalError{
		Code:    expiredTokenCode,
		Message: expiredTokenMessage,
	}
}

func accessDeniedFlowError() *FlowTerminalError {
	return &FlowTerminalError{
		Code:    accessDeniedCode,
		Message: accessDeniedMessage,
	}
}

func cancelledFlowError() *FlowTerminalError {
	return &FlowTerminalError{
		Code:    flowCancelledCode,
		Message: flowCancelledMessage,
	}
}

func errorsForSnapshot(msg string) error {
	return fmt.Errorf("oauth.GetFlow: %s", msg)
}
