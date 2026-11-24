package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

const factSourceJoins = `
 JOIN messages s ON s.id=f.message_id AND s.memory_authorization_event_id=f.authorization_event_id
 JOIN conversations c ON c.id=s.conversation_id
 LEFT JOIN scope_policies direct ON direct.bot_id=c.bot_id AND direct.scope_type=c.kind AND direct.scope_id=c.target_id
 LEFT JOIN scope_policies template ON template.bot_id=c.bot_id AND template.scope_type=c.kind AND template.scope_id=0`

const factSourceConditions = `
 AND s.direction='incoming' AND s.control_kind='' AND s.bot_id=c.bot_id
 AND jsonb_typeof(s.segments)='array' AND jsonb_array_length(s.segments)>0
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(s.segments) part WHERE part->>'type' IS DISTINCT FROM 'text')
 AND s.sender_id>0 AND s.sender_id<>s.bot_id
 AND (f.expires_at IS NULL OR f.expires_at>statement_timestamp())
 AND COALESCE(direct.enabled,template.enabled,false)
 AND COALESCE(direct.store_messages,template.store_messages,false)
 AND COALESCE(direct.allow_memory_write,template.allow_memory_write,false)
 AND COALESCE(direct.allow_memory_read,template.allow_memory_read,false)
 AND COALESCE(direct.reply_mode,template.reply_mode,'observe_only')<>'observe_only'
 AND f.authorization_event_id=CASE WHEN direct.bot_id IS NOT NULL THEN direct.memory_authorization_event_id ELSE
 GREATEST(template.memory_authorization_event_id, (SELECT max(e.id) FROM scope_memory_authorization_events e
 WHERE e.bot_id=c.bot_id AND e.scope_type=c.kind AND e.scope_id=c.target_id AND e.event_kind='removed')) END
 AND (COALESCE((SELECT (payload #>> '{Messages,RetentionDays}')::int FROM system_settings WHERE id=1),0)=0
 OR s.created_at>statement_timestamp()-make_interval(days=>COALESCE((SELECT (payload #>> '{Messages,RetentionDays}')::int FROM system_settings WHERE id=1),0)))`

func FactSupportPredicate(memoryAlias string) string {
	return FactSupportsMessage(memoryAlias, "")
}

func FactSupportsMessage(memoryAlias, messageExpression string) string {
	messageConstraint := ""
	if messageExpression != "" {
		messageConstraint = " AND s.id=" + messageExpression
	}
	return `EXISTS(SELECT 1 FROM memory_fact_sources f` + factSourceJoins + ` WHERE f.memory_id=` + memoryAlias + `.memory_id` + messageConstraint + `
 AND s.bot_id=` + memoryAlias + `.bot_id AND ` + memoryAlias + `.subject_type='user' AND ` + memoryAlias + `.subject_id=s.sender_id::text
 AND ((` + memoryAlias + `.visibility='group' AND c.kind='group' AND ` + memoryAlias + `.owner_key=c.target_id::text)
 OR (` + memoryAlias + `.visibility='private_user' AND c.kind='private' AND ` + memoryAlias + `.owner_key=c.target_id::text AND c.target_id=s.sender_id)
 OR (` + memoryAlias + `.visibility='conversation' AND ` + memoryAlias + `.owner_key=c.id::text))` + factSourceConditions + `)`
}

