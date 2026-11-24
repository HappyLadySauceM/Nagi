package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outbound "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	scope "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/google/uuid"
)

func validConsentRead(in domain.SearchRequest) bool {
	if in.Kind != "group" || in.ReplyJob.ID <= 0 {
		return false
	}
	if _, err := uuid.Parse(in.ConversationID); err != nil {
		return false
	}
	_, err := uuid.Parse(in.ReplyJob.Claim)
	return err == nil
}

var verifiedConsent = `
 FROM memory_consent_grants g
 JOIN memory_consent_proposals p ON p.id=g.proposal_id AND p.bot_id=g.bot_id AND p.memory_id=g.memory_id AND p.fact_version=g.fact_version AND p.subject_id=g.subject_id AND p.status='confirmed'
 JOIN memories m ON m.memory_id=g.memory_id AND m.bot_id=g.bot_id AND m.authorization_version=g.fact_version AND m.status='active' AND m.approved_by<>''
 JOIN messages src ON src.id=p.source_message_id AND src.id=m.source_message_id AND src.bot_id=p.bot_id AND src.conversation_id=p.source_conversation_id AND src.sender_id=p.subject_id AND src.sender_id=m.source_sender_id AND src.direction='incoming' AND src.control_kind='' AND src.platform_message_id<>0
 JOIN conversations source_conv ON source_conv.id=src.conversation_id AND source_conv.bot_id=p.bot_id AND source_conv.kind='group' AND source_conv.target_id=p.source_group_id
 JOIN messages confirmed ON confirmed.id=g.confirmation_message_id AND confirmed.bot_id=p.bot_id AND confirmed.conversation_id=p.source_conversation_id AND confirmed.sender_id=p.subject_id AND confirmed.direction='incoming' AND confirmed.control_kind='memory_consent_command' AND confirmed.platform_message_id<>0
 JOIN memory_consent_events event ON event.proposal_id=p.id AND event.bot_id=p.bot_id AND event.conversation_id=p.source_conversation_id AND event.platform_message_id=confirmed.platform_message_id AND event.outcome='confirmed'` + consentDelivery + `
 WHERE g.revoked_at IS NULL AND (m.expires_at IS NULL OR m.expires_at>clock_timestamp()) AND p.subject_id<>p.bot_id
 AND (m.fact_key IS NULL OR ` + FactSupportsMessage("m", "src.id") + `)
 AND m.subject_type='user' AND m.subject_id=p.subject_id::text AND p.source_group_id<>g.target_group_id
 AND p.target_groups @> jsonb_build_array(g.target_group_id)
 AND ((m.visibility='group' AND m.owner_key=source_conv.target_id::text) OR (m.visibility='conversation' AND m.owner_key=source_conv.id::text))`

