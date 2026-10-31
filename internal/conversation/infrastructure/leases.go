package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	if err := tx.QueryRow(ctx, `SELECT c.id FROM conversations c JOIN messages m ON m.conversation_id=c.id JOIN message_jobs j ON j.incoming_message_id=m.id WHERE j.id=$1 FOR UPDATE OF c`, jobID).Scan(&conversation); err != nil {
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
