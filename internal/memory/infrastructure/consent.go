package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
)

// consentFacts ties a version to an approved fact with a direct source from its subject.
// consentFacts 将事实版本绑定到已审批且来自本人直接入站的事实。
const consentFacts = ` JOIN messages src ON src.id=m.source_message_id AND src.bot_id=m.bot_id AND src.direction='incoming'
 JOIN conversations c ON c.id=src.conversation_id AND c.bot_id=m.bot_id AND c.kind='group'
 WHERE m.bot_id=$1 AND m.memory_id=$2::uuid AND m.authorization_version=$3
 AND m.status='active' AND m.approved_by<>'' AND (m.expires_at IS NULL OR m.expires_at>clock_timestamp())
 AND m.subject_type='user' AND src.sender_id>0 AND src.sender_id<>m.bot_id
 AND src.platform_message_id<>0 AND src.control_kind='' AND m.subject_id=src.sender_id::text AND m.source_sender_id=src.sender_id
 AND ((m.visibility='group' AND m.owner_key=c.target_id::text) OR (m.visibility='conversation' AND m.owner_key=c.id::text))`

const consentColumns = `p.id::text,p.bot_id,p.memory_id::text,p.fact_version,p.subject_id,p.source_conversation_id::text,p.source_group_id,p.target_groups,p.status,p.state_version,p.created_at,p.expires_at`

func readConsent(row rowScanner) (contracts.ConsentProposal, error) {
	var p contracts.ConsentProposal
	var targets []byte
	err := row.Scan(&p.ID, &p.BotID, &p.MemoryID, &p.FactVersion, &p.SubjectID, &p.SourceConversationID, &p.SourceGroupID, &targets, &p.Status, &p.StateVersion, &p.CreatedAt, &p.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, contracts.ErrConsentMissing
	}
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(targets, &p.TargetGroups)
}

