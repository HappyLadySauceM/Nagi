package infrastructure

import (
	"cmp"
	"context"
	"entgo.io/ent/dialect/sql"
	"fmt"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	c "github.com/HappyLadySauceM/Nagi/pkg/db/ent/conversation"
	m "github.com/HappyLadySauceM/Nagi/pkg/db/ent/memory"
	msg "github.com/HappyLadySauceM/Nagi/pkg/db/ent/message"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/predicate"
	"github.com/google/uuid"
	"slices"
	"time"
)

type Repo struct{ pool *db.Pool }

func NewRepo(pool *db.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("memory: pool required")
	}
	return &Repo{pool}, nil
}
func uuidNewString() string { return uuid.NewString() }
func (r *Repo) Insert(ctx context.Context, v domain.Memory) (string, error) {
	id := v.ID
	if id == "" {
		id = uuidNewString()
	}
	key, err := uuid.Parse(id)
	if err != nil {
		return "", err
	}
	origin := v.Origin
	if origin == "" {
		origin = domain.OriginManual
	}
	b := r.pool.Client.Memory.Create().SetID(key).SetBotID(v.BotID).SetVisibility(string(v.Visibility)).SetOwnerKey(v.OwnerKey).SetContent(v.Content).SetSource(v.Source).SetConfidence(float32(v.Confidence)).SetStatus(string(v.Status)).SetApprovedBy(v.ApprovedBy).SetOrigin(string(origin)).SetReviewReason(v.ReviewReason)
	if v.SourceMessageID != 0 {
		b.SetSourceMessageID(v.SourceMessageID)
	}
	if v.SourceSenderID != 0 {
		b.SetSourceSenderID(v.SourceSenderID)
	}
	if v.SubjectType != "" {
		b.SetSubjectType(string(v.SubjectType))
	}
	if v.SubjectID != "" {
		b.SetSubjectID(v.SubjectID)
	}
	if v.DedupeKey != "" {
		b.SetDedupeKey(v.DedupeKey)
	}
	if !v.ExpiresAt.IsZero() {
		b.SetExpiresAt(v.ExpiresAt)
	}
	if err := b.Exec(ctx); err != nil {
		return "", err
	}
	return id, nil
}
func (r *Repo) Get(ctx context.Context, id string) (domain.Memory, bool, error) {
	key, err := uuid.Parse(id)
	if err != nil {
		return domain.Memory{}, false, err
	}
	v, err := r.pool.Client.Memory.Get(ctx, key)
	if ent.IsNotFound(err) {
		return domain.Memory{}, false, nil
	}
	if err != nil {
		return domain.Memory{}, false, err
	}
	return memoryModel(v), true, nil
}
func (r *Repo) Update(ctx context.Context, v domain.Memory) error {
	current, found, err := r.Get(ctx, v.ID)
	if err != nil {
		return err
	}
	if !found {
		return memoryapp.ErrMemoryNotFound
	}
	if current.Status != v.Status || current.ApprovedBy != v.ApprovedBy {
		return memoryapp.ErrMemoryStatusConflict
	}
	v.BotID = current.BotID
	return r.UpdateMemoryLocked(ctx, v)
}
func (r *Repo) Delete(ctx context.Context, id string) (bool, error) { return r.DeleteMemory(ctx, id) }
func (r *Repo) List(ctx context.Context, f domain.ListFilter) ([]domain.Memory, int64, error) {
	q := r.pool.Client.Memory.Query()
	if f.BotID != nil {
		q.Where(m.BotID(*f.BotID))
	}
	if f.Visibility != "" {
		q.Where(m.Visibility(string(f.Visibility)))
	}
	if f.OwnerKey != "" {
		q.Where(m.OwnerKey(f.OwnerKey))
	}
	if f.Status != "" {
		q.Where(m.Status(string(f.Status)))
	}
	if f.Search != "" {
		q.Where(m.ContentContainsFold(f.Search))
	}
	n, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(m.FieldUpdatedAt)).Limit(f.Limit).Offset(f.Offset()).All(ctx)
	return memoryModels(rows), int64(n), err
}

// FindByDedupeKey returns an existing candidate or active memory sharing the
// complete structured key.
// FindByDedupeKey 返回共享完整结构化键的既有候选或激活记忆。
func (r *Repo) FindByDedupeKey(ctx context.Context, key string) (domain.Memory, bool, error) {
	v, err := r.pool.Client.Memory.Query().Where(m.DedupeKey(key), m.StatusIn("candidate", "active")).First(ctx)
	if ent.IsNotFound(err) {
		return domain.Memory{}, false, nil
	}
	if err != nil {
		return domain.Memory{}, false, err
	}
	return memoryModel(v), true, nil
}

