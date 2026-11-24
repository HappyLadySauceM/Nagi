package infrastructure

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ordering "github.com/HappyLadySauceM/Nagi/internal/conversation/contracts"
	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	plugins "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	pluginc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	scoped "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settings "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

type gatePermissions func() []pluginc.RegisteredTool

// SetGatePermissions binds the tool catalogue used to resolve effective send grants.
// SetGatePermissions 装配解析发送授权所用的工具目录。
func (r *Repo) SetGatePermissions(tools func() []pluginc.RegisteredTool) { r.authorize = tools }

type preparedDelivery struct {
	job, input, topic, version int64
	status, action, payload    string
	memoryDependencies         []byte
}

func (r *Repo) markSending(ctx context.Context, id, claim string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	reads := memory.ConsentReads(ctx)
	proposals, err := consentProposals(reads)
	if err != nil {
		return err
	}
	scope, err := r.lockScopedDeliveryConversations(ctx, tx, id, proposals...)
	if err != nil {
		return err
	}
	delivery, reason, err := lockDeliveryTopic(ctx, tx, id, scope)
	if err != nil {
		return err
	}
	if err := lockDeliveryJob(ctx, tx, id, claim); err != nil {
		return err
	}
	var continued, controlJob, incoming bool
	var sequence int
	var toolCallID string
	if err := tx.QueryRow(ctx, `SELECT delivery_continuation,source_kind='memory_consent_prompt' AND consent_proposal_id IS NOT NULL AND incoming_message_id IS NULL,
 source_kind='incoming' AND consent_proposal_id IS NULL AND incoming_message_id IS NOT NULL FROM message_jobs WHERE id=$1`, delivery.job).Scan(&continued, &controlJob, &incoming); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM agent_outbound_messages WHERE job_id=$1 AND (id=$2 OR status IN ('prepared','cancelled')) ORDER BY id FOR UPDATE`, delivery.job, id); err != nil {
		return err
	}
	var digestOK bool
	var storedPermit []byte
	var returned bool
	if err := tx.QueryRow(ctx, `SELECT status,action,payload::text,COALESCE(resume_digest=`+resumeDigestSQL+`,false), memory_dependencies,COALESCE(error_code='lease_recovered',false), send_permit, sequence, COALESCE(tool_call_id,'') FROM agent_outbound_messages WHERE id=$1`, id).Scan(&delivery.status, &delivery.action, &delivery.payload, &digestOK, &delivery.memoryDependencies, &returned, &storedPermit, &sequence, &toolCallID); err != nil {
		return err
	}
	if delivery.status != string(domain.StatusPrepared) && !(continued && returned && delivery.status == string(domain.StatusCancelled)) {
		return &domain.TransitionError{RecordID: id, From: domain.Status(delivery.status), To: domain.StatusSending}
	}
	derived, _, err := consentMarker(delivery.payload)
	if err != nil || derived != scope.derived || (scope.control && (continued || derived)) || controlJob != scope.control || (!incoming && !scope.control) || derived != (len(reads) > 0) {
		return domain.ErrConsentSendDenied
	}
	var reserved int
	if err := tx.QueryRow(ctx, `SELECT reserved_messages FROM agent_run_budgets WHERE job_id=$1 FOR UPDATE`, delivery.job).Scan(&reserved); err != nil {
		return err
	}
	var limits domain.Limits
	if derived || scope.control {
		if r.consentGate == nil {
			return domain.ErrConsentSendDenied
		}
		if derived {
			err = r.consentGate.ValidateConsentReply(ctx, tx, scope.botID, id, reads)
		} else {
			err = r.consentGate.ValidateConsentSend(ctx, tx, scope.botID, id)
		}
		if err != nil {
			return err
		}
	}
	if scope.hasMemoryReads && (scope.control || r.authorize == nil || r.memoryReads == nil) {
		return domain.ErrConsentSendDenied
	}
	if scope.control || r.authorize == nil {
		if !continued {
			limits, err = lockCurrentLimits(ctx, tx, scope.botID, scope.sourceKind, scope.sourceTarget, domain.Limits{MinIntervalMS: DefaultMessageIntervalMS})
			if err != nil {
				return err
			}
			if scope.control {
				limits.MaxMessages = 1
				var interval int
				if err := tx.QueryRow(ctx, `SELECT min_reply_interval_ms FROM scope_policies WHERE bot_id=$1 AND scope_type=$2 AND scope_id IN ($3,0) ORDER BY scope_id DESC LIMIT 1`, scope.botID, scope.sourceKind, scope.sourceTarget).Scan(&interval); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				limits.MinIntervalMS = max(limits.MinIntervalMS, interval)
			}
			if err := validateReserved(ctx, tx, id, limits); err != nil {
				return err
			}
		}
	} else {
		var permit []byte
		permit, limits, err = r.currentSendingPolicy(ctx, tx, scope, delivery)
		allowed := sameSendPermit(storedPermit, permit)
		if err != nil {
			return err
		}
		if !allowed {
			reason = domain.GateRevoked
		}
		if allowed && !returned {
			if err := validateReserved(ctx, tx, id, limits); err != nil {
				var budget *domain.BudgetError
				if !errors.As(err, &budget) {
					return err
				}
				reason = domain.GateBudget
			}
		}
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return err
	}
	if reason == "" || reason == domain.GatePending {
		reason, err = deliveryRouteReason(ctx, tx, delivery, now)
		if err != nil {
			return err
		}
	}
	if reason == "" && incoming && scope.sourceKind == "group" && scope.targetKind == "group" && scope.targetID == scope.sourceTarget {
		if toolCallID == "final" && !derived {
			reason = domain.GatePayload
		} else if reason, err = groupDeliveryOrder(ctx, tx, scope, delivery, sequence); err != nil {
			return err
		}
	}
	if continued && reason == "" {
		switch {
		case delivery.action == "qq.send_voice_current":
			reason = domain.GateAudio
		case !digestOK || !resumableText(delivery.payload):
			reason = domain.GatePayload
		}
	}
	if reason == "" && limits.MinIntervalMS > 0 {
		var remaining float64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(GREATEST(EXTRACT(epoch FROM (max(updated_at)-clock_timestamp()))+$3::double precision,0),0) FROM agent_outbound_messages WHERE conversation_id=$1 AND id<>$2 AND status IN ('sending','sent','unknown')`, scope.sourceID, id, float64(limits.MinIntervalMS)/1000).Scan(&remaining); err != nil {
			return err
		}
		if remaining > 0 {
			delay := time.Duration(remaining * float64(time.Second))
			if !continued {
				if scope.control || r.authorize == nil {
					return &domain.IntervalWait{Remaining: delay}
				}
				return &domain.GateError{Reason: domain.GateInterval, RetryAfter: delay}
			}
			if err := postponeContinuation(ctx, tx, delivery.job, claim, delay, domain.GateInterval); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			return &domain.GateError{Reason: domain.GateInterval}
		}
	}
	if returned && reason == "" {
		if err := restoreReservation(ctx, tx, id, claim, limits); err != nil {
			var budget *domain.BudgetError
			if !errors.As(err, &budget) {
				return err
			}
			reason = domain.GateBudget
		} else {
			returned = false
		}
	}
	switch reason {
	case "":
		moved, err := tx.Exec(ctx, `UPDATE agent_outbound_messages o SET status='sending',updated_at=clock_timestamp()
 WHERE o.id=$1 AND o.status='prepared' AND o.claim_token=$2::uuid AND EXISTS (
 SELECT 1 FROM message_jobs j WHERE j.id=o.job_id AND j.status='processing' AND j.claim_token=o.claim_token AND j.lease_expires_at>clock_timestamp())`, id, claim)
		if err != nil {
			return err
		}
		if moved.RowsAffected() != 1 {
			return ErrLeaseLost
		}
	case domain.GatePending:
		if continued {
			if err := postponeContinuation(ctx, tx, delivery.job, claim, 200*time.Millisecond, domain.GatePending); err != nil {
				return err
			}
		} else {
			if err := handOffDelivery(ctx, tx, delivery.job, claim); err != nil {
				return err
			}
		}
	default:
		if returned {
			if err := cancelRefunded(ctx, tx, id, claim, reason); err != nil {
				return err
			}
		} else if err := cancelAtGate(ctx, tx, id, claim, reason); err != nil {
			return err
		}
		if continued {
			if err := saveContinuationResult(ctx, tx, id); err != nil {
				return err
			}
			if err := finishContinuation(ctx, tx, delivery.job, claim); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if reason != "" {
		return &domain.GateError{Reason: reason, HandedOff: reason == domain.GatePending && !continued}
	}
	return nil
}

func databaseNow(ctx context.Context, tx *db.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

func lockDeliveryTopic(ctx context.Context, tx *db.Tx, id string, scope deliveryPolicyScope) (preparedDelivery, string, error) {
	var d preparedDelivery
	err := tx.QueryRow(ctx, `SELECT j.id,COALESCE(j.incoming_message_id,0),COALESCE(i.topic_id,0),COALESCE(i.version,0)
 FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id
 LEFT JOIN group_topic_inputs i ON i.route_id=j.incoming_message_id AND i.conversation_id=o.conversation_id
 WHERE o.id=$1`, id).Scan(&d.job, &d.input, &d.topic, &d.version)
	if err != nil {
		return d, "", err
	}
	if scope.control {
		return d, "", nil
	}
	if d.topic == 0 {
		var protected bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM intent_decisions d JOIN intent_decision_snapshots s ON s.decision_id=d.id
 WHERE d.job_id=$1 AND jsonb_typeof(s.input->'TopicChange')='object'
 AND d.id=(SELECT max(id) FROM intent_decisions WHERE job_id=$1))`, d.job).Scan(&protected); err != nil {
			return d, "", err
		}
		if protected {
			return d, domain.GateStale, nil
		}
		return d, "", nil
	}
	var current int64
	var active bool
	var decidedTopic int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE((
 SELECT CASE WHEN jsonb_typeof(s.input->'TopicChange')='object' THEN (s.input->'TopicChange'->>'ID')::bigint END
 FROM intent_decisions i LEFT JOIN intent_decision_snapshots s ON s.decision_id=i.id
 WHERE i.job_id=$1 ORDER BY i.id DESC LIMIT 1),0)`, d.job).Scan(&decidedTopic); err != nil {
		return d, "", err
	}
	if decidedTopic != 0 && decidedTopic != d.topic {
		return d, domain.GateStale, nil
	}
	err = tx.QueryRow(ctx, `SELECT version,NOT closed AND expires_at>clock_timestamp() FROM group_topics WHERE id=$1 AND conversation_id=$2 FOR UPDATE`, d.topic, scope.sourceID).Scan(&current, &active)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!active || current != d.version) {
		return d, domain.GateStale, nil
	}
	if err != nil {
		return d, "", err
	}
	return d, "", nil
}