func readFactSources(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (*sql.Rows, error)
}, memory domain.Memory) ([]domain.FactSource, error) {
	rows, err := q.Query(ctx, `SELECT s.id,s.sender_id,s.text_content,c.id::text,c.kind,c.target_id,f.authorization_event_id FROM memory_fact_sources f`+factSourceJoins+`
 WHERE f.memory_id=$1::uuid AND s.bot_id=$2 AND s.sender_id::text=$3
 AND (($4='group' AND c.kind='group' AND $5=c.target_id::text)
 OR ($4='private_user' AND c.kind='private' AND $5=c.target_id::text AND c.target_id=s.sender_id)
 OR ($4='conversation' AND $5=c.id::text))`+factSourceConditions+` ORDER BY s.id`, memory.ID, memory.BotID, memory.SubjectID, memory.Visibility, memory.OwnerKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []domain.FactSource
	fact, valid := domain.FactFromContent(memory.Content)
	for rows.Next() {
		var source domain.FactSource
		var text string
		scope := domain.ExtractionScope{BotID: memory.BotID}
		if err := rows.Scan(&source.MessageID, &source.SenderID, &text, &scope.ConversationID, &scope.ScopeType, &scope.ScopeID, &scope.AuthorizationEventID); err != nil {
			return nil, err
		}
		if valid && sourceMatchesFactScope(memory, scope, source.SenderID) && fact.Key(memory, scope) == memory.FactKey && domain.FactSupportedBy(fact, text) {
			sources = append(sources, source)
		}
	}
	return sources, rows.Err()
}

func projectFactSources(memory *domain.Memory, sources []domain.FactSource) {
	memory.FactSources = sources
	memory.Source = ""
	memory.SourceMessageID = 0
	memory.SourceSenderID = 0
	if len(sources) > 0 {
		memory.SourceMessageID = sources[0].MessageID
		memory.SourceSenderID = sources[0].SenderID
		memory.Source = fmt.Sprintf("message:%d", memory.SourceMessageID)
	}
}

func factSourceState(ctx context.Context, tx *db.Tx, botID, messageID int64) (domain.ExtractionScope, int64, string, time.Time, error) {
	scope := domain.ExtractionScope{BotID: botID}
	var sender int64
	var text string
	var expires sql.NullTime
	err := tx.QueryRow(ctx, `SELECT c.id::text,c.kind,c.target_id,s.memory_authorization_event_id,s.sender_id,s.text_content,
 CASE WHEN COALESCE(cfg.days,0)>0 THEN s.created_at+make_interval(days=>cfg.days) END
 FROM messages s JOIN conversations c ON c.id=s.conversation_id AND c.bot_id=s.bot_id
 LEFT JOIN LATERAL(SELECT (payload #>> '{Messages,RetentionDays}')::int days FROM system_settings WHERE id=1) cfg ON true
 WHERE s.id=$1 AND s.bot_id=$2 AND s.direction='incoming' AND s.control_kind='' AND s.sender_id>0 AND s.sender_id<>s.bot_id
 AND s.memory_authorization_event_id IS NOT NULL AND (c.kind<>'private' OR c.target_id=s.sender_id)
 AND jsonb_typeof(s.segments)='array' AND jsonb_array_length(s.segments)>0
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(s.segments) part WHERE part->>'type' IS DISTINCT FROM 'text')
 AND (COALESCE(cfg.days,0)=0 OR s.created_at+make_interval(days=>cfg.days)>statement_timestamp())`, messageID, botID).Scan(&scope.ConversationID, &scope.ScopeType, &scope.ScopeID, &scope.AuthorizationEventID, &sender, &text, &expires)
	return scope, sender, text, expires.Time, err
}

func insertFactSource(ctx context.Context, tx *db.Tx, id string, messageID, eventID int64, expires time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO memory_fact_sources(id,memory_id,message_id,authorization_event_id,expires_at)
 VALUES($1::uuid,$2::uuid,$3,$4,$5) ON CONFLICT(memory_id,message_id) DO NOTHING`, uuid.NewString(), id, messageID, eventID, memoryNullableTime(expires))
	return err
}

func sourceMatchesFactScope(memory domain.Memory, scope domain.ExtractionScope, sender int64) bool {
	return memory.SubjectType == domain.SubjectUser && memory.SubjectID == strconv.FormatInt(sender, 10) && memory.VisibleTo(scope.ScopeType, scope.ScopeID, scope.ConversationID)
}

func (r *Repo) finalizeFactCandidate(ctx context.Context, tx *db.Tx, batch domain.ExtractionBatch, memory *domain.Memory) (handled bool, created bool, suppressed bool, err error) {
	if memory.FactProposal == nil {
		return false, false, false, nil
	}
	scope, sender, text, expires, err := factSourceState(ctx, tx, memory.BotID, memory.SourceMessageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, false, false, err
	}
	fact, verified := domain.FactFromEvidence(text, *memory.FactProposal)
	if !verified || err != nil || scope != batch.Scope || !sourceMatchesFactScope(*memory, scope, sender) || memory.ReviewReason != "" {
		memory.SubjectType = domain.SubjectUnknown
		memory.SubjectID = domain.ExtractionUnknownSubjectID(memory.SourceMessageID, memory.SourceSenderID)
		memory.Content = "待核对来源"
		memory.DedupeKey = domain.ComputeDedupeKey(memory.BotID, memory.Visibility, memory.OwnerKey, memory.SubjectType, memory.SubjectID, memory.Content)
		memory.ReviewReason = "evidence_insufficient"
		return false, false, false, nil
	}
	memory.Content = fact.Content()
	memory.FactKey = fact.Key(*memory, scope)
	memory.DedupeKey = factExactKey(*memory, scope.AuthorizationEventID)
	var id string
	var status domain.Status
	err = tx.QueryRow(ctx, `SELECT memory_id::text,status FROM memories WHERE bot_id=$1 AND visibility=$2 AND owner_key=$3 AND fact_key=$4
 ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'candidate' THEN 1 ELSE 2 END,created_at,memory_id LIMIT 1`, memory.BotID, memory.Visibility, memory.OwnerKey, memory.FactKey).Scan(&id, &status)
	if err == nil {
		if status == domain.StatusCandidate || status == domain.StatusActive {
			return true, false, false, insertFactSource(ctx, tx, id, memory.SourceMessageID, scope.AuthorizationEventID, expires)
		}
		return true, false, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, false, false, err
	}
	id = uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO memories(memory_id,bot_id,visibility,owner_key,content,source,source_message_id,source_sender_id,origin,subject_type,subject_id,fact_key,dedupe_key,confidence,status)
 VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,'extraction',$9,$10,$11,$12,$13,'candidate')`, id, memory.BotID, memory.Visibility, memory.OwnerKey, memory.Content, memory.Source, memory.SourceMessageID, memory.SourceSenderID, memory.SubjectType, memory.SubjectID, memory.FactKey, memory.DedupeKey, memory.Confidence); err != nil {
		return false, false, false, err
	}
	return true, true, false, insertFactSource(ctx, tx, id, memory.SourceMessageID, scope.AuthorizationEventID, expires)
}

