package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

var ErrLeaseLost = errors.New("outbound: lease lost")

func lockDelivery(ctx context.Context, tx *db.Tx, id, claim string, includeTarget ...bool) error {
	target := len(includeTarget) > 0 && includeTarget[0]
	return lockDeliveryRows(ctx, tx, id, claim, target, nil)
}

func lockDispatch(ctx context.Context, tx *db.Tx, id, claim string, reads []memory.ConsentRead) error {
	proposals := make([]string, 0, len(reads))
	for _, read := range reads {
		parts := strings.Split(read.Identity, ":")
		if len(parts) != 4 {
			return domain.ErrConsentSendDenied
		}
		proposals = append(proposals, parts[2])
	}
	if err := lockDeliveryRows(ctx, tx, id, claim, true, proposals); err != nil {
		return err
	}
	var record string
	return tx.QueryRow(ctx, `SELECT id::text FROM agent_outbound_messages WHERE id=$1 AND claim_token=$2::uuid FOR UPDATE`, id, claim).Scan(&record)
}

func lockDeliveryRows(ctx context.Context, tx *db.Tx, id, claim string, target bool, proposals []string) error {
	if proposals == nil {
		proposals = []string{}
	}
	encoded, _ := json.Marshal(proposals)
	// Lock both conversations in a stable order for reciprocal cross-target sends.
	// 跨目标互发时按固定顺序锁定两个会话，避免历史写入的外键锁互相等待。
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c JOIN agent_outbound_messages o ON c.id=o.conversation_id OR ($2 AND c.bot_id=o.bot_id AND c.kind=o.target_kind AND c.target_id=o.target_id)
	 OR c.id IN (SELECT p.source_conversation_id FROM memory_consent_proposals p WHERE p.bot_id=o.bot_id AND p.id::text IN (SELECT value FROM jsonb_array_elements_text($3::jsonb)))
	 WHERE o.id=$1 ORDER BY c.id FOR UPDATE OF c`, id, target, encoded)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var conversation string
		if err := rows.Scan(&conversation); err != nil {
			rows.Close()
			return err
		}
		found = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrRecordNotFound
	}
	var job int64
	if err := tx.QueryRow(ctx, `SELECT j.id FROM message_jobs j JOIN agent_outbound_messages o ON o.job_id=j.id WHERE o.id=$1 AND j.status='processing' AND j.claim_token=$2::uuid AND o.claim_token=j.claim_token AND j.lease_expires_at>clock_timestamp() FOR UPDATE OF j`, id, claim).Scan(&job); err != nil {
		return ErrLeaseLost
	}
	return nil
}
