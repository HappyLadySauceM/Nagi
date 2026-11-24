package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

var ErrLeaseLost = errors.New("conversation: lease lost")

func claimValue(tokens []string) any {
	if len(tokens) != 1 || tokens[0] == "" {
		return nil
	}
	return tokens[0]
}

// RenewClaims renews only the still-owned, unexpired claims and returns their tokens.
// RenewClaims 仅续租仍由调用方持有且未过期的任务，并返回续租成功的令牌。
func (r *Repo) RenewClaims(ctx context.Context, tokens []string) ([]string, error) {
	if len(tokens) == 0 {
		return []string{}, nil
	}
	data, _ := json.Marshal(tokens)
	rows, err := r.pool.Query(ctx, `UPDATE message_jobs SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE status='processing' AND lease_expires_at>clock_timestamp() AND claim_token IN(SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb)) RETURNING claim_token`, data)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var renewed []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		renewed = append(renewed, token)
	}
	return renewed, rows.Err()
}

// lockClaim serializes claim-sensitive transitions with sending and recovery.
// lockClaim 使认领状态迁移与发送、恢复按一致顺序串行执行。
func (r *Repo) lockClaim(ctx context.Context, tx *db.Tx, jobID int64, token any) error {
	var conversation string
	if err := tx.QueryRow(ctx, `SELECT c.id FROM message_jobs j LEFT JOIN messages m ON m.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id JOIN conversations c ON c.id=COALESCE(m.conversation_id,p.source_conversation_id) WHERE j.id=$1 FOR UPDATE OF c`, jobID).Scan(&conversation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM message_jobs WHERE id=$1 AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp() FOR UPDATE`, jobID, token).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	return nil
}

func failurePolicyKeys(ctx context.Context, tx *db.Tx, jobID int64) ([]scopecontracts.PolicyLockKey, error) {
	rows, err := tx.Query(ctx, `SELECT c.bot_id,c.kind
 FROM message_jobs j LEFT JOIN messages m ON m.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id
 JOIN conversations c ON c.id=COALESCE(m.conversation_id,p.source_conversation_id) WHERE j.id=$1
 UNION SELECT o.bot_id,o.target_kind FROM agent_outbound_messages o WHERE o.job_id=$1
 ORDER BY 1,2`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []scopecontracts.PolicyLockKey
	for rows.Next() {
		var key scopecontracts.PolicyLockKey
		if err := rows.Scan(&key.BotID, &key.Kind); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func failureConversations(ctx context.Context, tx *db.Tx, jobID int64, lock bool) ([]string, error) {
	query := `SELECT c.id FROM conversations c WHERE c.id IN (
 SELECT COALESCE(m.conversation_id,p.source_conversation_id) FROM message_jobs j LEFT JOIN messages m ON m.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id WHERE j.id=$1
 ) OR EXISTS (SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=$1
 AND o.bot_id=c.bot_id AND o.target_kind=c.kind AND o.target_id=c.target_id) ORDER BY c.id`
	if lock {
		query += ` FOR UPDATE OF c`
	}
	rows, err := tx.Query(ctx, query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repo) lockFailure(ctx context.Context, tx *db.Tx, jobID int64, claim any) ([]string, error) {
	keys, err := failurePolicyKeys(ctx, tx, jobID)
	if err != nil {
		return nil, err
	}
	if err := scopecontracts.LockPolicies(ctx, tx, false, keys...); err != nil {
		return nil, err
	}
	ids, err := failureConversations(ctx, tx, jobID, true)
	if err != nil {
		return nil, err
	}
	if err := r.lockClaim(ctx, tx, jobID, claim); err != nil {
		return nil, err
	}
	currentKeys, err := failurePolicyKeys(ctx, tx, jobID)
	if err != nil {
		return nil, err
	}
	currentIDs, err := failureConversations(ctx, tx, jobID, false)
	if err != nil {
		return nil, err
	}
	// A reservation that committed during lock acquisition may add a new lock key.
	// 取锁期间提交的新预留可能增加锁键。
	if !slices.Equal(currentKeys, keys) || !slices.Equal(currentIDs, ids) {
		return nil, errFailureScopeChanged
	}
	return ids, nil
}

var errFailureScopeChanged = errors.New("conversation: delivery scope changed while locking failure")