func (r *Repo) consentRead(ctx context.Context, in domain.SearchRequest, id string, keywords []domain.QueryTerm) ([]domain.Memory, error) {
	if !validConsentRead(in) {
		return nil, nil
	}
	terms := make([]string, 0, len(keywords))
	for _, term := range keywords {
		terms = append(terms, term.Term)
	}
	encoded, _ := json.Marshal(terms)
	args := []any{in.BotID, in.TargetID, in.ConversationID, in.ReplyJob.ID, in.ReplyJob.Claim, encoded}
	query := `SELECT m.memory_id::text,m.content,m.subject_id,m.confidence,m.updated_at,m.authorization_version,concat(g.id,':',g.state_version,':',p.id,':',confirmed.id)` + verifiedConsent + `
 AND g.bot_id=$1 AND g.target_group_id=$2
 AND COALESCE((SELECT enabled AND store_messages AND allow_memory_read AND reply_mode<>'observe_only'
 FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id IN ($2,0) ORDER BY scope_id DESC LIMIT 1),false)
 AND EXISTS(SELECT 1 FROM message_jobs job JOIN messages incoming ON incoming.id=job.incoming_message_id JOIN conversations target ON target.id=incoming.conversation_id WHERE job.id=$4 AND job.claim_token=$5::uuid AND job.status='processing' AND job.lease_expires_at>clock_timestamp() AND job.source_kind='incoming' AND incoming.direction='incoming' AND incoming.control_kind='' AND incoming.platform_message_id<>0 AND incoming.bot_id=g.bot_id AND incoming.sender_id=g.subject_id AND target.id=$3::uuid AND target.bot_id=g.bot_id AND target.kind='group' AND target.target_id=g.target_group_id)
 AND (jsonb_array_length($6::jsonb)=0 OR EXISTS(SELECT 1 FROM jsonb_array_elements_text($6::jsonb) term WHERE strpos(lower(m.content),lower(term.value))>0))`
	if id != "" {
		args = append(args, id)
		query += ` AND m.memory_id=$7::uuid`
	}
	query += ` ORDER BY m.confidence DESC,m.updated_at DESC,m.memory_id`
	if in.Limit > 0 {
		args = append(args, in.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Memory
	for rows.Next() {
		memory := domain.Memory{BotID: in.BotID, Visibility: domain.Visibility("cross_group_consent"), SubjectType: domain.SubjectUser, Status: domain.StatusActive}
		if err = rows.Scan(&memory.ID, &memory.Content, &memory.SubjectID, &memory.Confidence, &memory.UpdatedAt, &memory.AuthorizationVersion, &memory.ConsentIdentity); err != nil {
			return nil, err
		}
		out = append(out, memory)
	}
	return out, rows.Err()
}

func (r *Repo) consentGroups(ctx context.Context, proposalID string) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT g.target_group_id`+verifiedConsent+` AND p.id=$1 ORDER BY g.target_group_id`, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []int64{}
	for rows.Next() {
		var group int64
		if err = rows.Scan(&group); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// ValidateConsentReply checks the recorded wire payload and the live grants inside the dispatch transaction.
// ValidateConsentReply 在派发事务内核验已记录的线上载荷及当前授权实例。
func (g ConsentSendGate) ValidateConsentReply(ctx context.Context, tx *db.Tx, botID int64, deliveryID string, reads []memory.ConsentRead) error {
	if !g.ReadEnabled || len(reads) == 0 {
		return outbound.ErrConsentSendDenied
	}
	var bot, group, job, sender, trigger int64
	var conversation, claim, action, text string
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT o.bot_id,c.target_id,j.id,m.sender_id,m.platform_message_id,c.id::text,j.claim_token::text,o.action,o.text_content,o.payload
 FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id AND j.source_kind='incoming' AND j.status='processing' AND j.lease_expires_at>clock_timestamp() AND j.claim_token=o.claim_token
 JOIN messages m ON m.id=j.incoming_message_id AND m.direction='incoming' AND m.control_kind='' AND m.platform_message_id<>0 AND m.sender_id>0 AND m.sender_id<>o.bot_id AND m.bot_id=o.bot_id AND m.conversation_id=o.conversation_id
 JOIN conversations c ON c.id=o.conversation_id AND c.bot_id=o.bot_id AND c.kind='group' AND c.target_id=o.target_id
 WHERE o.id=$1 AND o.status='prepared' AND o.bot_id=$2 AND o.target_kind='group' AND o.payload @> '{"cross_group_fact":true}'::jsonb FOR SHARE OF m`, deliveryID, botID).Scan(&bot, &group, &job, &sender, &trigger, &conversation, &claim, &action, &text, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return outbound.ErrConsentSendDenied
	}
	if err != nil {
		return err
	}
	if !validConsentReplyPayload(action, text, payload, group, sender, trigger, reads) {
		return outbound.ErrConsentSendDenied
	}
	if err := scope.LockMemoryAuthorization(ctx, tx, bot, "group", group); err != nil {
		return err
	}
	for _, read := range reads {
		if read.BotID != bot || read.GroupID != group || read.ConversationID != conversation || read.Kind != "group" || read.JobID != job || read.Claim != claim {
			return outbound.ErrConsentSendDenied
		}
		var found int64
		err := tx.QueryRow(ctx, `SELECT g.id`+verifiedConsent+`
 AND g.bot_id=$1 AND g.target_group_id=$2 AND g.subject_id=$3 AND g.memory_id=$4::uuid AND g.fact_version=$5
 AND concat(g.id,':',g.state_version,':',p.id,':',confirmed.id)=$6
 FOR SHARE OF g,p,m,src,confirmed,event,j,o,h`, bot, group, sender, read.MemoryID, read.FactVersion, read.Identity).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return outbound.ErrConsentSendDenied
		}
		if err != nil {
			return err
		}
	}
	defaultCanSend, err := consentDefaultCanSend(ctx, tx)
	if err != nil {
		return err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT enabled AND store_messages AND allow_memory_read AND reply_mode<>'observe_only'
 AND COALESCE((tool_permissions->>'qq.reply.current')::boolean,$3)
 FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id IN ($2,0) ORDER BY scope_id DESC LIMIT 1 FOR SHARE`, bot, group, defaultCanSend).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return outbound.ErrConsentSendDenied
	}
	if err != nil {
		return err
	}

	return nil
}

func validConsentReplyPayload(action, text string, payload []byte, group, sender, trigger int64, reads []memory.ConsentRead) bool {
	if action != string(ob.ActionSendGroup) && action != string(ob.ActionSendMessage) {
		return false
	}
	var wire struct {
		Message           *string         `json:"message"`
		Segments          json.RawMessage `json:"segments"`
		Params            json.RawMessage `json:"params"`
		ConsentReadDigest string          `json:"consent_read_digest"`
	}
	if json.Unmarshal(payload, &wire) != nil || wire.ConsentReadDigest != memory.ConsentReadDigest(reads) {
		return false
	}
	segments, err := ob.SegmentsFromWire(wire.Segments)
	if err != nil || strings.TrimSpace(text) == "" || ob.PlainText(segments) != text {
		return false
	}
	if len(wire.Params) == 0 {
		return action == string(ob.ActionSendGroup) && wire.Message != nil && *wire.Message == text && len(segments) == 1 && segments[0].Type == ob.SegText && len(segments[0].Data) == 1
	}
	var params map[string]json.RawMessage
	if wire.Message != nil || json.Unmarshal(wire.Params, &params) != nil {
		return false
	}
	var actualGroup int64
	if json.Unmarshal(params["group_id"], &actualGroup) != nil || actualGroup != group {
		return false
	}
	if action == string(ob.ActionSendMessage) {
		var kind string
		if json.Unmarshal(params["message_type"], &kind) != nil || kind != "group" || len(params) != 3 {
			return false
		}
	} else if len(params) != 2 {
		return false
	}
	// Both representations must identify the same segments. The action uses a structured array.
	// 两处载荷必须具有相同的段内容，实际动作使用结构化段数组。
	var actual []json.RawMessage
	if json.Unmarshal(params["message"], &actual) != nil || len(actual) != len(segments) || string(params["message"]) != string(wire.Segments) {
		return false
	}
	for _, segment := range segments {
		if len(segment.Data) != 1 {
			return false
		}
		switch segment.Type {
		case ob.SegText:
			if _, ok := segment.Data["text"]; !ok {
				return false
			}
		case ob.SegAt:
			if segment.Data["qq"] != strconv.FormatInt(sender, 10) {
				return false
			}
		case ob.SegReply:
			if segment.Data["id"] != strconv.FormatInt(trigger, 10) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ValidateConsentGeneration commits one first-write decision against the primary database.
// ValidateConsentGeneration 从主库提交一次首写派发决定。
func (r *Repo) ValidateConsentGeneration(ctx context.Context, picked models.Profile, request memory.GenerationDispatch) error {
	if len(request.Reads) == 0 || !picked.Verified() || picked.Protocol != "openai" || request.Model != picked.Model {
		return memory.ErrReadChanged
	}
	endpoint, err := url.Parse(picked.BaseURL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return memory.ErrReadChanged
	}
	if request.Endpoint != strings.TrimRight(endpoint.String(), "/")+"/chat/completions" {
		return memory.ErrReadChanged
	}
	first := request.Reads[0]
	proposals := make([]string, 0, len(request.Reads))
	for _, read := range request.Reads {
		parts := strings.Split(read.Identity, ":")
		if read.BotID != first.BotID || read.GroupID != first.GroupID || read.ConversationID != first.ConversationID || read.JobID != first.JobID || read.Claim != first.Claim || read.Kind != "group" || len(parts) != 4 {
			return memory.ErrReadChanged
		}
		proposals = append(proposals, parts[2])
	}
	for _, read := range request.LocalReads {
		if read.BotID != first.BotID || read.Kind != first.Kind || read.TargetID != first.GroupID || read.ConversationID != first.ConversationID ||
			read.FactVersion <= 0 || read.UpdatedAt.IsZero() {
			return memory.ErrReadChanged
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	base, err := r.pool.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	tx := &db.Tx{Tx: base}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, first.BotID); err != nil {
		return err
	}
	proposalIDs, _ := json.Marshal(proposals)
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE c.bot_id=$1 AND (c.id=$2::uuid OR c.id IN (SELECT source_conversation_id FROM memory_consent_proposals WHERE bot_id=$1 AND id::text IN (SELECT value FROM jsonb_array_elements_text($3::jsonb)))) ORDER BY c.id FOR UPDATE`, first.BotID, first.ConversationID, proposalIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var jobID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM message_jobs WHERE id=$1 AND claim_token=$2::uuid FOR SHARE`, first.JobID, first.Claim).Scan(&jobID); err != nil {
		return err
	}
	if err := scope.LockMemoryAuthorization(ctx, tx, first.BotID, "group", first.GroupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('settings:bootstrap',0))`); err != nil {
		return err
	}
	var settingID int
	if err := tx.QueryRow(ctx, `SELECT id FROM system_settings WHERE id=1 FOR SHARE`).Scan(&settingID); err != nil {
		return err
	}
	encoded, _ := json.Marshal(request.Reads)
	local, _ := json.Marshal(request.LocalReads)
	selection := fmt.Sprintf("%d:group:%d", first.BotID, first.GroupID)
	template := fmt.Sprintf("%d:group:0", first.BotID)
	var allowed bool
	err = tx.QueryRow(ctx, `WITH trigger AS (
 SELECT m.sender_id FROM message_jobs job JOIN messages m ON m.id=job.incoming_message_id
 JOIN conversations c ON c.id=m.conversation_id AND c.bot_id=m.bot_id
 WHERE job.id=$4 AND job.claim_token=$5::uuid AND job.status='processing' AND job.lease_expires_at>clock_timestamp()
 AND job.source_kind='incoming' AND m.direction='incoming' AND m.control_kind='' AND m.platform_message_id<>0
 AND m.sender_id>0 AND m.sender_id<>m.bot_id AND m.bot_id=$1 AND c.id=$3::uuid AND c.kind='group' AND c.target_id=$2
 ), catalog AS (SELECT payload->'Models' models FROM system_settings WHERE id=1 AND payload #>> '{AI,Enabled}'='true'),
 policy AS (SELECT enabled,store_messages,allow_memory_read,allow_history_read,reply_mode
 FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id IN ($2,0) ORDER BY scope_id DESC LIMIT 1)
 SELECT EXISTS(SELECT 1 FROM trigger) AND COALESCE((SELECT enabled AND store_messages AND allow_memory_read AND reply_mode<>'observe_only' FROM policy),false)
 AND EXISTS(SELECT 1 FROM catalog, LATERAL jsonb_array_elements(models->'Profiles') profile
 WHERE profile->>'ID'=$7 AND profile->>'Usage'='generation' AND profile->>'Version'=$8::text
 AND profile->>'VerifiedVersion'=$8::text AND profile->>'VerifiedAt' IS NOT NULL
 AND profile->>'Protocol'=$9 AND profile->>'Model'=$10 AND profile->>'BaseURL'=$11
 AND $7=COALESCE(NULLIF(models->'Selections'->>$12,''),NULLIF(models->'Selections'->>$13,''),models->'Defaults'->>'generation'))
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements($6::jsonb) dependency WHERE NOT EXISTS(SELECT 1`+verifiedConsent+`
 AND g.bot_id=$1 AND g.target_group_id=$2 AND g.subject_id=(SELECT sender_id FROM trigger)
 AND m.memory_id=(dependency->>'MemoryID')::uuid AND m.authorization_version=(dependency->>'FactVersion')::bigint
 AND concat(g.id,':',g.state_version,':',p.id,':',confirmed.id)=dependency->>'Identity'))
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF($14::jsonb,'null'::jsonb),'[]'::jsonb)) dep
 WHERE NOT EXISTS(SELECT 1 FROM memories m
 WHERE m.memory_id=(dep->>'MemoryID')::uuid AND m.bot_id=$1 AND m.status='active' AND m.approved_by<>''
 AND m.authorization_version=(dep->>'FactVersion')::bigint AND m.updated_at=(dep->>'UpdatedAt')::timestamptz
 AND (m.expires_at IS NULL OR m.expires_at>clock_timestamp())
 AND ((m.visibility='group' AND m.owner_key=$2::bigint::text) OR (m.visibility='conversation' AND m.owner_key=$3::uuid::text) OR m.visibility='bot_shared')
 AND (m.fact_key IS NOT NULL OR (COALESCE(m.source_message_id,0)=(dep->>'SourceMessageID')::bigint AND COALESCE(m.source_sender_id,0)=(dep->>'SourceSenderID')::bigint))
 AND (NOT (dep->>'HistorySource')::boolean OR (SELECT allow_history_read FROM policy))
 AND ((m.fact_key IS NOT NULL AND `+FactSupportsMessage("m", "(dep->>'SourceMessageID')::bigint")+`)
 OR (m.fact_key IS NULL AND ((m.origin='manual' AND m.visibility<>'bot_shared' AND m.source_message_id IS NULL AND m.source_sender_id IS NULL AND NOT (dep->>'HistorySource')::boolean)
 OR EXISTS(SELECT 1 FROM messages src WHERE src.id=m.source_message_id AND src.bot_id=$1 AND src.conversation_id=$3::uuid
 AND src.direction='incoming' AND src.control_kind='' AND src.platform_message_id<>0 AND src.sender_id>0 AND src.sender_id<>$1
 AND (m.source_sender_id IS NULL OR src.sender_id=m.source_sender_id)))))))`, first.BotID, first.GroupID, first.ConversationID, first.JobID, first.Claim, encoded, picked.ID, strconv.FormatInt(picked.Version, 10), picked.Protocol, picked.Model, picked.BaseURL, selection, template, local).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return memory.ErrReadChanged
	}
	return tx.Commit(ctx)
}
