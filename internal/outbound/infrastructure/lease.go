package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

var ErrLeaseLost = errors.New("outbound: lease lost")

var errDeliveryScopeChanged = errors.New("outbound: delivery scope changed while locking")

type deliveryPolicyScope struct {
	botID          int64
	sourceID       string
	sourceKind     string
	sourceTarget   int64
	targetKind     string
	targetID       int64
	hasMemoryReads bool
	derived        bool
	control        bool
}

func loadDeliveryPolicyScope(ctx context.Context, tx *db.Tx, id string) (deliveryPolicyScope, error) {
	var scope deliveryPolicyScope
	err := tx.QueryRow(ctx, `SELECT o.bot_id,c.id,c.kind,c.target_id,o.target_kind,o.target_id,COALESCE(jsonb_array_length(NULLIF(o.memory_dependencies,'null'::jsonb)),0)>0,
 o.payload @> '{"cross_group_fact":true}'::jsonb, j.source_kind='memory_consent_prompt' AND j.consent_proposal_id IS NOT NULL AND COALESCE(o.tool_call_id,'')='memory_consent_prompt'
 FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN conversations c ON c.id=o.conversation_id AND c.bot_id=o.bot_id
 WHERE o.id=$1`, id).Scan(&scope.botID, &scope.sourceID, &scope.sourceKind, &scope.sourceTarget, &scope.targetKind, &scope.targetID, &scope.hasMemoryReads, &scope.derived, &scope.control)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, domain.ErrRecordNotFound
	}
	return scope, err
}

func lockScopedDelivery(ctx context.Context, tx *db.Tx, id, claim string) error {
	_, err := lockScopedDeliveryConversations(ctx, tx, id)
	if err != nil {
		return err
	}
	return lockDeliveryJob(ctx, tx, id, claim)
}

func lockScopedDeliveryConversations(ctx context.Context, tx *db.Tx, id string, proposals ...string) (deliveryPolicyScope, error) {
	scope, err := loadDeliveryPolicyScope(ctx, tx, id)
	if err != nil {
		return scope, err
	}
	if scope.hasMemoryReads || scope.derived || scope.control {
		if err := memoryinfra.LockMemoryMutation(ctx, tx, scope.botID); err != nil {
			return scope, err
		}
	}
	if err := contracts.LockPolicies(ctx, tx, false,
		contracts.PolicyLockKey{BotID: scope.botID, Kind: scope.sourceKind},
		contracts.PolicyLockKey{BotID: scope.botID, Kind: scope.targetKind}); err != nil {
		return scope, err
	}
	// Take the target's unique key before locking conversations by ID.
	// 按会话 ID 取行锁前先串行化目标会话的唯一键。
	if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,bot_id,kind,target_id)
 VALUES(gen_random_uuid(),$1,$2,$3) ON CONFLICT(bot_id,kind,target_id) DO NOTHING`, scope.botID, scope.targetKind, scope.targetID); err != nil {
		return scope, err
	}
	if err := lockDeliveryConversations(ctx, tx, id, true, proposals...); err != nil {
		return scope, err
	}
	current, err := loadDeliveryPolicyScope(ctx, tx, id)
	if err != nil {
		return scope, err
	}
	if current != scope {
		return scope, errDeliveryScopeChanged
	}
	return scope, nil
}

func lockDelivery(ctx context.Context, tx *db.Tx, id, claim string, includeTarget ...bool) error {
	target := len(includeTarget) > 0 && includeTarget[0]
	if err := lockDeliveryConversations(ctx, tx, id, target); err != nil {
		return err
	}
	return lockDeliveryJob(ctx, tx, id, claim)
}

func lockDeliveryConversations(ctx context.Context, tx *db.Tx, id string, target bool, proposals ...string) error {
	if proposals == nil {
		proposals = []string{}
	}
	encoded, err := json.Marshal(proposals)
	if err != nil {
		return err
	}
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
	return nil
}

func lockDeliveryJob(ctx context.Context, tx *db.Tx, id, claim string) error {
	var job int64
	if err := tx.QueryRow(ctx, `SELECT j.id FROM message_jobs j JOIN agent_outbound_messages o ON o.job_id=j.id WHERE o.id=$1 AND j.status='processing' AND j.claim_token=$2::uuid AND o.claim_token=j.claim_token AND j.lease_expires_at>clock_timestamp() FOR UPDATE OF j`, id, claim).Scan(&job); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	return nil
}

func consentProposals(reads []memory.ConsentRead) ([]string, error) {
	proposals := make([]string, 0, len(reads))
	for _, read := range reads {
		parts := strings.Split(read.Identity, ":")
		if len(parts) != 4 || parts[2] == "" {
			return nil, domain.ErrConsentSendDenied
		}
		proposals = append(proposals, parts[2])
	}
	return proposals, nil
}

func lockConsentAuthorization(ctx context.Context, tx *db.Tx, scope deliveryPolicyScope) error {
	return scopeinfra.LockMemoryAuthorization(ctx, tx, scope.botID, scope.sourceKind, scope.sourceTarget)
}
