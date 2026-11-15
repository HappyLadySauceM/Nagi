package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// PersistExtractionPromptSnapshot stores one immutable prompt snapshot on every
// job in a claimed batch before the model call. Existing identical snapshots
// are preserved; conflicting snapshots fail explicitly.
// PersistExtractionPromptSnapshot 在模型调用前把一个不可变 Prompt 快照写入批次内
// 每个已认领任务。已有相同快照保持不变；冲突快照明确失败。
func (r *Repo) PersistExtractionPromptSnapshot(ctx context.Context, batch domain.ExtractionBatch, snapshot domain.PromptSnapshot) error {
	if len(batch.Jobs) == 0 {
		return fmt.Errorf("memory extraction: prompt snapshot requires jobs")
	}
	if err := validatePromptSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.BotID != batch.Scope.BotID || snapshot.TargetType != batch.Scope.ScopeType ||
		snapshot.TargetID != batch.Scope.ScopeID || snapshot.ConversationID != batch.Scope.ConversationID {
		return fmt.Errorf("memory extraction: prompt snapshot target does not match batch")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("memory extraction: marshal prompt snapshot: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, job := range batch.Jobs {
		var payload []byte
		var status string
		var claimToken sql.NullString
		var leaseExpiresAt sql.NullTime
		err := tx.QueryRow(ctx, `
			SELECT extraction_payload, status, claim_token::text, lease_expires_at
			FROM memory_extraction_jobs
			WHERE id=$1::uuid
			FOR UPDATE`, job.ID).Scan(&payload, &status, &claimToken, &leaseExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return memoryapp.ErrExtractionLeaseLost
		}
		if err != nil {
			return err
		}
		if status != "processing" || !claimToken.Valid || claimToken.String != batch.ClaimToken || !leaseExpiresAt.Valid || !leaseExpiresAt.Time.After(time.Now()) {
			return memoryapp.ErrExtractionLeaseLost
		}
		if len(payload) > 0 {
			existing, err := decodePromptSnapshot(payload)
			if err != nil {
				return fmt.Errorf("memory extraction: existing prompt snapshot invalid: %w", err)
			}
			if !reflect.DeepEqual(existing, snapshot) {
				return fmt.Errorf("memory extraction: prompt snapshot conflict")
			}
			continue
		}
		result, err := tx.Exec(ctx, `
			UPDATE memory_extraction_jobs
			SET extraction_payload=$1::jsonb, updated_at=clock_timestamp()
			WHERE id=$2::uuid AND status='processing' AND claim_token=$3::uuid`, raw, job.ID, batch.ClaimToken)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return memoryapp.ErrExtractionLeaseLost
		}
	}
	return tx.Commit(ctx)
}

func decodePromptSnapshot(raw []byte) (domain.PromptSnapshot, error) {
	var snapshot domain.PromptSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return domain.PromptSnapshot{}, fmt.Errorf("memory extraction: decode prompt snapshot: %w", err)
	}
	if err := validatePromptSnapshot(snapshot); err != nil {
		return domain.PromptSnapshot{}, err
	}
	return snapshot, nil
}

func validatePromptSnapshot(snapshot domain.PromptSnapshot) error {
	if strings.TrimSpace(snapshot.PromptID) == "" || strings.TrimSpace(snapshot.Kind) == "" || strings.TrimSpace(snapshot.Provider) == "" {
		return fmt.Errorf("memory extraction: prompt snapshot prompt id/kind/provider required")
	}
	if snapshot.BotID <= 0 || snapshot.TargetID <= 0 || strings.TrimSpace(snapshot.TargetType) == "" {
		return fmt.Errorf("memory extraction: prompt snapshot target required")
	}
	if strings.TrimSpace(snapshot.FinalHash) == "" || strings.TrimSpace(snapshot.RenderedHash) == "" {
		return fmt.Errorf("memory extraction: prompt snapshot hashes required")
	}
	if len(snapshot.Layers) == 0 {
		return fmt.Errorf("memory extraction: prompt snapshot layers required")
	}
	return nil
}
