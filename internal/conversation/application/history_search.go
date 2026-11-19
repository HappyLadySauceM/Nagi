package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type historySource interface {
	JobIngress(ctx context.Context, botID, jobID int64) (*domain.JobIngress, error)
	HistoryIndexBatch(ctx context.Context, afterID int64, limit int) ([]domain.IndexedMessage, error)
	HistoryMessages(ctx context.Context, search domain.HistorySearch, ids []int64) ([]domain.Message, error)
}

type historyIndex interface {
	Ensure(context.Context) error
	Index(context.Context, []domain.HistoryDocument, []int64) error
	Search(context.Context, domain.HistorySearch) ([]int64, error)
	DeleteBefore(context.Context, time.Time) error
}

// HistorySearch serves scoped search and reconciles its index with PostgreSQL.
// HistorySearch 提供会话范围内检索，并以 PostgreSQL 为准对账索引。
type HistorySearch struct {
	source    historySource
	index     historyIndex
	scopes    scopecontracts.Authorizer
	retention func() int
	logger    *slog.Logger
}

// NewHistorySearch binds the authoritative source, derived index and current scope policy.
// NewHistorySearch 绑定事实来源、派生索引和实时作用域策略。
func NewHistorySearch(source historySource, index historyIndex, scopes scopecontracts.Authorizer, retention func() int, logger *slog.Logger) *HistorySearch {
	return &HistorySearch{source: source, index: index, scopes: scopes, retention: retention, logger: logger}
}

// Search checks live authorization before and after retrieving search candidates.
// Search 在读取候选前后均复核当前历史授权。
func (s *HistorySearch) Search(ctx context.Context, botID, targetID, jobID int64, kind domain.Kind, query domain.HistorySearch) ([]domain.Message, error) {
	if !kind.Valid() || len([]rune(query.Query)) > 160 || strings.TrimSpace(query.Query) == "" || query.Limit < 1 || query.Limit > 8 {
		return nil, fmt.Errorf("conversation: invalid history query")
	}
	allowed, err := s.allowed(ctx, botID, kind, targetID)
	if err != nil || !allowed {
		return nil, err
	}
	ingress, err := s.source.JobIngress(ctx, botID, jobID)
	if err != nil || ingress == nil || ingress.Kind != kind || ingress.TargetID != targetID {
		return nil, err
	}
	query.BotID = botID
	query.ConversationID = ingress.Message.ConversationID
	query.BeforeID = ingress.Message.ID
	query.Before = ingress.Message.CreatedAt
	if minimum := s.cutoff(); query.After.Before(minimum) {
		query.After = minimum
	}
	ids, err := s.index.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	messages, err := s.source.HistoryMessages(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	allowed, err = s.allowed(ctx, botID, kind, targetID)
	if err != nil || !allowed {
		return nil, err
	}
	return messages, nil
}

// Run reconciles fixed-size batches without blocking message ingestion.
// An incomplete batch is retried without advancing its cursor. Each full scan
// restarts from zero, revisiting edited, recently committed, and revoked rows.
// Run 按固定批次对账，不阻塞消息入库。批次失败时不推进游标，扫描完整轮后
// 重新开始，以覆盖更新后的正文、延迟提交和已撤销授权的记录。
func (s *HistorySearch) Run(ctx context.Context) error {
	const batchSize = 100
	var cursor int64
	ready := false
	backoff := time.Second
	for ctx.Err() == nil {
		interval := 2 * time.Second
		var err error
		if !ready {
			err = s.index.Ensure(ctx)
			ready = err == nil
		}
		if ready {
			var next int64
			next, err = s.reconcileBatch(ctx, cursor, batchSize)
			if err == nil {
				cursor = next
				if cursor == 0 {
					interval = 45 * time.Second
				}
			}
		}
		if err != nil {
			ready = false
			if ctx.Err() == nil {
				s.logger.Warn("history index reconcile failed", "error", err, "cursor", cursor)
			}
			backoff = min(30*time.Second, 2*backoff)
			interval = backoff
		} else {
			backoff = time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	return nil
}

func (s *HistorySearch) reconcileBatch(ctx context.Context, cursor int64, limit int) (int64, error) {
	rows, err := s.source.HistoryIndexBatch(ctx, cursor, limit)
	if err != nil {
		return cursor, err
	}
	cutoff := s.cutoff()
	policies := make(map[string]bool)
	var documents []domain.HistoryDocument
	var removals []int64
	for _, row := range rows {
		m := row.Message
		allowed, cached := policies[m.ConversationID]
		if !cached {
			allowed, err = s.allowed(ctx, m.BotID, row.Kind, row.TargetID)
			if err != nil {
				return cursor, err
			}
			policies[m.ConversationID] = allowed
		}
		text := SearchableMessageText(m)
		if !allowed || m.CreatedAt.Before(cutoff) || text == "" || m.Direction == domain.Outgoing && m.SentStatus != "sent" {
			removals = append(removals, m.ID)
			continue
		}
		documents = append(documents, domain.HistoryDocument{
			ID: m.ID, BotID: m.BotID, ConversationID: m.ConversationID, SenderID: m.SenderID,
			CreatedAt: m.CreatedAt, Text: text,
		})
	}
	if err := s.index.Index(ctx, documents, removals); err != nil {
		return cursor, err
	}
	if len(rows) == limit {
		return rows[len(rows)-1].Message.ID, nil
	}
	if !cutoff.IsZero() {
		if err := s.index.DeleteBefore(ctx, cutoff); err != nil {
			return cursor, err
		}
	}
	return 0, nil
}

func (s *HistorySearch) allowed(ctx context.Context, botID int64, kind domain.Kind, targetID int64) (bool, error) {
	policy, err := s.scopes.Resolve(ctx, botID, scopedomain.ScopeType(kind), targetID)
	return policy.Enabled && policy.StoreMessages && policy.AllowHistoryRead, err
}

func (s *HistorySearch) cutoff() time.Time {
	if days := s.retention(); days > 0 {
		return time.Now().UTC().AddDate(0, 0, -days)
	}
	return time.Time{}
}

// SearchableMessageText extracts plain words and avoids indexing raw URLs from segments.
// SearchableMessageText 提取纯文本，避免索引消息段中的原始 URL。
func SearchableMessageText(message domain.Message) string {
	text := message.Text
	if len(message.Segments) > 0 {
		text = ob.PlainText(message.Segments)
		for _, segment := range message.Segments {
			if segment.Type == ob.SegRecord && segment.Data[media.TranscriptMarker] == "true" {
				text = ob.PlainText(ob.ParseCQ(message.Text))
				break
			}
		}
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 4096 {
		runes = runes[:4096]
	}
	return string(runes)
}
