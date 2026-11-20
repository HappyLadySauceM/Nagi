// PostgreSQL repository for locally published intent prompt versions.
// 本地已发布意图 Prompt 版本的 PostgreSQL 仓库。
package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	ipv "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentpromptversion"
)

var (
	ErrPromptNotFound    = errors.New("intent: prompt not found")
	ErrPromptImmutable   = errors.New("intent: prompt version is not a draft")
	ErrPromptNotArchived = errors.New("intent: prompt version is not an archived published version")
	ErrPromptPublished   = errors.New("intent: published prompt version cannot be deleted")
	ErrPromptBuiltin     = errors.New("intent: builtin prompt version cannot be deleted")
)

type PromptsRepo struct{ pool *db.Pool }

func NewPromptsRepo(p *db.Pool) (*PromptsRepo, error) {
	if p == nil {
		return nil, fmt.Errorf("intent: pool required")
	}
	return &PromptsRepo{pool: p}, nil
}

// Active returns the currently published version for one logical prompt and provider.
// Active 返回某个逻辑 Prompt 与提供方当前已发布的版本。
func (r *PromptsRepo) Active(ctx context.Context, promptID, provider string) (domain.PromptRecord, bool, error) {
	row, err := r.pool.Client.IntentPromptVersion.Query().
		Where(
			ipv.PromptID(promptID), ipv.PromptKind(domain.PromptKindIntentParticipation),
			ipv.Provider(provider), ipv.BotID(0), ipv.ScopeType(domain.PromptScopeAccount), ipv.ScopeID(0),
			ipv.Status(domain.PromptStatusPublished), ipv.DeletedAtIsNil(),
		).
		Order(ent.Desc(ipv.FieldVersion)).First(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, false, nil
	}
	if err != nil {
		return domain.PromptRecord{}, false, err
	}
	return promptRecord(row), true, nil
}

func (r *PromptsRepo) List(ctx context.Context) ([]domain.PromptRecord, error) {
	rows, err := r.pool.Client.IntentPromptVersion.Query().Where(
		ipv.PromptKind(domain.PromptKindIntentParticipation), ipv.BotID(0),
		ipv.ScopeType(domain.PromptScopeAccount), ipv.ScopeID(0), ipv.DeletedAtIsNil(),
	).Order(ent.Desc(ipv.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PromptRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptRecord(row))
	}
	return out, nil
}

func (r *PromptsRepo) Versions(ctx context.Context, promptID string) ([]domain.PromptRecord, error) {
	rows, err := r.pool.Client.IntentPromptVersion.Query().
		Where(
			ipv.PromptID(promptID), ipv.PromptKind(domain.PromptKindIntentParticipation),
			ipv.BotID(0), ipv.ScopeType(domain.PromptScopeAccount), ipv.ScopeID(0), ipv.DeletedAtIsNil(),
		).
		Order(ent.Desc(ipv.FieldVersion)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PromptRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptRecord(row))
	}
	return out, nil
}

func (r *PromptsRepo) Get(ctx context.Context, id int64) (domain.PromptRecord, error) {
	row, err := r.pool.Client.IntentPromptVersion.Query().Where(ipv.ID(id), ipv.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, err
	}
	return promptRecord(row), nil
}

// GetIncludingDeleted returns a prompt version even after soft deletion. It is
// used only to rebuild historical prompt snapshots referenced by version ID.
// GetIncludingDeleted 返回包含已软删除的 Prompt 版本；仅用于按版本 ID 重建历史 Prompt 快照。
func (r *PromptsRepo) GetIncludingDeleted(ctx context.Context, id int64) (domain.PromptRecord, error) {
	row, err := r.pool.Client.IntentPromptVersion.Query().Where(ipv.ID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, err
	}
	return promptRecord(row), nil
}

// CreateDraft creates the next immutable draft version for a logical prompt.
// CreateDraft 为某个逻辑 Prompt 创建下一个不可变草稿版本。
func (r *PromptsRepo) CreateDraft(ctx context.Context, promptID, name, provider string, content domain.Prompt, note, source, sourceRef string) (domain.PromptRecord, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return domain.PromptRecord{}, err
	}
	defer tx.Rollback()
	c := tx.Client()
	var latest int
	if row, err := c.IntentPromptVersion.Query().Where(
		ipv.PromptID(promptID), ipv.PromptKind(domain.PromptKindIntentParticipation),
		ipv.BotID(0), ipv.ScopeType(domain.PromptScopeAccount), ipv.ScopeID(0),
	).Order(ent.Desc(ipv.FieldVersion)).First(ctx); err == nil {
		latest = row.Version
	} else if !ent.IsNotFound(err) {
		return domain.PromptRecord{}, err
	}
	raw, err := json.Marshal(promptContentPayload(content))
	if err != nil {
		return domain.PromptRecord{}, err
	}
	row, err := c.IntentPromptVersion.Create().
		SetPromptID(promptID).SetPromptKind(domain.PromptKindIntentParticipation).
		SetName(name).SetProvider(provider).SetBotID(0).SetScopeType(domain.PromptScopeAccount).SetScopeID(0).SetVersion(latest + 1).
		SetStatus(domain.PromptStatusDraft).SetSource(source).SetSourceRef(sourceRef).
		SetHash(domain.PromptHash(content)).SetNote(note).SetContent(raw).Save(ctx)
	if err != nil {
		return domain.PromptRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.PromptRecord{}, err
	}
	return promptRecord(row), nil
}

