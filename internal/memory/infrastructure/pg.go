package infrastructure

import (
	"context"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	m "github.com/HappyLadySauceM/Nagi/pkg/db/ent/memory"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/predicate"
	"github.com/google/uuid"
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
	key, err := uuid.Parse(v.ID)
	if err != nil {
		return err
	}
	origin := v.Origin
	if origin == "" {
		origin = domain.OriginManual
	}
	b := r.pool.Client.Memory.UpdateOneID(key).SetContent(v.Content).SetVisibility(string(v.Visibility)).SetOwnerKey(v.OwnerKey).SetStatus(string(v.Status)).SetApprovedBy(v.ApprovedBy).SetOrigin(string(origin)).SetReviewReason(v.ReviewReason).SetUpdatedAt(time.Now())
	if v.SourceSenderID != 0 {
		b.SetSourceSenderID(v.SourceSenderID)
	} else {
		b.ClearSourceSenderID()
	}
	if v.SubjectType != "" {
		b.SetSubjectType(string(v.SubjectType))
	} else {
		b.ClearSubjectType()
	}
	if v.SubjectID != "" {
		b.SetSubjectID(v.SubjectID)
	} else {
		b.ClearSubjectID()
	}
	if v.DedupeKey != "" {
		b.SetDedupeKey(v.DedupeKey)
	} else {
		b.ClearDedupeKey()
	}
	if v.ExpiresAt.IsZero() {
		b.ClearExpiresAt()
	} else {
		b.SetExpiresAt(v.ExpiresAt)
	}
	return b.Exec(ctx)
}
func (r *Repo) SoftDelete(ctx context.Context, id string) error {
	key, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = r.pool.Client.Memory.Update().Where(m.ID(key)).SetStatus("deleted").SetUpdatedAt(time.Now()).Save(ctx)
	return err
}
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
	visibility := "group"
	if in.Kind == "private" {
		visibility = "private_user"
	}
	owner := ""
	if in.TargetID > 0 {
		owner = fmt.Sprint(in.TargetID)
	}
	visible := []predicate.Memory{m.Visibility("bot_shared")}
	if owner != "" {
		visible = append(visible, m.And(m.Visibility(visibility), m.OwnerKey(owner)))
	}
	if in.ConversationID != "" {
		visible = append(visible, m.And(m.Visibility("conversation"), m.OwnerKey(in.ConversationID)))
	}
	q := r.pool.Client.Memory.Query().Where(m.BotID(in.BotID), m.Status("active"), m.Or(m.ExpiresAtIsNil(), m.ExpiresAtGT(time.Now())), m.Or(visible...))
	if keywords := domain.QueryKeywords(in.Query); len(keywords) > 0 {
		matches := make([]predicate.Memory, 0, len(keywords))
		for _, keyword := range keywords {
			matches = append(matches, m.ContentContainsFold(keyword))
		}
		q.Where(m.Or(matches...))
	}
	rows, err := q.Order(ent.Desc(m.FieldConfidence, m.FieldUpdatedAt)).Limit(in.Limit).All(ctx)
	return memoryModels(rows), err
}
func memoryModel(v *ent.Memory) domain.Memory {
	out := domain.Memory{ID: v.ID.String(), BotID: v.BotID, Visibility: domain.Visibility(v.Visibility), OwnerKey: v.OwnerKey, Content: v.Content, Source: v.Source, Origin: domain.Origin(v.Origin), ReviewReason: v.ReviewReason, Confidence: float64(v.Confidence), Status: domain.Status(v.Status), ApprovedBy: v.ApprovedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
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
