package domain

import (
	"errors"
	"time"
)

// ErrModelNotFound is returned when no active account supports the requested model.
var ErrModelNotFound = errors.New("model not found on any active account")

// ErrAccountModelDuplicate is returned when trying to insert a duplicate account model.
var ErrAccountModelDuplicate = errors.New("account model duplicate")

// AccountModel tracks which models a specific upstream account supports.
// It lives in the domain layer as pure data with no store dependency.
// Metadata holds a JSON snapshot of the upstream model object (null for
// manual entries without upstream metadata).
type AccountModel struct {
	ID        int64     `xorm:"pk autoincr 'id'" json:"id"`
	AccountID int64     `xorm:"not null 'account_id'" json:"account_id"`
	ModelID   string    `xorm:"not null 'model_id'" json:"model_id"`
	Source    string    `xorm:"not null default('manual') 'source'" json:"source"`
	Metadata  *string   `xorm:"'metadata'" json:"metadata,omitempty"`
	CreatedAt time.Time `xorm:"created not null 'created_at'" json:"created_at"`
	UpdatedAt time.Time `xorm:"updated not null 'updated_at'" json:"updated_at"`
}

// AccountModelDraft is a refresh write target: one upstream model ID plus
// the raw JSON of its upstream object (may be nil).
type AccountModelDraft struct {
	ID       string
	Metadata []byte
}

func (AccountModel) TableName() string { return "account_models" }

// AccountModelSource constants for the source column.
const (
	AccountModelSourceManual   = "manual"
	AccountModelSourceUpstream = "upstream"
)
