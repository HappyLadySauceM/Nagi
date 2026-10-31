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
	b := r.pool.Client.Memory.Create().SetID(key).SetBotID(v.BotID).SetVisibility(string(v.Visibility)).SetOwnerKey(v.OwnerKey).SetContent(v.Content).SetSource(v.Source).SetConfidence(float32(v.Confidence)).SetStatus(string(v.Status)).SetApprovedBy(v.ApprovedBy)
	if v.SourceMessageID != 0 {
		b.SetSourceMessageID(v.SourceMessageID)
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
	b := r.pool.Client.Memory.UpdateOneID(key).SetContent(v.Content).SetVisibility(string(v.Visibility)).SetOwnerKey(v.OwnerKey).SetStatus(string(v.Status)).SetApprovedBy(v.ApprovedBy).SetUpdatedAt(time.Now())
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
	if in.Query != "" {
		q.Where(m.ContentContainsFold(in.Query))
	}
	rows, err := q.Order(ent.Desc(m.FieldConfidence, m.FieldUpdatedAt)).Limit(in.Limit).All(ctx)
	return memoryModels(rows), err
}
func memoryModel(v *ent.Memory) domain.Memory {
	out := domain.Memory{ID: v.ID.String(), BotID: v.BotID, Visibility: domain.Visibility(v.Visibility), OwnerKey: v.OwnerKey, Content: v.Content, Source: v.Source, Confidence: float64(v.Confidence), Status: domain.Status(v.Status), ApprovedBy: v.ApprovedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if v.SourceMessageID != nil {
		out.SourceMessageID = *v.SourceMessageID
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