// UpdateDraft replaces content on an editable draft only.
// UpdateDraft 只替换可编辑草稿的内容。
func (r *PromptsRepo) UpdateDraft(ctx context.Context, id int64, note string, content domain.Prompt) (domain.PromptRecord, error) {
	row, err := r.pool.Client.IntentPromptVersion.Query().Where(ipv.ID(id), ipv.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, err
	}
	if row.Status != domain.PromptStatusDraft {
		return domain.PromptRecord{}, ErrPromptImmutable
	}
	raw, err := json.Marshal(promptContentPayload(content))
	if err != nil {
		return domain.PromptRecord{}, err
	}
	hash := domain.PromptHash(content)
	update := r.pool.Client.IntentPromptVersion.UpdateOneID(id).
		Where(ipv.StatusEQ(domain.PromptStatusDraft), ipv.DeletedAtIsNil()).
		SetNote(note).SetHash(hash).SetContent(raw)
	if row.Source == domain.PromptSourceLangfuse && hash != row.Hash {
		update.SetSource(domain.PromptSourceLocal).SetSourceRef("")
	}
	updated, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, ErrPromptImmutable
	}
	if err != nil {
		return domain.PromptRecord{}, err
	}
	return promptRecord(updated), nil
}

// Publish archives the previous published version and activates this one atomically.
// Publish 原子地把旧发布版本归档并激活当前版本。
func (r *PromptsRepo) Publish(ctx context.Context, id int64) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	// Read the target without a row lock first so Publish and Rollback always
	// acquire the per-prompt transaction lock in the same order.
	initial, err := c.IntentPromptVersion.Query().Where(ipv.ID(id), ipv.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPromptNotFound
	}
	if err != nil {
		return err
	}
	if err := lockPromptLayerTx(ctx, c, initial); err != nil {
		return err
	}
	row, err := c.IntentPromptVersion.Query().Where(ipv.ID(id), ipv.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPromptNotFound
	}
	if err != nil {
		return err
	}
	if row.Status == domain.PromptStatusPublished {
		return tx.Commit()
	}
	if row.Status != domain.PromptStatusDraft && row.Status != domain.PromptStatusArchived {
		return ErrPromptImmutable
	}
	return publishPromptTx(ctx, tx, row)
}

// Rollback only republishes an archived version that was published before.
// Drafts are rejected even if they are the requested version; the currently
// published version is left untouched.
// Rollback 只重新发布曾发布过的归档版本；草稿即使被指定也拒绝，当前发布版本保持不变。
func (r *PromptsRepo) Rollback(ctx context.Context, promptID string, version int) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	initial, err := c.IntentPromptVersion.Query().
		Where(
			ipv.PromptID(promptID), ipv.PromptKind(domain.PromptKindIntentParticipation),
			ipv.BotID(0), ipv.ScopeType(domain.PromptScopeAccount), ipv.ScopeID(0),
			ipv.Version(version), ipv.DeletedAtIsNil(),
		).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPromptNotFound
	}
	if err != nil {
		return err
	}
	if err := lockPromptLayerTx(ctx, c, initial); err != nil {
		return err
	}
	row, err := c.IntentPromptVersion.Query().Where(ipv.ID(initial.ID), ipv.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPromptNotFound
	}
	if err != nil {
		return err
	}
	if row.Status != domain.PromptStatusArchived || row.PublishedAt == nil {
		return ErrPromptNotArchived
	}
	return publishPromptTx(ctx, tx, row)
}

func lockPromptTx(ctx context.Context, c *ent.Client, promptID string) error {
	_, err := c.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, promptID)
	return err
}

func publishPromptTx(ctx context.Context, tx *ent.Tx, row *ent.IntentPromptVersion) error {
	c := tx.Client()
	now := time.Now()
	if _, err := c.IntentPromptVersion.Update().
		Where(
			ipv.PromptID(row.PromptID), ipv.PromptKind(row.PromptKind), ipv.Provider(row.Provider),
			ipv.BotID(row.BotID), ipv.ScopeType(row.ScopeType), ipv.ScopeID(0),
			ipv.Status(domain.PromptStatusPublished),
		).
		SetStatus(domain.PromptStatusArchived).SetArchivedAt(now).Save(ctx); err != nil {
		return err
	}
	if err := c.IntentPromptVersion.UpdateOneID(row.ID).
		SetStatus(domain.PromptStatusPublished).SetPublishedAt(now).ClearArchivedAt().Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func promptRecord(row *ent.IntentPromptVersion) domain.PromptRecord {
	rec := domain.PromptRecord{
		ID: row.ID, PromptID: row.PromptID, Kind: row.PromptKind, Name: row.Name, Provider: row.Provider,
		BotID: row.BotID, ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		Version: row.Version, Status: row.Status, Source: row.Source, SourceRef: row.SourceRef,
		Hash: row.Hash, Note: row.Note, Content: append(json.RawMessage(nil), []byte(row.Content)...),
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
	}
	if row.PublishedAt != nil {
		rec.PublishedAt = row.PublishedAt.Format(time.RFC3339)
	}
	if row.ArchivedAt != nil {
		rec.ArchivedAt = row.ArchivedAt.Format(time.RFC3339)
	}
	return rec
}

func promptContentPayload(p domain.Prompt) map[string]any {
	out := map[string]any{"Provider": p.Provider}
	if p.SystemOne != nil {
		out["SystemOne"] = p.SystemOne
	}
	if p.OpenAI != nil {
		out["OpenAI"] = p.OpenAI
	}
	return out
}

func promptContentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
