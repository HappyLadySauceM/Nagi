package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

var ErrLeaseLost = errors.New("outbound: lease lost")

func lockDelivery(ctx context.Context, tx *db.Tx, id, claim string) error {
	var conversation string
	if err := tx.QueryRow(ctx, `SELECT c.id FROM conversations c JOIN agent_outbound_messages o ON o.conversation_id=c.id WHERE o.id=$1 FOR UPDATE OF c`, id).Scan(&conversation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRecordNotFound
		}
		return err
	}
	var job int64
	if err := tx.QueryRow(ctx, `SELECT j.id FROM message_jobs j JOIN agent_outbound_messages o ON o.job_id=j.id WHERE o.id=$1 AND j.status='processing' AND j.claim_token=$2::uuid AND o.claim_token=j.claim_token AND j.lease_expires_at>clock_timestamp() FOR UPDATE OF j`, id, claim).Scan(&job); err != nil {
		return ErrLeaseLost
	}
	return nil
}
