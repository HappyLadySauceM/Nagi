package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type indexTestSource struct {
	ingress *domain.JobIngress
	batch   []domain.IndexedMessage
	query   domain.HistorySearch
}

func (s *indexTestSource) JobIngress(context.Context, int64, int64) (*domain.JobIngress, error) {
	return s.ingress, nil
}

func (s *indexTestSource) HistoryIndexBatch(context.Context, int64, int) ([]domain.IndexedMessage, error) {
	return s.batch, nil
}

func (s *indexTestSource) HistoryMessages(_ context.Context, query domain.HistorySearch, ids []int64) ([]domain.Message, error) {
	s.query = query
	return []domain.Message{{ID: ids[0], Text: "接口文档在群文件"}}, nil
}

type testHistoryIndex struct {
	bulkErr error
	docs    []domain.HistoryDocument
	deleted []int64
	onQuery func()
}

func (*testHistoryIndex) Ensure(context.Context) error { return nil }

func (i *testHistoryIndex) Index(_ context.Context, docs []domain.HistoryDocument, deleted []int64) error {
	i.docs, i.deleted = docs, deleted
	return i.bulkErr
}

func (i *testHistoryIndex) Search(context.Context, domain.HistorySearch) ([]int64, error) {
	if i.onQuery != nil {
		i.onQuery()
	}
	return []int64{10}, nil
}

func (*testHistoryIndex) DeleteBefore(context.Context, time.Time) error { return nil }

type historyTestScopes map[int64]bool

func (s historyTestScopes) Resolve(_ context.Context, _ int64, _ scopedomain.ScopeType, id int64) (scopedomain.Policy, error) {
	return scopedomain.Policy{Enabled: true, StoreMessages: true, AllowHistoryRead: s[id]}, nil
}

func TestHistorySearchRechecksGrantAndExactJobScope(t *testing.T) {
	now := time.Now().UTC()
	source := &indexTestSource{ingress: &domain.JobIngress{
		Kind: domain.KindGroup, TargetID: 8,
		Message: domain.Message{ID: 20, ConversationID: "group-a", CreatedAt: now},
	}}
	scopes := historyTestScopes{8: true, 9: true}
	index := &testHistoryIndex{}
	service := NewHistorySearch(source, index, scopes, func() int { return 0 }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	query := domain.HistorySearch{Query: "接口文档", Limit: 5}
	if rows, err := service.Search(ctx, 1, 9, 40, domain.KindGroup, query); err != nil || len(rows) != 0 {
		t.Fatalf("another conversation's job qualified: %v %v", rows, err)
	}
	index.onQuery = func() { scopes[8] = false }
	if rows, err := service.Search(ctx, 1, 8, 40, domain.KindGroup, query); err != nil || len(rows) != 0 {
		t.Fatalf("revoked search returned rows: %v %v", rows, err)
	}
	index.onQuery = nil
	scopes[8] = true
	rows, err := service.Search(ctx, 1, 8, 40, domain.KindGroup, query)
	if err != nil || len(rows) != 1 || source.query.BotID != 1 || source.query.ConversationID != "group-a" ||
		source.query.BeforeID != 20 || !source.query.Before.Equal(now) {
		t.Fatalf("rows=%v query=%+v err=%v", rows, source.query, err)
	}
}

func TestHistoryReconcileRetriesWithoutSkippingRows(t *testing.T) {
	source := &indexTestSource{batch: []domain.IndexedMessage{
		{Kind: domain.KindGroup, TargetID: 8, Message: domain.Message{ID: 12, ConversationID: "a", Text: "群文件"}},
		{Kind: domain.KindGroup, TargetID: 9, Message: domain.Message{ID: 13, ConversationID: "b", Text: "禁止读取"}},
	}}
	index := &testHistoryIndex{bulkErr: errors.New("temporary bulk rejection")}
	service := NewHistorySearch(source, index, historyTestScopes{8: true}, func() int { return 0 }, nil)
	if cursor, err := service.reconcileBatch(context.Background(), 11, 2); err == nil || cursor != 11 {
		t.Fatalf("failed batch advanced cursor = %d, err = %v", cursor, err)
	}
	index.bulkErr = nil
	cursor, err := service.reconcileBatch(context.Background(), 11, 2)
	if err != nil || cursor != 13 || len(index.docs) != 1 || index.docs[0].ID != 12 ||
		len(index.deleted) != 1 || index.deleted[0] != 13 {
		t.Fatalf("cursor=%d docs=%+v deleted=%v err=%v", cursor, index.docs, index.deleted, err)
	}
}

func TestSearchableMessageTextKeepsSavedTranscriptAndOmitsMediaFields(t *testing.T) {
	message := domain.Message{
		Text: "[CQ:image,url=https://example.test/private-image]\n[语音转写] 文档在群文件",
		Segments: []ob.Segment{
			{Type: ob.SegImage, Data: map[string]string{"url": "https://example.test/private-image"}},
			{Type: ob.SegRecord, Data: map[string]string{media.TranscriptMarker: "true"}},
		},
	}
	if got := SearchableMessageText(message); got != "[语音转写] 文档在群文件" {
		t.Fatalf("searchable text = %q", got)
	}
	original := "[语音转写] " + strings.Repeat("甲", 2200)
	message.Text = original + "\n[语音转写] 实际转写"
	message.Segments = append([]ob.Segment{ob.Text(original)}, message.Segments...)
	if got := SearchableMessageText(message); !strings.HasSuffix(got, "实际转写") || strings.Count(got, "甲") != 2200 {
		t.Fatal("original text displaced the saved transcript")
	}
}