func deliveryRouteReason(ctx context.Context, tx *db.Tx, d preparedDelivery, now time.Time) (string, error) {
	if d.topic == 0 {
		return "", nil
	}
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM group_topic_route_candidates p JOIN group_topic_routes r ON (r.id,r.conversation_id)=(p.route_id,p.conversation_id)
 WHERE p.topic_id=$1 AND p.version<=$2 AND r.id>$3 AND r.status IN ('pending','routing') AND r.deadline>$4)`, d.topic, d.version, d.input, now).Scan(&pending)
	if pending {
		return domain.GatePending, err
	}
	return "", err
}

func groupDeliveryOrder(ctx context.Context, tx *db.Tx, scope deliveryPolicyScope, d preparedDelivery, sequence int) (string, error) {
	var head int64
	var earlierSlot bool
	err := tx.QueryRow(ctx, `SELECT COALESCE((
 SELECT j.id FROM messages m JOIN message_jobs j ON j.incoming_message_id=m.id`+ordering.GroupInputOrderJoins+`
 WHERE m.conversation_id=$1 AND m.direction='incoming' AND m.bot_id=$2
 AND (j.id=$3 OR (j.status IN ('pending','processing') AND (j.delivery_continuation OR
  (SELECT decision FROM intent_decisions WHERE job_id=j.id ORDER BY id DESC LIMIT 1)='reply')))
 ORDER BY `+ordering.GroupInputOrderKey+` LIMIT 1),0), EXISTS (
 SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=$3 AND o.sequence<$4
 AND (o.status IN ('prepared','sending') OR (o.status='cancelled' AND o.error_code='lease_recovered' AND o.resume_digest IS NOT NULL))
 )`, scope.sourceID, scope.botID, d.job, sequence).Scan(&head, &earlierSlot)
	if err != nil {
		return "", err
	}
	if head != d.job || earlierSlot {
		return domain.GatePending, nil
	}
	return "", nil
}

func restoreReservation(ctx context.Context, tx *db.Tx, id, claim string, limits domain.Limits) error {
	var usage domain.Usage
	var cost, count, chars int
	err := tx.QueryRow(ctx, `SELECT b.reserved_messages,b.reserved_chars,o.charge_chars,
 (SELECT count(*) FROM agent_outbound_messages p WHERE p.job_id=o.job_id AND p.status IN ('prepared','sending','sent','unknown','failed')),
 (SELECT COALESCE(sum(p.charge_chars),0) FROM agent_outbound_messages p WHERE p.job_id=o.job_id AND p.status IN ('prepared','sending','sent','unknown','failed'))
 FROM agent_outbound_messages o JOIN agent_run_budgets b ON b.job_id=o.job_id
 WHERE o.id=$1 AND o.status='cancelled' AND o.error_code='lease_recovered' AND o.claim_token=$2`, id, claim).Scan(&usage.Messages, &usage.Chars, &cost, &count, &chars)
	if err != nil {
		return err
	}
	if usage.Messages != count || usage.Chars != chars {
		return &domain.BudgetError{Dimension: domain.DimensionMessages, Requested: count + 1, Limit: usage.Messages}
	}
	if err := domain.CheckBudgetChars(limits, usage, cost); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT restore_reservation`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `WITH restored AS (
 UPDATE agent_outbound_messages SET status='prepared',error_code=NULL,created_at=clock_timestamp(),updated_at=clock_timestamp()
 WHERE id=$1 AND status='cancelled' AND error_code='lease_recovered' AND claim_token=$2 RETURNING job_id,charge_chars
 ) UPDATE agent_run_budgets b SET reserved_messages=reserved_messages+1,reserved_chars=reserved_chars+r.charge_chars
 FROM restored r WHERE b.job_id=r.job_id`, id, claim); err != nil {
		return err
	}
	if err := validateReserved(ctx, tx, id, limits); err != nil {
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT restore_reservation`); rollbackErr != nil {
			return rollbackErr
		}
		return err
	}
	_, err = tx.Exec(ctx, `RELEASE SAVEPOINT restore_reservation`)
	return err
}

func cancelRefunded(ctx context.Context, tx *db.Tx, id, claim, reason string) error {
	result, err := tx.Exec(ctx, `WITH cancelled AS (UPDATE agent_outbound_messages SET error_code=$3,updated_at=clock_timestamp()
 WHERE id=$1 AND claim_token=$2 AND status='cancelled' AND error_code='lease_recovered' RETURNING job_id)
 UPDATE message_jobs j SET delivery_outcome='blocked',delivery_reason=CASE WHEN j.delivery_reason=$4 THEN j.delivery_reason ELSE $3 END,denied_count=denied_count+1 FROM cancelled c WHERE c.job_id=j.id`, id, claim, reason, consentEvidenceMissing)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func cancelAtGate(ctx context.Context, tx *db.Tx, id, claim, reason string) error {
	return cancelUnsent(ctx, tx, id, claim, reason, domain.StatusPrepared)
}

func cancelUnsent(ctx context.Context, tx *db.Tx, id, claim, reason string, status domain.Status) error {
	result, err := tx.Exec(ctx, `WITH cancelled AS (
 UPDATE agent_outbound_messages o SET status='cancelled',error_code=$3,updated_at=clock_timestamp()
 WHERE o.id=$1 AND o.claim_token=$2::uuid AND o.status=$4 AND o.platform_message_id IS NULL AND EXISTS (
 SELECT 1 FROM message_jobs j WHERE j.id=o.job_id AND j.claim_token=o.claim_token AND j.lease_expires_at>clock_timestamp())
 RETURNING job_id,charge_chars
 ), refunded AS (
 UPDATE agent_run_budgets b SET reserved_messages=b.reserved_messages-1,reserved_chars=b.reserved_chars-c.charge_chars,updated_at=clock_timestamp()
 FROM cancelled c WHERE b.job_id=c.job_id RETURNING b.job_id
 ) UPDATE message_jobs j SET delivery_outcome='blocked',delivery_reason=CASE WHEN j.delivery_reason=$5 THEN j.delivery_reason ELSE $3 END,denied_count=denied_count+1
 FROM refunded f WHERE f.job_id=j.id`, id, claim, reason, status, consentEvidenceMissing)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

type sendPermitSide struct {
	BotID            int64             `json:"bot_id"`
	Kind             string            `json:"scope_type"`
	Target           int64             `json:"scope_id"`
	Selection        string            `json:"selection_generation"`
	PolicyInstance   string            `json:"policy_instance"`
	Capabilities     map[string]string `json:"policy_generations"`
	DefaultsInstance string            `json:"defaults_instance,omitempty"`
	Defaults         map[string]string `json:"defaults_generations,omitempty"`
}

type sendingPolicy struct {
	scoped.Policy
	instance    string
	selection   string
	generations map[string]string
}

func sameSendPermit(stored, current []byte) bool {
	if len(stored) == 0 || len(current) == 0 {
		return false
	}
	var persisted []sendPermitSide
	if err := json.Unmarshal(stored, &persisted); err != nil || len(persisted) == 0 {
		return false
	}
	canonical, err := json.Marshal(persisted)
	return err == nil && bytes.Equal(canonical, current)
}

func (r *Repo) currentSendingPolicy(ctx context.Context, tx *db.Tx, scope deliveryPolicyScope, d preparedDelivery) ([]byte, domain.Limits, error) {
	src, limits, err := loadSendingPolicy(ctx, tx, scope.botID, scope.sourceKind, scope.sourceTarget)
	if err != nil {
		return nil, limits, err
	}
	if !src.Enabled || !src.StoreMessages || src.ReplyMode == scoped.ModeObserveOnly || (d.topic != 0 && !src.AllowHistoryRead) || (scope.derived && !src.AllowMemoryRead) {
		return nil, limits, nil
	}
	source := src.permitSide(scope.botID, scope.sourceKind, scope.sourceTarget, ":state", ":store")
	if scope.derived {
		source.Capabilities[":memory"] = src.generations[":memory"]
	}
	if d.topic != 0 {
		source.Capabilities[":history"] = src.generations[":history"]
	}
	cross := scope.sourceKind != scope.targetKind || scope.sourceTarget != scope.targetID
	var target *sendPermitSide
	if cross {
		policy, _, err := loadSendingPolicy(ctx, tx, scope.botID, scope.targetKind, scope.targetID)
		if err != nil {
			return nil, limits, err
		}
		if !policy.Enabled || policy.ReplyMode == scoped.ModeObserveOnly {
			return nil, limits, nil
		}
		side := policy.permitSide(scope.botID, scope.targetKind, scope.targetID, ":state")
		target = &side
	}
	if scope.hasMemoryReads {
		if !src.AllowMemoryRead {
			return nil, limits, nil
		}
		if r.memoryReads == nil {
			return nil, limits, domain.ErrConsentSendDenied
		}
		var refs []memory.ReadDependency
		if err := json.Unmarshal(d.memoryDependencies, &refs); err != nil {
			return nil, limits, err
		}
		valid, err := r.memoryReads.ValidateDeliveryReads(ctx, tx, memory.DeliveryScope{
			BotID: scope.botID, Kind: scope.sourceKind, TargetID: scope.sourceTarget,
			ConversationID: scope.sourceID, AllowHistoryRead: src.AllowHistoryRead,
		}, refs)
		if err != nil || !valid {
			return nil, limits, err
		}
		source.Capabilities[":memory"] = src.generations[":memory"]
		for _, ref := range refs {
			if ref.SourceMessageID != 0 {
				source.Capabilities[":history"] = src.generations[":history"]
				break
			}
		}
	}
	// Lock settings last. Numeric budgets have their own validation and do not rotate authorization.
	// 最后锁设置行。数值预算独立校验，不推进发送许可代际。
	var wire, generationsWire []byte
	var globalInstance string
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('settings:bootstrap',0))`); err != nil {
		return nil, limits, err
	}
	err = tx.QueryRow(ctx, `SELECT payload,send_permit_instance,send_permit_generations FROM system_settings WHERE id=1 FOR SHARE`).Scan(&wire, &globalInstance, &generationsWire)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, limits, nil
	}
	if err != nil {
		return nil, limits, err
	}
	var live settings.Settings
	if err := json.Unmarshal(wire, &live); err != nil {
		return nil, limits, err
	}
	var globalGenerations map[string]string
	if err := json.Unmarshal(generationsWire, &globalGenerations); err != nil {
		return nil, limits, err
	}
	grants := plugins.ResolvePermissions(r.authorize(), live.Tools.PermissionDefaults, src.ToolPermissions)
	required := map[string]bool{"qq.reply.current": true}
	if cross {
		required["qq.cross_conversation"] = true
	}
	if d.action == "qq.send_voice_current" {
		required["media.voice.send"] = true
		required["qq.content.media"] = true
	} else {
		var body struct {
			Segments json.RawMessage `json:"segments"`
			Params   json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(d.payload), &body); err != nil {
			return nil, limits, err
		}
		if len(body.Params) > 0 {
			segments, err := ob.SegmentsFromWire(body.Segments)
			if err != nil {
				return nil, limits, err
			}
			checked := plugins.Scope{Permissions: grants, Reauthorize: func(_ context.Context, desc pluginc.ToolDescriptor) error {
				required[desc.Permission] = true
				return nil
			}}
			if _, err := plugins.CheckMessageContent(plugins.WithScope(ctx, checked), ob.PublicCommand{Segments: segments}); err != nil {
				return nil, limits, nil
			}
		}
	}
	for permission := range required {
		if !grants[permission] {
			return nil, limits, nil
		}
		source.Capabilities[permission] = src.generations[permission]
		if _, overridden := src.ToolPermissions[permission]; !overridden {
			if source.Defaults == nil {
				source.Defaults = make(map[string]string)
			}
			source.DefaultsInstance = globalInstance
			source.Defaults[permission] = globalGenerations[permission]
		}
	}
	sides := []sendPermitSide{source}
	if target != nil {
		sides = append(sides, *target)
	}
	permit, err := json.Marshal(sides)
	return permit, limits, err
}

