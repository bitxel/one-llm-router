package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/user/one-llm-router/internal/domain"
)

var ErrPreForward = errors.New("core: pre-forward failed")

type PreForwardError struct {
	Message string
	Cause   error
}

func (e *PreForwardError) Error() string {
	if e == nil {
		return ErrPreForward.Error()
	}
	if e.Message == "" {
		return ErrPreForward.Error()
	}
	return fmt.Sprintf("%s: %s", ErrPreForward, e.Message)
}

func (e *PreForwardError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *PreForwardError) Is(target error) bool {
	return target == ErrPreForward
}

type AccountUnavailableReason string

const (
	AccountUnavailableMissing               AccountUnavailableReason = "missing"
	AccountUnavailableDeleted               AccountUnavailableReason = "deleted"
	AccountUnavailableDisabled              AccountUnavailableReason = "disabled"
	AccountUnavailableIneligible            AccountUnavailableReason = "ineligible"
	AccountUnavailableCredentialUnavailable AccountUnavailableReason = "credential_unavailable"
)

type AccountSelectionError struct {
	AccountID int64
	Reason    AccountUnavailableReason
}

func (e *AccountSelectionError) Error() string {
	if e == nil {
		return "account unavailable"
	}
	return fmt.Sprintf("account unavailable: id=%d reason=%s", e.AccountID, e.Reason)
}

func (e *AccountSelectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Reason {
	case AccountUnavailableMissing:
		return domain.ErrAccountNotFound
	case AccountUnavailableDeleted:
		return domain.ErrAccountDeleted
	default:
		return nil
	}
}

type AccountSelector struct {
	repo          AccountRepository
	sessionRouter SessionRouter
	counter       atomic.Uint64
	PreForward    func(context.Context, *domain.UpstreamAccount) ([]byte, bool, error)
}

type PreparedAccount struct {
	Account      domain.UpstreamAccount
	Token        []byte
	UsedFallback bool
}

func NewAccountSelector(repo AccountRepository, sessionRouter SessionRouter) *AccountSelector {
	return &AccountSelector{
		repo:          repo,
		sessionRouter: sessionRouter,
	}
}

func (s *AccountSelector) SelectAccount(ctx context.Context, sessionKey string) (domain.UpstreamAccount, error) {
	return s.pick(ctx, sessionKey, nil)
}

func (s *AccountSelector) Select(ctx context.Context, sessionKey string) (domain.UpstreamAccount, []byte, bool, error) {
	acct, err := s.pick(ctx, sessionKey, nil)
	if err != nil {
		return domain.UpstreamAccount{}, nil, false, err
	}

	token, usedFallback, err := s.prepareToken(ctx, &acct)
	return acct, token, usedFallback, err
}

func (s *AccountSelector) SelectEligible(ctx context.Context, sessionKey string, eligible func(domain.UpstreamAccount) bool) (domain.UpstreamAccount, []byte, bool, error) {
	acct, err := s.pick(ctx, sessionKey, eligible)
	if err != nil {
		return domain.UpstreamAccount{}, nil, false, err
	}

	token, usedFallback, err := s.prepareToken(ctx, &acct)
	return acct, token, usedFallback, err
}

func (s *AccountSelector) ListEligiblePrepared(ctx context.Context, eligible func(domain.UpstreamAccount) bool) ([]PreparedAccount, error) {
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active accounts: %w", err)
	}

	prepared := make([]PreparedAccount, 0, len(active))
	for _, acct := range active {
		if eligible != nil && !eligible(acct) {
			continue
		}
		token, usedFallback, err := s.prepareToken(ctx, &acct)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, PreparedAccount{
			Account:      acct,
			Token:        token,
			UsedFallback: usedFallback,
		})
	}
	if len(prepared) == 0 {
		return nil, domain.ErrNoCapacity
	}
	return prepared, nil
}

func (s *AccountSelector) SelectByID(ctx context.Context, id int64) (domain.UpstreamAccount, []byte, bool, error) {
	if id <= 0 {
		return domain.UpstreamAccount{}, nil, false, &AccountSelectionError{
			AccountID: id,
			Reason:    AccountUnavailableMissing,
		}
	}
	acct, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return domain.UpstreamAccount{}, nil, false, &AccountSelectionError{
				AccountID: id,
				Reason:    AccountUnavailableMissing,
			}
		}
		return domain.UpstreamAccount{}, nil, false, fmt.Errorf("get account %d: %w", id, err)
	}
	switch acct.Status {
	case domain.AccountStatusActive:
	case domain.AccountStatusDisabled:
		return domain.UpstreamAccount{}, nil, false, &AccountSelectionError{
			AccountID: id,
			Reason:    AccountUnavailableDisabled,
		}
	case domain.AccountStatusDeleted:
		return domain.UpstreamAccount{}, nil, false, &AccountSelectionError{
			AccountID: id,
			Reason:    AccountUnavailableDeleted,
		}
	default:
		return domain.UpstreamAccount{}, nil, false, &AccountSelectionError{
			AccountID: id,
			Reason:    AccountUnavailableIneligible,
		}
	}

	token, usedFallback, err := s.prepareToken(ctx, acct)
	return *acct, token, usedFallback, err
}

func (s *AccountSelector) prepareToken(ctx context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
	if s.PreForward == nil {
		if acct.APIKey == "" {
			return nil, false, &AccountSelectionError{
				AccountID: acct.ID,
				Reason:    AccountUnavailableCredentialUnavailable,
			}
		}
		return []byte(acct.APIKey), false, nil
	}

	token, usedFallback, err := s.PreForward(ctx, acct)
	if err != nil {
		return nil, false, &PreForwardError{Message: "credential refresh error", Cause: err}
	}
	if len(token) == 0 {
		return nil, false, &PreForwardError{Message: "pre-forward returned empty access token"}
	}
	return cloneBytes(token), usedFallback, nil
}

func (s *AccountSelector) pick(ctx context.Context, sessionKey string, eligible func(domain.UpstreamAccount) bool) (domain.UpstreamAccount, error) {
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return domain.UpstreamAccount{}, fmt.Errorf("list active accounts: %w", err)
	}
	if eligible != nil {
		filtered := active[:0]
		for _, acct := range active {
			if eligible(acct) {
				filtered = append(filtered, acct)
			}
		}
		active = filtered
	}
	if len(active) == 0 {
		return domain.UpstreamAccount{}, domain.ErrNoCapacity
	}

	if sessionKey != "" && s.sessionRouter != nil {
		return s.sessionRouter.Route(sessionKey, active)
	}

	idx := s.counter.Add(1) - 1
	return active[idx%uint64(len(active))], nil
}
func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