func factExactKey(memory domain.Memory, eventID int64) string {
	return domain.ComputeDedupeKey(memory.BotID, memory.Visibility, memory.OwnerKey, memory.SubjectType, memory.SubjectID, memory.Content) + ":" + strconv.FormatInt(eventID, 10)
}

func validateFactMutation(ctx context.Context, tx *db.Tx, memory *domain.Memory, previous domain.Memory, except string) error {
	if memory.Origin != domain.OriginExtraction {
		return nil
	}
	if previous.FactKey != "" {
		fact, ok := domain.FactFromContent(memory.Content)
		if !ok || memory.Visibility != previous.Visibility || memory.OwnerKey != previous.OwnerKey {
			return memoryapp.ErrFactEvidenceRequired
		}
		scope, err := lockFactScope(ctx, tx, *memory)
		if err != nil {
			return err
		}
		memory.FactKey = fact.Key(*memory, scope)
		if memory.FactKey != previous.FactKey {
			return memoryapp.ErrFactEvidenceRequired
		}
		memory.Content = fact.Content()
		memory.DedupeKey = factExactKey(*memory, scope.AuthorizationEventID)
		sources, err := readFactSources(ctx, tx, *memory)
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			return memoryapp.ErrFactEvidenceRequired
		}
	} else {
		if memory.SourceMessageID <= 0 || memory.SubjectType != domain.SubjectUser {
			return memoryapp.ErrFactEvidenceRequired
		}
		scope, sender, text, expires, err := factSourceState(ctx, tx, memory.BotID, memory.SourceMessageID)
		if errors.Is(err, sql.ErrNoRows) {
			return memoryapp.ErrFactEvidenceRequired
		}
		if err != nil {
			return err
		}
		if !sourceMatchesFactScope(*memory, scope, sender) {
			return memoryapp.ErrFactEvidenceRequired
		}
		if err := scopeinfra.LockMemoryAuthorization(ctx, tx, memory.BotID, scope.ScopeType, scope.ScopeID); err != nil {
			return err
		}
		event, authorized, err := scopeinfra.ResolveMemoryAuthorizationInTx(ctx, tx, memory.BotID, scope.ScopeType, scope.ScopeID)
		if err != nil {
			return err
		}
		if !authorized || event != scope.AuthorizationEventID {
			return memoryapp.ErrFactEvidenceRequired
		}
		fact, verified := domain.FactFromContent(memory.Content)
		if !verified || !domain.FactSupportedBy(fact, text) {
			return memoryapp.ErrFactEvidenceRequired
		}
		memory.FactKey = fact.Key(*memory, scope)
		memory.Content = fact.Content()
		memory.DedupeKey = factExactKey(*memory, scope.AuthorizationEventID)
		if err := insertFactSource(ctx, tx, memory.ID, memory.SourceMessageID, scope.AuthorizationEventID, expires); err != nil {
			return err
		}
		if support, err := readFactSources(ctx, tx, *memory); err != nil || len(support) == 0 {
			if err != nil {
				return err
			}
			return memoryapp.ErrFactEvidenceRequired
		}
	}
	return checkFactCollision(ctx, tx, *memory, except)
}

