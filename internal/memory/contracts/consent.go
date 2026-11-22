package contracts

import (
	"errors"
	"time"
)

var (
	ErrConsentInvalid  = errors.New("memory consent: invalid request")
	ErrConsentConflict = errors.New("memory consent: state conflict")
	ErrConsentMissing  = errors.New("memory consent: proposal not found")
	ErrConsentDisabled = errors.New("memory consent: new grants disabled")
)

type ConsentProposal struct {
	ID                   string    `json:"id"`
	BotID                int64     `json:"bot_id"`
	MemoryID             string    `json:"memory_id"`
	FactVersion          int64     `json:"fact_version"`
	SubjectID            int64     `json:"subject_id"`
	SourceConversationID string    `json:"source_conversation_id"`
	SourceGroupID        int64     `json:"source_group_id"`
	TargetGroups         []int64   `json:"target_groups"`
	Status               string    `json:"status"`
	StateVersion         int64     `json:"state_version"`
	CreatedAt            time.Time `json:"created_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	DeliveryStatus       string    `json:"delivery_status"`
	PlatformMessageID    int64     `json:"platform_message_id"`
	ActiveGroups         []int64   `json:"active_groups"`
}

type CreateConsent struct {
	BotID          int64
	MemoryID       string
	FactVersion    int64
	TargetGroups   []int64
	IdempotencyKey string
	MaxPromptRunes int
}

// ConsentResult is the original disposition and the current proposal state.
// ConsentResult 包含原始处理结果及提案当前状态。
type ConsentResult struct {
	Outcome      string
	ProposalID   string
	Status       string
	StateVersion int64
}
