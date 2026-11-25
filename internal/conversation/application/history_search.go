package application

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

const historyQueryTimeout = 1500 * time.Millisecond

type historySource interface {
	JobIngress(ctx context.Context, botID, jobID int64) (*domain.JobIngress, error)
	SearchHistory(ctx context.Context, search domain.HistorySearch) ([]contracts.HistoryHit, error)
}

type HistorySearch struct {
	source    historySource
	scopes    scopecontracts.Authorizer
	retention func() int
}

func NewHistorySearch(source historySource, scopes scopecontracts.Authorizer, retention func() int) *HistorySearch {
	return &HistorySearch{source: source, scopes: scopes, retention: retention}
}

func (s *HistorySearch) Search(ctx context.Context, request contracts.HistoryRequest) ([]contracts.HistoryHit, error) {
	kind := domain.Kind(request.Kind)
	keyword := strings.TrimSpace(request.Query)
	if !kind.Valid() || keyword == "" || utf8.RuneCountInString(keyword) > 160 || strings.ContainsRune(keyword, 0) || request.Limit < 1 || request.Limit > 8 {
		return nil, fmt.Errorf("conversation: invalid history query")
	}
	allowed, err := s.allowed(ctx, request)
	if err != nil || !allowed {
		return nil, err
	}
	ingress, err := s.source.JobIngress(ctx, request.BotID, request.JobID)
	if err != nil || ingress == nil || ingress.Kind != kind || ingress.TargetID != request.TargetID {
		return nil, err
	}
	if request.ConversationID != ingress.Message.ConversationID {
		return nil, nil
	}
	query := domain.HistorySearch{
		BotID: request.BotID, ConversationID: ingress.Message.ConversationID,
		BeforeID: ingress.Message.ID, Before: ingress.Message.CreatedAt,
		After: request.After, SenderID: request.SenderID, Query: keyword, Limit: request.Limit,
	}
	if minimum := s.cutoff(); query.After.Before(minimum) {
		query.After = minimum
	}
	readCtx, cancel := context.WithTimeout(ctx, historyQueryTimeout)
	hits, err := s.source.SearchHistory(readCtx, query)
	readErr := readCtx.Err()
	cancel()
	if readErr != nil {
		return nil, readErr
	}
	if err != nil {
		return nil, err
	}
	allowed, err = s.allowed(ctx, request)
	if err != nil || !allowed {
		return nil, err
	}
	minimum := s.cutoff()
	kept := hits[:0]
	for _, hit := range hits {
		if !hit.CreatedAt.Before(minimum) {
			kept = append(kept, hit)
		}
	}
	return kept, ctx.Err()
}

func (s *HistorySearch) allowed(ctx context.Context, request contracts.HistoryRequest) (bool, error) {
	policy, err := s.scopes.Resolve(ctx, request.BotID, scopedomain.ScopeType(request.Kind), request.TargetID)
	return policy.Enabled && policy.StoreMessages && policy.AllowHistoryRead, err
}

func (s *HistorySearch) cutoff() time.Time {
	if days := s.retention(); days > 0 {
		return time.Now().UTC().AddDate(0, 0, -days)
	}
	return time.Time{}
}
