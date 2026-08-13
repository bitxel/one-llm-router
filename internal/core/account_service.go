package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
)

type AccountService struct {
	repo           AccountRepository
	modelRefresher *ModelRefresher
	modelRepo      ModelRefresherRepo
	logger         *slog.Logger
}

func NewAccountService(repo AccountRepository, logger *slog.Logger) *AccountService {
	return &AccountService{repo: repo, logger: logger}
}

func (s *AccountService) SetModelRefresher(refresher *ModelRefresher, modelRepo ModelRefresherRepo) {
	s.modelRefresher = refresher
	s.modelRepo = modelRepo
}

func (s *AccountService) Create(ctx context.Context, name, provider, apiKey string, baseURL *string, capabilities []string) (*domain.UpstreamAccount, error) {
	trimmedName := strings.TrimSpace(name)
	if !domain.ValidAccountName(trimmedName) {
		return nil, &domain.ValidationError{
			Field:   "name",
			Message: "account name must be 1-64 characters (after trimming) and free of control characters",
		}
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
		Name:         trimmedName,
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
	if s.modelRefresher != nil && s.modelRepo != nil && account.Status == domain.AccountStatusActive {
		go func() {
			_, _, _, refreshErr := s.modelRefresher.Refresh(context.Background(), account, s.modelRepo)
			if refreshErr != nil {
				s.logger.Error("model refresh after account creation failed",
					"account_id", account.ID,
					"name", account.Name,
					"error", refreshErr,
				)
			}
		}()
	}
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

// AccountDetailsPatch is the optional-field update set for api_key rows.
// Pointer fields distinguish "keep current" (nil) from "set" (non-nil);
// Capabilities replaces the whole set when non-nil. An empty api_key
// pointer value means "keep the existing key".
type AccountDetailsPatch struct {
	Name         *string
	APIKey       *string
	BaseURL      *string
	Capabilities []string
}

// UpdateDetails validates and applies an AccountDetailsPatch to an
// api_key account row. Only api_key-shaped accounts are editable —
// OAuth rows carry flow-minted tokens and observed metadata that the
// operator must not hand-edit (see AGENTS.md "No fabricated facts").
func (s *AccountService) UpdateDetails(ctx context.Context, id int64, patch AccountDetailsPatch) (*domain.UpstreamAccount, error) {
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if !domain.ValidAccountName(trimmed) {
			return nil, &domain.ValidationError{
				Field:   "name",
				Message: "account name must be 1-64 characters (after trimming) and free of control characters",
			}
		}
		patch.Name = &trimmed
	}
	if patch.APIKey != nil {
		trimmed := strings.TrimSpace(*patch.APIKey)
		if trimmed == "" {
			// Blank api_key in the edit form means "keep existing".
			patch.APIKey = nil
		} else if !domain.ValidUpstreamAPIKey(trimmed) {
			return nil, &domain.ValidationError{Field: "api_key", Message: "api_key must be 1..256 characters"}
		} else {
			patch.APIKey = &trimmed
		}
	}
	if patch.BaseURL != nil && *patch.BaseURL != "" {
		if err := domain.ValidateBaseURL(*patch.BaseURL); err != nil {
			return nil, err
		}
	}
	if patch.Capabilities != nil {
		if err := domain.ValidateCapabilities(patch.Capabilities); err != nil {
			return nil, &domain.ValidationError{Field: "capabilities", Message: err.Error()}
		}
	}

	acct, err := s.repo.UpdateDetails(ctx, id, patch)
	if err != nil {
		return nil, fmt.Errorf("update account details: %w", err)
	}

	s.logger.Info("account updated",
		"account_id", acct.ID,
		"name", acct.Name,
		"capabilities", acct.Capabilities,
	)
	return acct, nil
}

func (s *AccountService) List(ctx context.Context, statusFilter []string) ([]domain.UpstreamAccount, error) {
	return s.repo.List(ctx, statusFilter)
}
