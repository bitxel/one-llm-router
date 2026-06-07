package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/user/one-llm-router/internal/domain"
)

type AccountService struct {
	repo   AccountRepository
	logger *slog.Logger
}

func NewAccountService(repo AccountRepository, logger *slog.Logger) *AccountService {
	return &AccountService{repo: repo, logger: logger}
}

func (s *AccountService) Create(ctx context.Context, name, provider, apiKey string, baseURL *string, capabilities []string) (*domain.UpstreamAccount, error) {
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: "account name is required"}
	}
	if apiKey == "" {
		return nil, &domain.ValidationError{Field: "api_key", Message: "api_key is required"}
	}
	if provider == "" {
		provider = domain.ProviderOpenAI
	}
	if provider != domain.ProviderOpenAI {
		return nil, &domain.ValidationError{Field: "provider", Message: "unsupported provider: " + provider + " (MVP supports openai only)"}
	}
	if baseURL != nil && *baseURL != "" {
		if err := domain.ValidateBaseURL(*baseURL); err != nil {
			return nil, err
		}
	}
	if len(capabilities) > 0 {
		if err := domain.ValidateCapabilities(capabilities); err != nil {
			return nil, &domain.ValidationError{Field: "capabilities", Message: err.Error()}
		}
	}

	account := &domain.UpstreamAccount{
		Name:         name,
		Provider:     provider,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		Status:       domain.AccountStatusActive,
		Capabilities: capabilities,
	}

	if err := s.repo.Create(ctx, account); err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}

	s.logger.Info("account created",
		"account_id", account.ID,
		"name", account.Name,
		"provider", account.Provider,
		"capabilities", account.Capabilities,
	)
	return account, nil
}

func (s *AccountService) Enable(ctx context.Context, id int64) error {
	acct, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if acct.Status == domain.AccountStatusDeleted {
		return domain.ErrAccountDeleted
	}
	if acct.Status == domain.AccountStatusActive {
		return nil
	}
	if acct.Status != domain.AccountStatusDisabled {
		return fmt.Errorf("%w: cannot enable from %q", domain.ErrInvalidTransition, acct.Status)
	}

	if err := s.repo.UpdateStatus(ctx, id, domain.AccountStatusActive); err != nil {
		return fmt.Errorf("enable account %d: %w", id, err)
	}

	s.logger.Info("account enabled", "account_id", id)
	return nil
}

func (s *AccountService) Disable(ctx context.Context, id int64) error {
	acct, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if acct.Status == domain.AccountStatusDeleted {
		return domain.ErrAccountDeleted
	}
	if acct.Status == domain.AccountStatusDisabled {
		return nil
	}

	if err := s.repo.UpdateStatus(ctx, id, domain.AccountStatusDisabled); err != nil {
		return fmt.Errorf("disable account %d: %w", id, err)
	}

	s.logger.Info("account disabled", "account_id", id)
	return nil
}

func (s *AccountService) Delete(ctx context.Context, id int64) error {
	acct, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if acct.Status == domain.AccountStatusDeleted {
		return nil
	}

	if err := s.repo.UpdateStatus(ctx, id, domain.AccountStatusDeleted); err != nil {
		return fmt.Errorf("delete account %d: %w", id, err)
	}

	s.logger.Info("account deleted", "account_id", id)
	return nil
}

func (s *AccountService) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *AccountService) List(ctx context.Context, statusFilter []string) ([]domain.UpstreamAccount, error) {
	return s.repo.List(ctx, statusFilter)
}
