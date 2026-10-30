// Package infrastructure persists the runtime-editable settings row.
// Package infrastructure 持久化运行时可编辑的配置行。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

// Repo reads and writes the single system_settings row.
// Repo 负责读写唯一的 system_settings 行。
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds the repository; a nil pool is a programming error.
// NewRepo 构建仓储；pool 为 nil 属于装配错误。
func NewRepo(pool *pgxpool.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("settings: pool required")
	}
	return &Repo{pool: pool}, nil
}

// Load reads the stored revision; the boolean reports whether it exists.
// Load 读取已存储的修订；布尔值表示是否存在。
func (r *Repo) Load(ctx context.Context) (settingsdomain.Revision, bool, error) {
	var (
		revision settingsdomain.Revision
		payload  []byte
	)
	err := r.pool.QueryRow(ctx, `
		SELECT payload, version, updated_at, updated_by FROM system_settings WHERE id = 1
	`).Scan(&payload, &revision.Version, &revision.UpdatedAt, &revision.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return settingsdomain.Revision{}, false, nil
	}
	if err != nil {
		return settingsdomain.Revision{}, false, fmt.Errorf("settings: load: %w", err)
	}
	if err := json.Unmarshal(payload, &revision.Settings); err != nil {
		return settingsdomain.Revision{}, false, fmt.Errorf("settings: decode payload: %w", err)
	}
	return revision, true, nil
}

// Insert seeds the row when it is still missing; a concurrent seeder wins and
// its revision is returned instead of failing the boot.
// Insert 在行仍缺失时写入初值；并发的初始化者获胜，
// 返回其修订而不是让启动失败。
func (r *Repo) Insert(ctx context.Context, settings settingsdomain.Settings, actor string) (settingsdomain.Revision, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return settingsdomain.Revision{}, fmt.Errorf("settings: encode payload: %w", err)
	}
	var (
		revision settingsdomain.Revision
		stored   []byte
	)
	err = r.pool.QueryRow(ctx, `
		INSERT INTO system_settings (id, payload, version, updated_at, updated_by)
		VALUES (1, $1, 1, now(), $2)
		ON CONFLICT (id) DO NOTHING
		RETURNING payload, version, updated_at, updated_by
	`, payload, actor).Scan(&stored, &revision.Version, &revision.UpdatedAt, &revision.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, _, loadErr := r.Load(ctx)
		return existing, loadErr
	}
	if err != nil {
		return settingsdomain.Revision{}, fmt.Errorf("settings: insert: %w", err)
	}
	if err := json.Unmarshal(stored, &revision.Settings); err != nil {
		return settingsdomain.Revision{}, fmt.Errorf("settings: decode payload: %w", err)
	}
	return revision, nil
}

// Update writes a new revision when the expected version still matches. The
// boolean is false on a version conflict, which the caller maps to 409.
// Update 在期望版本仍匹配时写入新修订；
// 版本冲突时布尔值为 false，调用方据此返回 409。
func (r *Repo) Update(ctx context.Context, settings settingsdomain.Settings, expected int64, actor string) (settingsdomain.Revision, bool, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return settingsdomain.Revision{}, false, fmt.Errorf("settings: encode payload: %w", err)
	}
	var (
		revision settingsdomain.Revision
		stored   []byte
	)
	err = r.pool.QueryRow(ctx, `
		UPDATE system_settings
		SET payload = $1, version = version + 1, updated_at = now(), updated_by = $2
		WHERE id = 1 AND version = $3
		RETURNING payload, version, updated_at, updated_by
	`, payload, actor, expected).Scan(&stored, &revision.Version, &revision.UpdatedAt, &revision.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return settingsdomain.Revision{}, false, nil
	}
	if err != nil {
		return settingsdomain.Revision{}, false, fmt.Errorf("settings: update: %w", err)
	}
	if err := json.Unmarshal(stored, &revision.Settings); err != nil {
		return settingsdomain.Revision{}, false, fmt.Errorf("settings: decode payload: %w", err)
	}
	return revision, true, nil
}

// Version reads only the revision number, which is cheap enough to poll.
// Version 只读取修订号，开销足够小，可用于轮询。
func (r *Repo) Version(ctx context.Context) (int64, bool, error) {
	var version int64
	err := r.pool.QueryRow(ctx, `SELECT version FROM system_settings WHERE id = 1`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("settings: version: %w", err)
	}
	return version, true, nil
}