func lockFactScope(ctx context.Context, tx *db.Tx, memory domain.Memory) (domain.ExtractionScope, error) {
	scope := domain.ExtractionScope{BotID: memory.BotID}
	err := tx.QueryRow(ctx, `SELECT c.id::text,c.kind,c.target_id,f.authorization_event_id FROM memory_fact_sources f JOIN messages s ON s.id=f.message_id JOIN conversations c ON c.id=s.conversation_id WHERE f.memory_id=$1::uuid AND c.bot_id=$2 ORDER BY f.message_id LIMIT 1`, memory.ID, memory.BotID).Scan(&scope.ConversationID, &scope.ScopeType, &scope.ScopeID, &scope.AuthorizationEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, memoryapp.ErrFactEvidenceRequired
	}
	if err != nil {
		return scope, err
	}
	return scope, scopeinfra.LockMemoryAuthorization(ctx, tx, memory.BotID, scope.ScopeType, scope.ScopeID)
}

func checkFactCollision(ctx context.Context, tx *db.Tx, memory domain.Memory, except string) error {
	var existing string
	var visible bool
	err := tx.QueryRow(ctx, `SELECT m.memory_id::text,(m.status='active' AND m.approved_by<>'' AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp()) AND `+FactSupportPredicate("m")+`) FROM memories m WHERE m.bot_id=$1 AND m.visibility=$2 AND m.owner_key=$3 AND m.fact_key=$4
 AND m.memory_id::text<>$5 AND m.memory_id::text<>$6 AND m.status IN ('active','candidate')
 ORDER BY CASE m.status WHEN 'active' THEN 0 ELSE 1 END,m.created_at,m.memory_id LIMIT 1`, memory.BotID, memory.Visibility, memory.OwnerKey, memory.FactKey, memory.ID, except).Scan(&existing, &visible)
	if err == nil {
		if visible {
			return &memoryapp.FactConflict{ExistingID: existing}
		}
		return &memoryapp.FactConflict{}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	fact, ok := domain.FactFromContent(memory.Content)
	if !ok {
		return memoryapp.ErrFactEvidenceRequired
	}
	rows, err := tx.Query(ctx, `SELECT m.memory_id::text,m.content,m.origin,m.source_message_id
 FROM memories m WHERE m.bot_id=$1 AND m.visibility=$2 AND m.owner_key=$3 AND m.subject_type=$4 AND m.subject_id=$5
 AND m.memory_id::text<>$6 AND m.memory_id::text<>$7 AND m.fact_key IS NULL AND m.status='active'
 AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp()) ORDER BY m.created_at,m.memory_id`, memory.BotID, memory.Visibility, memory.OwnerKey, memory.SubjectType, memory.SubjectID, memory.ID, except)
	if err != nil {
		return err
	}
	type legacy struct {
		id, content, origin string
		source              sql.NullInt64
	}
	var matches []legacy
	for rows.Next() {
		var item legacy
		if err := rows.Scan(&item.id, &item.content, &item.origin, &item.source); err != nil {
			rows.Close()
			return err
		}
		matches = append(matches, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range matches {
		if item.origin == string(domain.OriginManual) {
			if other, ok := domain.FactFromContent(item.content); ok && other == fact {
				return &memoryapp.FactConflict{ExistingID: item.id}
			}
			continue
		}
		if !item.source.Valid {
			continue
		}
		scope, sender, text, _, err := factSourceState(ctx, tx, memory.BotID, item.source.Int64)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if other, ok := domain.FactFromContent(item.content); ok && other == fact && domain.FactSupportedBy(fact, text) && sourceMatchesFactScope(memory, scope, sender) && fact.Key(memory, scope) == memory.FactKey {
			return &memoryapp.FactConflict{ExistingID: item.id}
		}
	}
	return nil
}
