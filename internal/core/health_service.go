package core

import (
	"context"
	"fmt"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

type HealthStatus struct {
	Status           string `json:"status"`
	ActiveAccounts   int    `json:"active_accounts"`
	DisabledAccounts int    `json:"disabled_accounts"`
	DeletedAccounts  int    `json:"deleted_accounts"`
	Uptime           string `json:"uptime"`
}

type HealthService struct {
	repo      AccountRepository
	startTime time.Time
}

func NewHealthService(repo AccountRepository) *HealthService {
	return &HealthService{
		repo:      repo,
		startTime: time.Now(),
	}
}

func (s *HealthService) GetHealth(ctx context.Context) (HealthStatus, error) {
	accounts, err := s.repo.List(ctx, nil)
	if err != nil {
		return HealthStatus{
			Status: "degraded",
			Uptime: s.uptime(),
		}, fmt.Errorf("list accounts for health: %w", err)
	}

	var active, disabled, deleted int
	for _, a := range accounts {
		switch a.Status {
		case domain.AccountStatusActive:
			active++
		case domain.AccountStatusDisabled:
			disabled++
		case domain.AccountStatusDeleted:
			deleted++
		}
	}

	status := "healthy"
	if active == 0 {
		status = "no_capacity"
	}

	return HealthStatus{
		Status:           status,
		ActiveAccounts:   active,
		DisabledAccounts: disabled,
		DeletedAccounts:  deleted,
		Uptime:           s.uptime(),
	}, nil
}

func (s *HealthService) uptime() string {
	return fmt.Sprintf("%.0fs", time.Since(s.startTime).Seconds())
}