func (r *Repo) FindSame(ctx context.Context, bot int64, visibility domain.Visibility, owner, content string) (domain.Memory, bool, error) {
	v, err := r.pool.Client.Memory.Query().Where(m.BotID(bot), m.Visibility(string(visibility)), m.OwnerKey(owner), m.Content(content), m.StatusIn("candidate", "active")).Order(ent.Desc(m.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		return domain.Memory{}, false, nil
	}
	if err != nil {
		return domain.Memory{}, false, err
	}
	return memoryModel(v), true, nil
}
func (r *Repo) SearchVisible(ctx context.Context, in domain.SearchRequest) ([]domain.Memory, error) {
	q := r.visibleMemories(in)
	keywords := in.Keywords
	if keywords == nil {
		keywords = domain.QueryKeywords(in.Query)
	}
	if len(keywords) > 0 {
		matches := make([]predicate.Memory, 0, len(keywords))
		for _, keyword := range keywords {
			matches = append(matches, m.ContentContainsFold(keyword.Term))
		}
		q.Where(m.Or(matches...))
	}
	q = q.Order(ent.Desc(m.FieldConfidence, m.FieldUpdatedAt))
	if in.Limit > 0 {
		q = q.Limit(in.Limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	local := memoryModels(rows)
	if !validConsentRead(in) {
		return local, nil
	}
	cross, err := r.consentRead(ctx, in, "", keywords)
	if err != nil {
		return nil, err
	}
	all := append(local, cross...)
	slices.SortStableFunc(all, func(a, b domain.Memory) int {
		if rank := cmp.Compare(b.Confidence, a.Confidence); rank != 0 {
			return rank
		}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	if in.Limit > 0 && len(all) > in.Limit {
		all = all[:in.Limit]
	}
	return all, nil
}

// ReadVisible uses the same scope predicate as retrieval, without keyword or ranking filters.
// ReadVisible 使用与检索相同的作用域谓词，不套用关键词和排序过滤。
func (r *Repo) ReadVisible(ctx context.Context, in domain.SearchRequest, id string) (domain.Memory, bool, error) {
	key, err := uuid.Parse(id)
	if err != nil {
		return domain.Memory{}, false, err
	}
	row, err := r.visibleMemories(in).Where(m.ID(key)).Only(ctx)
	if ent.IsNotFound(err) {
		cross, err := r.consentRead(ctx, in, id, nil)
		if len(cross) == 1 {
			return cross[0], true, err
		}
		return domain.Memory{}, false, err
	}
	if err != nil {
		return domain.Memory{}, false, err
	}
	return memoryModel(row), true, nil
}

func (r *Repo) visibleMemories(in domain.SearchRequest) *ent.MemoryQuery {
	q := r.pool.Client.Memory.Query().Where(m.BotID(in.BotID), m.Status("active"), m.Or(m.ExpiresAtIsNil(), m.ExpiresAtGT(time.Now())))
	if in.BotID <= 0 || in.TargetID <= 0 || in.Kind != "group" && in.Kind != "private" {
		return q.Where(m.BotID(0))
	}
	visibility := "group"
	if in.Kind == "private" {
		visibility = "private_user"
	}
	visible := []predicate.Memory{m.And(m.Visibility(visibility), m.OwnerKey(fmt.Sprint(in.TargetID)))}
	if key, err := uuid.Parse(in.ConversationID); err == nil {
		visible = append(visible, m.And(m.Visibility("conversation"), m.OwnerKey(key.String())))
		visible = append(visible, m.And(m.Visibility("bot_shared"), m.ApprovedByNEQ(""), func(sel *sql.Selector) {
			source := sql.Table(msg.Table)
			scope := sql.Table(c.Table)
			match := sql.Select(source.C(msg.FieldID)).From(source).Join(scope).On(source.C(msg.FieldConversationID), scope.C(c.FieldID)).Where(sql.And(
				sql.ColumnsEQ(source.C(msg.FieldID), sel.C(m.FieldSourceMessageID)),
				sql.EQ(source.C(msg.FieldDirection), "incoming"), sql.EQ(source.C(msg.FieldBotID), in.BotID),
				sql.GT(source.C(msg.FieldSenderID), 0), sql.NEQ(source.C(msg.FieldSenderID), in.BotID),
				sql.Or(sql.IsNull(sel.C(m.FieldSourceSenderID)), sql.ColumnsEQ(source.C(msg.FieldSenderID), sel.C(m.FieldSourceSenderID))),
				sql.EQ(scope.C(c.FieldID), key), sql.EQ(scope.C(c.FieldBotID), in.BotID),
				sql.EQ(scope.C(c.FieldKind), in.Kind), sql.EQ(scope.C(c.FieldTargetID), in.TargetID),
			))
			if in.Kind == "private" {
				match.Where(sql.ColumnsEQ(source.C(msg.FieldSenderID), scope.C(c.FieldTargetID)))
			}
			sel.Where(sql.Exists(match))
		}))
	}
	return q.Where(m.Or(visible...))
}

func memoryModel(v *ent.Memory) domain.Memory {
	out := domain.Memory{AuthorizationVersion: v.AuthorizationVersion, ID: v.ID.String(), BotID: v.BotID, Visibility: domain.Visibility(v.Visibility), OwnerKey: v.OwnerKey, Content: v.Content, Source: v.Source, Origin: domain.Origin(v.Origin), ReviewReason: v.ReviewReason, Confidence: float64(v.Confidence), Status: domain.Status(v.Status), ApprovedBy: v.ApprovedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if v.SourceMessageID != nil {
		out.SourceMessageID = *v.SourceMessageID
	}
	if v.SourceSenderID != nil {
		out.SourceSenderID = *v.SourceSenderID
	}
	if v.SubjectType != nil {
		out.SubjectType = domain.SubjectType(*v.SubjectType)
	}
	if v.SubjectID != nil {
		out.SubjectID = *v.SubjectID
	}
	if v.DedupeKey != nil {
		out.DedupeKey = *v.DedupeKey
	}
	if v.ExpiresAt != nil {
		out.ExpiresAt = *v.ExpiresAt
	}
	return out
}
func memoryModels(rows []*ent.Memory) []domain.Memory {
	out := make([]domain.Memory, 0, len(rows))
	for _, v := range rows {
		out = append(out, memoryModel(v))
	}
	return out
}
