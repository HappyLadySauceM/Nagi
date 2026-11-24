package infrastructure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	ipv "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentpromptversion"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/predicate"
)

// ActiveLayer returns the current published version for one editable layer.
func (r *PromptsRepo) ActiveLayer(ctx context.Context, key domain.PromptKey) (domain.PromptRecord, bool, error) {
	row, err := r.pool.Client.IntentPromptVersion.Query().
		Where(
			ipv.PromptID(key.PromptID), ipv.PromptKind(key.Kind), ipv.Provider(key.Provider),
			ipv.BotID(key.BotID), ipv.ScopeType(key.ScopeType), ipv.ScopeID(0),
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

// HasLayer reports whether one editable layer has any stored version.
// HasLayer 判断一个可编辑层是否已有任意历史版本。
func (r *PromptsRepo) HasLayer(ctx context.Context, key domain.PromptKey) (bool, error) {
	return r.pool.Client.IntentPromptVersion.Query().Where(
		ipv.PromptID(key.PromptID), ipv.PromptKind(key.Kind), ipv.Provider(key.Provider),
		ipv.BotID(key.BotID), ipv.ScopeType(key.ScopeType), ipv.ScopeID(0), ipv.DeletedAtIsNil(),
	).Exist(ctx)
}

// ListAll returns all stored versions for the unified admin view.
func (r *PromptsRepo) ListAll(ctx context.Context) ([]domain.PromptRecord, error) {
	rows, err := r.pool.Client.IntentPromptVersion.Query().Where(ipv.DeletedAtIsNil()).Order(ent.Desc(ipv.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PromptRecord, 0, len(rows))
	for _, row := range rows {
		if isRestoreBuiltinMarker(row) {
			continue
		}
		out = append(out, promptRecord(row))
	}
	return out, nil
}

// VersionsLayer returns all versions in one editable layer.
func (r *PromptsRepo) VersionsLayer(ctx context.Context, key domain.PromptKey) ([]domain.PromptRecord, error) {
	rows, err := r.pool.Client.IntentPromptVersion.Query().
		Where(
			ipv.PromptID(key.PromptID), ipv.PromptKind(key.Kind), ipv.Provider(key.Provider),
			ipv.BotID(key.BotID), ipv.ScopeType(key.ScopeType), ipv.ScopeID(0), ipv.DeletedAtIsNil(),
		).
		Order(ent.Desc(ipv.FieldVersion)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PromptRecord, 0, len(rows))
	for _, row := range rows {
		if isRestoreBuiltinMarker(row) {
			continue
		}
		out = append(out, promptRecord(row))
	}
	return out, nil
}

// CreateDraftVersion creates the next immutable draft in one layer.
func (r *PromptsRepo) CreateDraftVersion(ctx context.Context, input domain.PromptDraftInput) (domain.PromptRecord, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return domain.PromptRecord{}, err
	}
	defer tx.Rollback()
	c := tx.Client()
	lockKey := promptLayerLockKey(input.PromptID, input.BotID, input.ScopeType, input.Provider)
	if _, err := c.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return domain.PromptRecord{}, err
	}
	filters := []predicate.IntentPromptVersion{
		ipv.PromptID(input.PromptID), ipv.PromptKind(input.Kind), ipv.Provider(input.Provider),
		ipv.BotID(input.BotID), ipv.ScopeType(input.ScopeType), ipv.ScopeID(0),
	}
	hash := domain.PromptHash(input.Content)
	if input.Source == domain.PromptSourceLangfuse && strings.HasPrefix(input.SourceRef, domain.PromptVerifiedSourcePrefix) {
		rows, err := c.IntentPromptVersion.Query().Where(filters...).Where(ipv.SourceRef(input.SourceRef)).All(ctx)
		if err != nil {
			return domain.PromptRecord{}, err
		}
		var draft *ent.IntentPromptVersion
		for _, row := range rows {
			if row.Hash != hash {
				return domain.PromptRecord{}, domain.ErrPromptRemoteVersionConflict
			}
			if row.Status == domain.PromptStatusDraft && row.DeletedAt == nil {
				draft = row
			}
		}
		if draft != nil {
			return promptRecord(draft), nil
		}
	}
	var latest int
	if row, err := c.IntentPromptVersion.Query().Where(filters...).Order(ent.Desc(ipv.FieldVersion)).First(ctx); err == nil {
		latest = row.Version
	} else if !ent.IsNotFound(err) {
		return domain.PromptRecord{}, err
	}
	raw := domain.PromptContentJSON(input.Content)
	row, err := c.IntentPromptVersion.Create().
		SetPromptID(input.PromptID).SetPromptKind(input.Kind).SetName(input.Name).SetProvider(input.Provider).
		SetBotID(input.BotID).SetScopeType(input.ScopeType).SetScopeID(0).SetVersion(latest + 1).
		SetStatus(domain.PromptStatusDraft).SetSource(input.Source).SetSourceRef(input.SourceRef).
		SetHash(hash).SetNote(input.Note).SetContent(raw).Save(ctx)
	if err != nil {
		return domain.PromptRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.PromptRecord{}, err
	}
	return promptRecord(row), nil
}

// PublishVersion publishes one draft and returns the archived previous version id.
// PublishVersion 发布一个草稿并返回被归档的上一版本 id。
func (r *PromptsRepo) PublishVersion(ctx context.Context, id int64) (domain.PromptRecord, int64, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	defer tx.Rollback()
	c := tx.Client()
	initial, err := c.IntentPromptVersion.Query().Where(ipv.ID(id), ipv.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, 0, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	if err := lockPromptLayerTx(ctx, c, initial); err != nil {
		return domain.PromptRecord{}, 0, err
	}
	row, err := c.IntentPromptVersion.Query().Where(ipv.ID(id), ipv.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, 0, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	if row.Status != domain.PromptStatusDraft {
		return domain.PromptRecord{}, 0, ErrPromptImmutable
	}
	archivedID, err := publishLayeredPromptTx(ctx, tx, row)
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	updated, err := r.Get(ctx, id)
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	return updated, archivedID, nil
}

// RollbackVersion republishes one archived version in the exact editable layer.
// RollbackVersion 在精确可编辑层内重新发布一个归档版本。
func (r *PromptsRepo) RollbackVersion(ctx context.Context, key domain.PromptKey, versionID int64) (domain.PromptRecord, int64, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	defer tx.Rollback()
	c := tx.Client()
	initial, err := c.IntentPromptVersion.Query().Where(ipv.ID(versionID), ipv.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, 0, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	if !promptKeyMatchesRecord(key, initial) {
		return domain.PromptRecord{}, 0, ErrPromptNotFound
	}
	if err := lockPromptLayerTx(ctx, c, initial); err != nil {
		return domain.PromptRecord{}, 0, err
	}
	row, err := c.IntentPromptVersion.Query().Where(ipv.ID(versionID), ipv.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return domain.PromptRecord{}, 0, ErrPromptNotFound
	}
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	if row.Status != domain.PromptStatusArchived || row.PublishedAt == nil {
		return domain.PromptRecord{}, 0, ErrPromptNotArchived
	}
	archivedID, err := publishLayeredPromptTx(ctx, tx, row)
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	updated, err := r.Get(ctx, versionID)
	if err != nil {
		return domain.PromptRecord{}, 0, err
	}
	return updated, archivedID, nil
}

// RestoreBuiltin archives the current published version of one layer and
// clears the local binding. It never falls back to a bot_id=0 legacy row.
func (r *PromptsRepo) RestoreBuiltin(ctx context.Context, key domain.PromptKey) (int64, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	c := tx.Client()
	if err := lockPromptLayerKeyTx(ctx, c, key); err != nil {
		return 0, err
	}
	row, err := c.IntentPromptVersion.Query().Where(
		ipv.PromptID(key.PromptID), ipv.PromptKind(key.Kind), ipv.Provider(key.Provider),
		ipv.BotID(key.BotID), ipv.ScopeType(key.ScopeType), ipv.ScopeID(0),
		ipv.Status(domain.PromptStatusPublished), ipv.DeletedAtIsNil(),
	).ForUpdate().First(ctx)
	if ent.IsNotFound(err) {
		// If a legacy bot_id=0 prompt exists, record an archived builtin marker for
		// this bot layer. It prevents future resolution from falling back to the
		// legacy binding after an explicit restore-builtin request.
		legacy, legacyErr := c.IntentPromptVersion.Query().Where(
			ipv.PromptID(key.PromptID), ipv.PromptKind(key.Kind), ipv.Provider(key.Provider),
			ipv.BotID(0), ipv.ScopeType(domain.PromptScopeAccount), ipv.ScopeID(0),
			ipv.Status(domain.PromptStatusPublished), ipv.DeletedAtIsNil(),
		).Exist(ctx)
		if legacyErr != nil {
			return 0, legacyErr
		}
		if legacy && key.BotID != 0 {
			latest := 0
			if old, latestErr := c.IntentPromptVersion.Query().Where(
				ipv.PromptID(key.PromptID), ipv.PromptKind(key.Kind), ipv.Provider(key.Provider),
				ipv.BotID(key.BotID), ipv.ScopeType(key.ScopeType), ipv.ScopeID(0),
			).Order(ent.Desc(ipv.FieldVersion)).First(ctx); latestErr == nil {
				latest = old.Version
			} else if !ent.IsNotFound(latestErr) {
				return 0, latestErr
			}
			builtin := domain.BuiltinPromptFor(key.Kind, key.Provider)
			if _, createErr := c.IntentPromptVersion.Create().
				SetPromptID(key.PromptID).SetPromptKind(key.Kind).SetName(builtin.Name).SetProvider(key.Provider).
				SetBotID(key.BotID).SetScopeType(key.ScopeType).SetScopeID(0).SetVersion(latest + 1).
				SetStatus(domain.PromptStatusArchived).SetSource(domain.PromptSourceBuiltin).SetSourceRef("restore-builtin").
				SetHash(builtin.Hash).SetNote("restore-builtin").SetContent(domain.PromptContentJSON(builtin)).
				SetArchivedAt(time.Now()).Save(ctx); createErr != nil {
				return 0, createErr
			}
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	now := time.Now()
	if _, err := c.IntentPromptVersion.UpdateOneID(row.ID).SetStatus(domain.PromptStatusArchived).SetArchivedAt(now).Save(ctx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return row.ID, nil
}

// DeleteVersion soft-deletes one draft or archived local version. Published and
// builtin versions are protected, and deleted rows remain available through
// GetIncludingDeleted for historical snapshots.
// DeleteVersion 软删除一个草稿或归档的本地版本；已发布与内置版本受保护，
// 已删除行仍可通过 GetIncludingDeleted 读取以支持历史快照。
func (r *PromptsRepo) DeleteVersion(ctx context.Context, id, botID int64) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	initial, err := c.IntentPromptVersion.Query().Where(ipv.ID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPromptNotFound
	}
	if err != nil {
		return err
	}
	if initial.BotID != botID || initial.DeletedAt != nil {
		return ErrPromptNotFound
	}
	if err := lockPromptLayerTx(ctx, c, initial); err != nil {
		return err
	}
	row, err := c.IntentPromptVersion.Query().Where(ipv.ID(id)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPromptNotFound
	}
	if err != nil {
		return err
	}
	if row.BotID != botID || row.DeletedAt != nil {
		return ErrPromptNotFound
	}
	if row.Source == domain.PromptSourceBuiltin {
		return ErrPromptBuiltin
	}
	if row.Status == domain.PromptStatusPublished {
		return ErrPromptPublished
	}
	if row.Status != domain.PromptStatusDraft && row.Status != domain.PromptStatusArchived {
		return ErrPromptImmutable
	}
	if err := c.IntentPromptVersion.UpdateOneID(id).SetDeletedAt(time.Now()).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func isRestoreBuiltinMarker(row *ent.IntentPromptVersion) bool {
	return row.Source == domain.PromptSourceBuiltin && row.Note == "restore-builtin"
}

func promptLayerLockKey(promptID string, botID int64, scopeType, provider string) string {
	return fmt.Sprintf("%s|%d|%s|%s|0", promptID, botID, scopeType, provider)
}

func lockPromptLayerTx(ctx context.Context, c *ent.Client, row *ent.IntentPromptVersion) error {
	return lockPromptLayerKeyTx(ctx, c, domain.PromptKey{
		PromptID: row.PromptID, Kind: row.PromptKind, Provider: row.Provider,
		BotID: row.BotID, ScopeType: row.ScopeType,
	})
}

func lockPromptLayerKeyTx(ctx context.Context, c *ent.Client, key domain.PromptKey) error {
	_, err := c.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		promptLayerLockKey(key.PromptID, key.BotID, key.ScopeType, key.Provider))
	return err
}

func publishLayeredPromptTx(ctx context.Context, tx *ent.Tx, row *ent.IntentPromptVersion) (int64, error) {
	c := tx.Client()
	now := time.Now()
	archivedID := int64(0)
	if previous, err := c.IntentPromptVersion.Query().Where(
		ipv.PromptID(row.PromptID), ipv.PromptKind(row.PromptKind), ipv.Provider(row.Provider),
		ipv.BotID(row.BotID), ipv.ScopeType(row.ScopeType), ipv.ScopeID(0),
		ipv.Status(domain.PromptStatusPublished),
	).Order(ent.Desc(ipv.FieldVersion)).First(ctx); err == nil {
		archivedID = previous.ID
	} else if !ent.IsNotFound(err) {
		return 0, err
	}
	if _, err := c.IntentPromptVersion.Update().
		Where(
			ipv.PromptID(row.PromptID), ipv.PromptKind(row.PromptKind), ipv.Provider(row.Provider),
			ipv.BotID(row.BotID), ipv.ScopeType(row.ScopeType), ipv.ScopeID(0),
			ipv.Status(domain.PromptStatusPublished),
		).
		SetStatus(domain.PromptStatusArchived).SetArchivedAt(now).Save(ctx); err != nil {
		return 0, err
	}
	if err := c.IntentPromptVersion.UpdateOneID(row.ID).
		SetStatus(domain.PromptStatusPublished).SetPublishedAt(now).ClearArchivedAt().Exec(ctx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return archivedID, nil
}

func promptKeyMatchesRecord(key domain.PromptKey, row *ent.IntentPromptVersion) bool {
	return key.PromptID == row.PromptID && key.Kind == row.PromptKind && key.Provider == row.Provider &&
		key.BotID == row.BotID && key.ScopeType == row.ScopeType && key.ScopeID == row.ScopeID
}