func (p sendingPolicy) permitSide(bot int64, kind string, target int64, checks ...string) sendPermitSide {
	// An empty capability generation is the initial generation within this policy instance.
	// 空能力代际表示当前策略实例内的初始代际。
	side := sendPermitSide{BotID: bot, Kind: kind, Target: target, Selection: p.selection, PolicyInstance: p.instance, Capabilities: make(map[string]string, len(checks))}
	for _, check := range checks {
		side.Capabilities[check] = p.generations[check]
	}
	return side
}

func loadSendingPolicy(ctx context.Context, tx *db.Tx, bot int64, kind string, target int64) (sendingPolicy, domain.Limits, error) {
	var p sendingPolicy
	var permissions, generations []byte
	var l domain.Limits
	err := tx.QueryRow(ctx, `SELECT enabled,store_messages,allow_history_read,allow_memory_read,reply_mode,tool_permissions,
 max_messages_per_run,max_chars_per_message,max_total_chars_per_run,max_replies_per_hour,message_interval_ms,
 send_permit_instance,send_permit_generations,COALESCE((SELECT id::text FROM send_scope_selections s
 WHERE s.bot_id=$1 AND s.scope_type=$2 AND s.scope_id=$3),'')
 FROM scope_policies WHERE bot_id=$1 AND scope_type=$2 AND scope_id IN ($3,0) ORDER BY scope_id DESC LIMIT 1`, bot, kind, target).Scan(
		&p.Enabled, &p.StoreMessages, &p.AllowHistoryRead, &p.AllowMemoryRead, &p.ReplyMode, &permissions, &l.MaxMessages, &l.MaxCharsPerMessage, &l.MaxTotalChars, &l.MaxPerHour, &l.MinIntervalMS, &p.instance, &generations, &p.selection)
	if errors.Is(err, sql.ErrNoRows) {
		p.Policy = scoped.Default(scoped.ScopeType(kind), target)
		return p, l, nil
	}
	if err != nil {
		return p, l, err
	}
	if err := json.Unmarshal(permissions, &p.ToolPermissions); err != nil {
		return p, l, fmt.Errorf("outbound: decode policy permissions: %w", err)
	}
	if err := json.Unmarshal(generations, &p.generations); err != nil {
		return p, l, fmt.Errorf("outbound: decode policy generations: %w", err)
	}
	p.ToolPermissions = pluginc.NormalizePermissionOverrides(p.ToolPermissions)
	p.Policy = p.Policy.WithDefaults()
	return p, l, nil
}

// resumableText accepts the normalized text/face/mention/reply segments already held in the slot.
// resumableText 仅接受原槽中已留存的规范化文本、表情、提及和引用段。
func resumableText(payload string) bool {
	var wire struct {
		Segments json.RawMessage `json:"segments"`
	}
	if json.Unmarshal([]byte(payload), &wire) != nil {
		return false
	}
	parts, err := ob.SegmentsFromWire(wire.Segments)
	if err != nil || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		switch part.Type {
		case ob.SegText, ob.SegFace, ob.SegAt, ob.SegReply:
		default:
			return false
		}
	}
	return true
}

func consentMarker(payload string) (bool, string, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &body); err != nil || body == nil {
		return false, "", domain.ErrConsentSendDenied
	}
	var derived bool
	var digest string
	marker, marked := body["cross_group_fact"]
	rawDigest, hasDigest := body["consent_read_digest"]
	if (marked && (json.Unmarshal(marker, &derived) != nil || !derived)) ||
		(hasDigest && (json.Unmarshal(rawDigest, &digest) != nil || digest == "")) || marked != hasDigest {
		return false, "", domain.ErrConsentSendDenied
	}
	return derived, digest, nil
}