func (r *Repo) CreateConsent(ctx context.Context, in contracts.CreateConsent) (contracts.ConsentProposal, error) {
	if in.BotID <= 0 || in.FactVersion <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 128 || len(in.TargetGroups) == 0 || len(in.TargetGroups) > 100 {
		return contracts.ConsentProposal{}, contracts.ErrConsentInvalid
	}
	factID, err := uuid.Parse(in.MemoryID)
	if err != nil {
		return contracts.ConsentProposal{}, contracts.ErrConsentInvalid
	}
	in.MemoryID = factID.String()
	targets := slices.Clone(in.TargetGroups)
	slices.Sort(targets)
	targets = slices.Compact(targets)
	if targets[0] <= 0 {
		return contracts.ConsentProposal{}, contracts.ErrConsentInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return contracts.ConsentProposal{}, err
	}
	defer tx.Rollback(ctx)
	if err = LockMemoryMutation(ctx, tx, in.BotID); err != nil {
		return contracts.ConsentProposal{}, err
	}
	old, err := readConsent(tx.QueryRow(ctx, `SELECT `+consentColumns+` FROM memory_consent_proposals p WHERE p.bot_id=$1 AND p.idempotency_key=$2`, in.BotID, in.IdempotencyKey))
	if err == nil {
		if old.MemoryID != in.MemoryID || old.FactVersion != in.FactVersion || !slices.Equal(old.TargetGroups, targets) {
			return old, contracts.ErrConsentConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(err, contracts.ErrConsentMissing) {
		return old, err
	}
	var content, conversation string
	var subject, group, source int64
	err = tx.QueryRow(ctx, `SELECT m.content,c.id::text,c.target_id,src.sender_id,src.id FROM memories m`+consentFacts+` FOR UPDATE OF m`, in.BotID, in.MemoryID, in.FactVersion).Scan(&content, &conversation, &group, &subject, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return old, contracts.ErrConsentConflict
	}
	if err != nil {
		return old, err
	}
	if slices.Contains(targets, group) {
		return old, contracts.ErrConsentInvalid
	}
	encoded, _ := json.Marshal(targets)
	if err = scopeinfra.LockMemoryAuthorization(ctx, tx, in.BotID, "group", group); err != nil {
		return old, err
	}
	defaultCanSend, err := consentDefaultCanSend(ctx, tx)
	if err != nil {
		return old, err
	}
	var sourceLimit, totalLimit int
	var sourceEnabled bool
	err = tx.QueryRow(ctx, `SELECT max_chars_per_message,max_total_chars_per_run,enabled AND store_messages AND reply_mode<>'observe_only' AND COALESCE((tool_permissions->>'qq.reply.current')::boolean,$3)
 FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id IN($2,0) ORDER BY scope_id DESC LIMIT 1`, in.BotID, group, defaultCanSend).Scan(&sourceLimit, &totalLimit, &sourceEnabled)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !sourceEnabled {
		return old, contracts.ErrConsentConflict
	}
	if err != nil {
		return old, err
	}
	for _, limit := range []int{sourceLimit, totalLimit} {
		if limit > 0 {
			in.MaxPromptRunes = min(in.MaxPromptRunes, limit)
		}
	}
	var known int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM qq_groups WHERE bot_id=$1 AND (group_id=$3 OR group_id IN(SELECT value::bigint FROM jsonb_array_elements_text($2::jsonb)))`, in.BotID, encoded, group).Scan(&known)
	if err != nil {
		return old, err
	}
	if known != len(targets)+1 {
		return old, contracts.ErrConsentInvalid
	}
	id := uuid.NewString()
	prompt := domain.ConsentPrompt(id, content, targets)
	if in.MaxPromptRunes <= 0 || utf8.RuneCountInString(prompt) > in.MaxPromptRunes {
		return old, contracts.ErrConsentInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO memory_consent_proposals(id,bot_id,memory_id,fact_version,subject_id,source_conversation_id,source_group_id,source_message_id,target_groups,display_digest,idempotency_key,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,clock_timestamp()+interval '24 hours')`, id, in.BotID, in.MemoryID, in.FactVersion, subject, conversation, group, source, encoded, domain.ConsentPromptDigest(prompt), in.IdempotencyKey); err != nil {
		return old, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message_jobs(source_kind,consent_proposal_id) VALUES('memory_consent_prompt',$1)`, id); err != nil {
		return old, err
	}
	p, err := readConsent(tx.QueryRow(ctx, `SELECT `+consentColumns+` FROM memory_consent_proposals p WHERE p.id=$1`, id))
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func (r *Repo) Consent(ctx context.Context, bot int64, id string) (contracts.ConsentProposal, error) {
	p, err := readConsent(r.pool.QueryRow(ctx, `SELECT `+consentColumns+` FROM memory_consent_proposals p WHERE p.bot_id=$1 AND p.id=$2`, bot, id))
	if err != nil {
		return p, err
	}
	err = r.pool.QueryRow(ctx, `SELECT COALESCE((SELECT o.status FROM message_jobs j JOIN agent_outbound_messages o ON o.job_id=j.id WHERE j.consent_proposal_id=$1 ORDER BY o.sequence DESC LIMIT 1),''),COALESCE((SELECT o.platform_message_id FROM message_jobs j JOIN agent_outbound_messages o ON o.job_id=j.id WHERE j.consent_proposal_id=$1 AND o.status='sent' LIMIT 1),0)`, id).Scan(&p.DeliveryStatus, &p.PlatformMessageID)
	if err != nil {
		return p, err
	}
	p.ActiveGroups, err = r.consentGroups(ctx, p.ID)
	return p, err
}

func (r *Repo) RevokeConsent(ctx context.Context, bot int64, id string, expected, target int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = LockMemoryMutation(ctx, tx, bot); err != nil {
		return err
	}
	p, err := readConsent(tx.QueryRow(ctx, `SELECT `+consentColumns+` FROM memory_consent_proposals p WHERE p.bot_id=$1 AND p.id=$2 FOR UPDATE`, bot, id))
	if err != nil {
		return err
	}
	if p.StateVersion != expected {
		return contracts.ErrConsentConflict
	}
	if err = revokeConsent(ctx, tx, p, target, "administrator"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func revokeConsent(ctx context.Context, tx sqlExecutor, p contracts.ConsentProposal, target int64, actor string) error {
	if target != 0 && !slices.Contains(p.TargetGroups, target) {
		return contracts.ErrConsentInvalid
	}
	if p.Status == "pending" && actor == "administrator" && target == 0 {
		_, err := tx.ExecContext(ctx, `UPDATE memory_consent_proposals SET status='cancelled',state_version=state_version+1 WHERE id=$1`, p.ID)
		return err
	}
	pending := p.Status == "pending"
	if pending {
		target = 0
	}

	targets := p.TargetGroups
	if target != 0 {
		targets = []int64{target}
	}
	encoded, _ := json.Marshal(targets)
	_, err := tx.ExecContext(ctx, `UPDATE memory_consent_grants SET revoked_at=clock_timestamp(),revocation_source=$1,state_version=state_version+1 WHERE bot_id=$2 AND memory_id=$3 AND fact_version=$4 AND subject_id=$5 AND revoked_at IS NULL AND $7::boolean AND target_group_id IN (SELECT value::bigint FROM jsonb_array_elements_text($6::jsonb))`, actor, p.BotID, p.MemoryID, p.FactVersion, p.SubjectID, encoded, !pending)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE memory_consent_proposals q SET status=CASE WHEN q.id=$1 AND q.status='pending' THEN 'cancelled' WHEN q.status='pending' THEN 'invalidated' ELSE q.status END,state_version=state_version+1 WHERE q.bot_id=$2 AND q.memory_id=$3 AND q.fact_version=$4 AND q.subject_id=$5 AND (q.id=$1 OR (q.status='pending' AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(q.target_groups) a JOIN jsonb_array_elements_text($6::jsonb) b ON a.value=b.value)))`, p.ID, p.BotID, p.MemoryID, p.FactVersion, p.SubjectID, encoded)
	return err
}

// consentDelivery requires the ledger receipt and target history to identify one prompt.
// consentDelivery 要求出站台账回执与目标历史指向同一条确认提示。
const consentDelivery = ` JOIN message_jobs j ON j.consent_proposal_id=p.id AND j.source_kind='memory_consent_prompt'
 JOIN agent_outbound_messages o ON o.job_id=j.id AND o.status='sent' AND o.platform_message_id<>0 AND o.tool_call_id='memory_consent_prompt' AND o.bot_id=p.bot_id AND o.conversation_id=p.source_conversation_id AND o.target_kind='group' AND o.target_id=p.source_group_id
 JOIN messages h ON h.outbound_id=o.id AND h.platform_message_id=o.platform_message_id AND h.bot_id=p.bot_id AND h.conversation_id=p.source_conversation_id AND h.direction='outgoing' AND h.sent_status='sent' AND h.control_kind='memory_consent_prompt' AND h.sender_id=p.bot_id AND h.text_content=o.text_content AND encode(sha256(convert_to(o.text_content,'UTF8')),'hex')=p.display_digest`

// LockConsentIngress locks the account before the ingress conversation.
// LockConsentIngress 在入站会话锁之前锁定账号。
func (r *Repo) LockConsentIngress(ctx context.Context, tx *ent.Tx, bot int64) error {
	return LockMemoryMutation(ctx, tx.Client(), bot)
}

// LockConsentCleanup serializes credential removal with committed dispatch decisions.
// LockConsentCleanup 将凭据清理与派发决定按提交顺序串行化。
func (r *Repo) LockConsentCleanup(ctx context.Context, tx *db.Tx, bot int64) error {
	return LockMemoryMutation(ctx, tx, bot)
}

// ConsumeStoredConsent handles an ingress message atomically with its storage.
// ConsumeStoredConsent 与入站消息的持久化在同一事务处理授权操作。
func (r *Repo) ConsumeStoredConsent(ctx context.Context, tx *ent.Tx, bot int64, conversation string, message int64, enabled bool) (contracts.ConsentResult, error) {
	client := tx.Client()
	q := consentTx{client}
	var sender, platform int64
	var encoded []byte
	err := q.row(ctx, `SELECT m.sender_id,m.platform_message_id,m.segments FROM messages m JOIN conversations c ON c.id=m.conversation_id AND c.bot_id=m.bot_id AND c.kind='group' WHERE m.id=$1 AND m.bot_id=$2 AND m.conversation_id=$3 AND m.direction='incoming' AND m.control_kind='memory_consent_command' AND m.platform_message_id<>0 AND m.sender_id>0 AND m.sender_id<>m.bot_id`, message, bot, conversation).scan(&sender, &platform, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.ConsentResult{}, nil
	}
	if err != nil {
		return contracts.ConsentResult{}, err
	}
	return consumeConsent(ctx, q, bot, conversation, message, sender, platform, encoded, enabled)
}

type consentTx struct{ *ent.Client }
type consentRows struct{ open func() (*sql.Rows, error) }

func (q consentTx) row(ctx context.Context, sqlText string, args ...any) consentRows {
	return consentRows{open: func() (*sql.Rows, error) { return q.QueryContext(ctx, sqlText, args...) }}
}
func (r consentRows) Scan(args ...any) error { return r.scan(args...) }
func (r consentRows) scan(args ...any) error {
	rows, err := r.open()
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err := rows.Scan(args...); err != nil {
		return err
	}
	if rows.Next() {
		return contracts.ErrConsentConflict
	}
	return rows.Err()
}

func consentReceiptLocks(reference int64) string {
	if reference != 0 {
		return " FOR SHARE OF o,h"
	}
	return ""
}

func consumeConsent(ctx context.Context, q consentTx, bot int64, conversation string, message, sender, platform int64, encoded []byte, enabled bool) (contracts.ConsentResult, error) {
	var result contracts.ConsentResult
	err := q.row(ctx, `SELECT e.outcome,p.id::text,p.status,p.state_version FROM memory_consent_events e JOIN memory_consent_proposals p ON p.id=e.proposal_id WHERE e.bot_id=$1 AND e.conversation_id=$2 AND e.platform_message_id=$3`, bot, conversation, platform).scan(&result.Outcome, &result.ProposalID, &result.Status, &result.StateVersion)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	cmd, valid := domain.DecodeConsentCommand(bot, encoded)
	if !valid {
		return result, nil
	}
	query := `SELECT ` + consentColumns + ` FROM memory_consent_proposals p`
	arg := any(cmd.ProposalID)
	if cmd.ReplyID != 0 {
		query += consentDelivery + ` WHERE o.platform_message_id=$4`
		arg = cmd.ReplyID
	} else {
		query += ` WHERE p.id=$4`
	}
	p, err := readConsent(q.row(ctx, query+` AND p.bot_id=$1 AND p.source_conversation_id=$2 AND p.subject_id=$3 FOR UPDATE OF p`+consentReceiptLocks(cmd.ReplyID), bot, conversation, sender, arg))
	if errors.Is(err, contracts.ErrConsentMissing) || errors.Is(err, contracts.ErrConsentConflict) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	outcome := "rejected"
	switch {
	case cmd.Confirm && enabled && p.Status == "pending":
		if err = scopeinfra.LockMemoryAuthorization(ctx, q, bot, "group", p.SourceGroupID); err != nil {
			return result, err
		}
		var content, digest string
		err = q.row(ctx, `SELECT m.content,(SELECT display_digest FROM memory_consent_proposals WHERE id=$5) FROM memories m`+consentFacts+` AND c.id=$4 AND src.id=(SELECT source_message_id FROM memory_consent_proposals WHERE id=$5 AND expires_at>clock_timestamp() AND subject_id=src.sender_id)
 AND COALESCE((SELECT enabled AND store_messages FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1),false)
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text((SELECT target_groups FROM memory_consent_proposals WHERE id=$5)) target
 WHERE NOT EXISTS(SELECT 1 FROM qq_groups WHERE bot_id=$1 AND group_id=target.value::bigint)) FOR SHARE OF m,src`, bot, p.MemoryID, p.FactVersion, conversation, p.ID).scan(&content, &digest)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		if err == nil && domain.ConsentPromptDigest(domain.ConsentPrompt(p.ID, content, p.TargetGroups)) == digest {
			targets, _ := json.Marshal(p.TargetGroups)
			if _, err = q.ExecContext(ctx, `INSERT INTO memory_consent_grants(bot_id,memory_id,fact_version,subject_id,target_group_id,proposal_id,confirmation_message_id,confirmed_at) SELECT $1,$2,$3,$4,value::bigint,$5,$6,clock_timestamp() FROM jsonb_array_elements_text($7::jsonb) ON CONFLICT (bot_id,memory_id,fact_version,subject_id,target_group_id) DO UPDATE SET proposal_id=EXCLUDED.proposal_id,confirmation_message_id=EXCLUDED.confirmation_message_id,confirmed_at=EXCLUDED.confirmed_at,revoked_at=NULL,revocation_source='',state_version=memory_consent_grants.state_version+1`, bot, p.MemoryID, p.FactVersion, sender, p.ID, message, targets); err != nil {
				return result, err
			}
			if _, err = q.ExecContext(ctx, `UPDATE memory_consent_proposals SET status='confirmed',state_version=state_version+1 WHERE id=$1`, p.ID); err != nil {
				return result, err
			}
			outcome = "confirmed"
		}
	case !cmd.Confirm:
		if err = revokeConsent(ctx, q, p, cmd.TargetGroupID, "subject"); err != nil && !errors.Is(err, contracts.ErrConsentInvalid) {
			return result, err
		}
		if err == nil {
			outcome = "revoked"
		}
	}
	if _, err = q.ExecContext(ctx, `INSERT INTO memory_consent_events(bot_id,conversation_id,platform_message_id,proposal_id,outcome) VALUES($1,$2,$3,$4,$5)`, bot, conversation, platform, p.ID, outcome); err != nil {
		return result, err
	}
	err = q.row(ctx, `SELECT id::text,status,state_version FROM memory_consent_proposals WHERE id=$1`, p.ID).scan(&result.ProposalID, &result.Status, &result.StateVersion)
	result.Outcome = outcome
	return result, err
}

func (r *Repo) ConsentPrompt(ctx context.Context, bot int64, id string, jobID int64, claim string) (contracts.ConsentProposal, string, error) {
	p, err := readConsent(r.pool.QueryRow(ctx, `SELECT `+consentColumns+` FROM memory_consent_proposals p JOIN message_jobs j ON j.consent_proposal_id=p.id WHERE p.bot_id=$1 AND p.id=$2 AND j.id=$3 AND j.claim_token=$4::uuid AND j.status='processing' AND j.lease_expires_at>clock_timestamp() AND p.status='pending' AND p.expires_at>clock_timestamp()`, bot, id, jobID, claim))
	if err != nil {
		return p, "", err
	}
	var content, digest string
	err = r.pool.QueryRow(ctx, `SELECT m.content,(SELECT display_digest FROM memory_consent_proposals WHERE id=$5) FROM memories m`+consentFacts+` AND c.id=$4 AND src.id=(SELECT source_message_id FROM memory_consent_proposals WHERE id=$5)`, bot, p.MemoryID, p.FactVersion, p.SourceConversationID, id).Scan(&content, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return p, "", contracts.ErrConsentConflict
	}
	if err != nil {
		return p, "", err
	}
	text := domain.ConsentPrompt(p.ID, content, p.TargetGroups)
	if domain.ConsentPromptDigest(text) != digest {
		return p, "", contracts.ErrConsentConflict
	}
	return p, text, nil
}

// invalidateStaleConsent closes every authorization for a superseded fact version.
// invalidateStaleConsent 关闭失效事实版本的所有授权。
func invalidateStaleConsent(ctx context.Context, tx *db.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE memory_consent_proposals p SET status='invalidated',state_version=state_version+1 WHERE p.memory_id=$1 AND p.status IN ('confirmed','pending') AND NOT EXISTS(SELECT 1 FROM memories m WHERE m.memory_id=p.memory_id AND m.bot_id=p.bot_id AND m.authorization_version=p.fact_version AND m.source_message_id=p.source_message_id)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE memory_consent_grants g SET revoked_at=clock_timestamp(),revocation_source='fact_changed',state_version=g.state_version+1 FROM memory_consent_proposals p WHERE p.id=g.proposal_id AND p.memory_id=$1 AND p.status='invalidated' AND g.revoked_at IS NULL`, id)
	return err
}

// ConsentSendGate runs after the ledger takes its conversation and claim locks.
// ConsentSendGate 在台账获取会话与认领锁后执行。
type ConsentSendGate struct {
	Enabled, ReadEnabled bool
}

func (g ConsentSendGate) LockConsentSend(ctx context.Context, tx *db.Tx, botID int64) error {
	return LockMemoryMutation(ctx, tx, botID)
}

func consentDefaultCanSend(ctx context.Context, tx *db.Tx) (bool, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared(hashtextextended('settings:bootstrap',0))"); err != nil {
		return false, err
	}
	var stored sql.NullBool
	err := tx.QueryRow(ctx, `SELECT (payload #>> '{Tools,PermissionDefaults,qq.reply.current}')::boolean FROM system_settings WHERE id=1 FOR SHARE`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return !stored.Valid || stored.Bool, err
}

func (g ConsentSendGate) ValidateConsentSend(ctx context.Context, tx *db.Tx, botID int64, deliveryID string) error {
	if !g.Enabled {
		return outbounddomain.ErrConsentSendDenied
	}
	var text, digest, memoryID, conversation, proposal string
	var original int64
	var version, group int64
	var targets []byte
	err := tx.QueryRow(ctx, `SELECT o.text_content,p.display_digest,p.memory_id::text,p.fact_version,p.source_conversation_id::text,p.source_group_id,p.id::text,p.source_message_id,p.target_groups FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id WHERE o.id=$1 AND o.status='prepared' AND p.bot_id=$2 AND o.bot_id=p.bot_id AND o.conversation_id=p.source_conversation_id AND o.target_kind='group' AND o.target_id=p.source_group_id AND o.tool_call_id='memory_consent_prompt' AND p.status='pending' AND p.expires_at>clock_timestamp() AND p.source_message_id IS NOT NULL AND EXISTS(SELECT 1 FROM qq_groups WHERE bot_id=p.bot_id AND group_id=p.source_group_id) FOR SHARE OF p`, deliveryID, botID).Scan(&text, &digest, &memoryID, &version, &conversation, &group, &proposal, &original, &targets)
	if errors.Is(err, sql.ErrNoRows) {
		return outbounddomain.ErrConsentSendDenied
	}
	if err != nil {
		return err
	}
	if err = scopeinfra.LockMemoryAuthorization(ctx, tx, botID, "group", group); err != nil {
		return err
	}
	var canSend bool
	defaultCanSend, err := consentDefaultCanSend(ctx, tx)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT enabled AND store_messages AND reply_mode<>'observe_only'
 AND COALESCE((tool_permissions->>'qq.reply.current')::boolean,$3)
 AND (max_chars_per_message=0 OR max_chars_per_message>=char_length($4::text))
 AND (max_total_chars_per_run=0 OR max_total_chars_per_run>=char_length($4::text))
 AND (max_replies_per_hour=0 OR max_replies_per_hour >= (
  (SELECT count(*) FROM agent_outbound_messages WHERE conversation_id=$5 AND status IN('prepared','sending','sent','unknown') AND created_at>clock_timestamp()-interval '1 hour')
  + (SELECT count(*) FROM messages WHERE conversation_id=$5 AND direction='outgoing' AND outbound_id IS NULL AND sent_status IN('sent','unknown') AND created_at>clock_timestamp()-interval '1 hour')
 ))
 FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id IN ($2,0) ORDER BY scope_id DESC LIMIT 1`, botID, group, defaultCanSend, text, conversation).Scan(&canSend)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !canSend {
		return outbounddomain.ErrConsentSendDenied
	}
	if err != nil {
		return err
	}
	var content string
	err = tx.QueryRow(ctx, `SELECT m.content FROM memories m`+consentFacts+` AND c.id=$4 AND c.target_id=$5 AND src.id=$6 AND EXISTS (SELECT 1 FROM memory_consent_proposals p WHERE p.id=$7 AND p.expires_at>clock_timestamp() AND p.subject_id=src.sender_id) FOR SHARE OF m,src`, botID, memoryID, version, conversation, group, original, proposal).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return outbounddomain.ErrConsentSendDenied
	}
	if err != nil {
		return err
	}
	var groups []int64
	if err = json.Unmarshal(targets, &groups); err != nil {
		return err
	}
	if prompt := domain.ConsentPrompt(proposal, content, groups); prompt != text || domain.ConsentPromptDigest(prompt) != digest {
		return outbounddomain.ErrConsentSendDenied
	}
	return nil
}
