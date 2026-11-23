package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
)

const livePolicyJoin = ` FROM conversations c JOIN LATERAL (
 SELECT id,version,enabled,store_messages,allow_history_read FROM scope_policies p
 WHERE p.bot_id=c.bot_id AND p.scope_type=c.kind AND p.scope_id IN (0,c.target_id)
 ORDER BY p.scope_id DESC LIMIT 1
 ) p ON p.enabled AND p.store_messages WHERE c.bot_id=`

const liveScope = `WITH visible AS MATERIALIZED (SELECT c.id,p.allow_history_read` + livePolicyJoin + `$1) `

// The account policy stamp covers overrides and template fallback.
// 账号策略摘要覆盖独立策略及模板回退。
const scopeStamp = `SELECT encode(sha256(convert_to(COALESCE(string_agg(id::text||':'||version,',' ORDER BY id),'')||'/'||COALESCE((SELECT retention_revision FROM intent_stream_heads WHERE bot_id=$1),0),'UTF8')),'hex') FROM scope_policies WHERE bot_id=$1`

func (r *DecisionsRepo) DecisionAuditAccess(ctx context.Context, bot int64, conversation string) (visible, history bool, err error) {
	err = r.pool.QueryRow(ctx, liveScope+`SELECT EXISTS(SELECT 1 FROM visible WHERE id=$2::uuid),COALESCE((SELECT allow_history_read FROM visible WHERE id=$2::uuid),false)`, bot, conversation).Scan(&visible, &history)
	return
}

// DecisionDeliveryTargetAccess applies the current policy of the actual destination.
// DecisionDeliveryTargetAccess 应用实际目标的现行策略。
func (r *DecisionsRepo) DecisionDeliveryTargetAccess(ctx context.Context, bot int64, kind string, target int64) (visible, history bool, err error) {
	err = r.pool.QueryRow(ctx, `SELECT COALESCE(p.enabled AND p.store_messages,false),COALESCE(p.enabled AND p.store_messages AND p.allow_history_read,false)
	 FROM (SELECT 1) x LEFT JOIN LATERAL (SELECT enabled,store_messages,allow_history_read FROM scope_policies
	 WHERE bot_id=$1 AND scope_type=$2 AND scope_id IN(0,$3) ORDER BY scope_id DESC LIMIT 1) p ON true`, bot, kind, target).Scan(&visible, &history)
	return
}

func (r *DecisionsRepo) DecisionJobAccess(ctx context.Context, bot, job int64) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx, liveScope+`SELECT EXISTS(SELECT 1 FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id JOIN visible v ON v.id=m.conversation_id WHERE m.bot_id=$1 AND j.id=$2)`, bot, job).Scan(&allowed)
	return allowed, err
}

// DecisionHistorySources resolves platform IDs only within their recorded conversation.
// DecisionHistorySources 仅从原会话内定位平台消息 ID。
func (r *DecisionsRepo) DecisionHistorySources(ctx context.Context, bot int64, conversation string, platformIDs []int64) (map[int64]bool, error) {
	raw, err := json.Marshal(platformIDs)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, liveScope+`SELECT DISTINCT m.platform_message_id FROM messages m JOIN visible v ON v.id=m.conversation_id
 WHERE m.bot_id=$1 AND m.conversation_id=$2::uuid AND m.platform_message_id IN(SELECT value::bigint FROM jsonb_array_elements_text($3::jsonb)) AND v.allow_history_read`, bot, conversation, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	return allowed, rows.Err()
}

// DecisionSourceAccess checks that a retained message still belongs to the authorized source.
// DecisionSourceAccess 核对仍留存的消息属于获准读取的来源。
func (r *DecisionsRepo) DecisionSourceAccess(ctx context.Context, bot int64, conversation string, message int64) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx, liveScope+`SELECT EXISTS(SELECT 1 FROM messages m JOIN visible v ON v.id=m.conversation_id
	 WHERE m.bot_id=$1 AND m.id=$2 AND (NULLIF($3,'')::uuid IS NULL OR m.conversation_id=NULLIF($3,'')::uuid) AND v.allow_history_read)`, bot, message, conversation).Scan(&allowed)
	return allowed, err
}

func (r *DecisionsRepo) DecisionScope(ctx context.Context, bot int64) (string, error) {
	var scope string
	err := r.pool.QueryRow(ctx, scopeStamp, bot).Scan(&scope)
	return scope, err
}

func (r *DecisionsRepo) DecisionRunSnapshot(ctx context.Context, bot int64, before contracts.LiveHistoryKey) (contracts.LiveSnapshot, error) {
	return r.decisionRunSnapshot(ctx, bot, before, "")
}

// DecisionRunSnapshotSelected includes the authorized selection in the same snapshot as the page.
// DecisionRunSnapshotSelected 在同一读视图返回页面和已核准的所选运行。
func (r *DecisionsRepo) DecisionRunSnapshotSelected(ctx context.Context, bot int64, before contracts.LiveHistoryKey, selected string) (contracts.LiveSnapshot, error) {
	return r.decisionRunSnapshot(ctx, bot, before, selected)
}

func (r *DecisionsRepo) decisionRunSnapshot(ctx context.Context, bot int64, before contracts.LiveHistoryKey, selected string) (contracts.LiveSnapshot, error) {
	out := contracts.LiveSnapshot{Rows: []contracts.DecisionLiveRow{}}
	tx, err := r.pool.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, scopeStamp, bot).Scan(&out.Scope); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT position FROM intent_stream_heads WHERE bot_id=$1),0)`, bot).Scan(&out.Position); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, liveScope+`, candidates AS (
 SELECT r.id,r.conversation_id,r.decision_id,r.started_at,r.state,'r:'||r.id::text AS row_key
 FROM intent_decision_runs r JOIN visible v ON v.id=r.conversation_id WHERE r.bot_id=$1
 UNION ALL
 SELECT NULL,m.conversation_id,d.id,d.created_at,'unknown','d:'||d.id::text
 FROM intent_decisions d JOIN message_jobs j ON j.id=d.job_id JOIN messages m ON m.id=j.incoming_message_id
 JOIN visible v ON v.id=m.conversation_id LEFT JOIN intent_decision_runs r ON r.decision_id=d.id
 WHERE m.bot_id=$1 AND r.id IS NULL
 ), page AS (
 SELECT * FROM candidates WHERE state<>'running' AND ($3='' OR (started_at,row_key)<($2,$3))
 ORDER BY started_at DESC,row_key DESC LIMIT 101
 ), selected AS (
 SELECT *,false AS pinned FROM page
 UNION ALL SELECT *,true AS pinned FROM candidates WHERE $3='' AND (state='running' OR (row_key=$4 AND NOT EXISTS(SELECT 1 FROM (SELECT row_key FROM page ORDER BY started_at DESC,row_key DESC LIMIT 100) p WHERE p.row_key=$4)))
 ) SELECT COALESCE(c.id::text,''),c.conversation_id,COALESCE(c.decision_id,0),c.started_at,c.state,c.row_key,c.pinned,
 COALESCE(d.decision,''),COALESCE(d.reason_code,''),COALESCE(ev.events,'[]'::jsonb)
 FROM selected c LEFT JOIN intent_decisions d ON d.id=c.decision_id
 LEFT JOIN LATERAL (SELECT jsonb_agg(jsonb_build_object('RunID',e.run_id,'ConversationID',c.conversation_id,'StartedAt',c.started_at,'Sequence',e.sequence,'Kind',e.kind,'At',e.created_at,'Data',e.data) ORDER BY e.sequence) AS events
 FROM intent_run_events e WHERE e.run_id=c.id) ev ON true
 ORDER BY c.pinned DESC,c.started_at DESC,c.row_key DESC`, bot, before.StartedAt, before.RowKey, selected)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last contracts.LiveHistoryKey
	nonPinned := 0
	for rows.Next() {
		var r contracts.DecisionLiveRow
		var key string
		var raw []byte
		var pinned bool
		if err := rows.Scan(&r.RunID, &r.ConversationID, &r.DecisionID, &r.StartedAt, &r.State, &key, &pinned, &r.Decision, &r.ReasonCode, &raw); err != nil {
			return out, err
		}
		if !pinned && nonPinned == 100 {
			out.Next = last
			break
		}
		if err := json.Unmarshal(raw, &r.Events); err != nil {
			return out, err
		}
		if !pinned {
			nonPinned++
			last = contracts.LiveHistoryKey{StartedAt: r.StartedAt, RowKey: key}
		}
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit()
}

func (r *DecisionsRepo) DecisionRunBatch(ctx context.Context, bot, after int64, scope string) (contracts.LiveBatch, error) {
	batch := contracts.LiveBatch{Position: after, Events: []contracts.DecisionRunEvent{}}
	tx, err := r.pool.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return batch, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, scopeStamp, bot).Scan(&batch.Scope); err != nil {
		return batch, err
	}
	if batch.Scope != scope {
		batch.Reset = true
		return batch, tx.Commit()
	}
	var head, floor int64
	if err := tx.QueryRowContext(ctx, `SELECT h.pos,GREATEST(0,h.pos-100000,COALESCE((SELECT retained_after FROM intent_stream_heads WHERE bot_id=$1),0),COALESCE((SELECT position FROM intent_run_events WHERE bot_id=$1 AND created_at<=statement_timestamp()-interval '24 hours' ORDER BY created_at DESC,position DESC LIMIT 1),0))
 FROM (SELECT COALESCE((SELECT position FROM intent_stream_heads WHERE bot_id=$1),0) pos) h`, bot).Scan(&head, &floor); err != nil {
		return batch, err
	}
	if after < floor || after > head || head-after > 4096 {
		batch.Reset = true
		return batch, tx.Commit()
	}
	if head == after {
		return batch, tx.Commit()
	}
	batch.Position = min(head, after+256)
	rows, err := tx.QueryContext(ctx, liveScope+`SELECT e.run_id,r.conversation_id,r.started_at,e.sequence,e.kind,e.created_at,e.data
 FROM intent_run_events e JOIN intent_decision_runs r ON r.id=e.run_id JOIN visible v ON v.id=r.conversation_id
 WHERE e.bot_id=$1 AND e.position>$2 AND e.position<=$3 ORDER BY e.position`, bot, after, batch.Position)
	if err != nil {
		return batch, err
	}
	defer rows.Close()
	for rows.Next() {
		var event contracts.DecisionRunEvent
		var raw []byte
		if err := rows.Scan(&event.RunID, &event.ConversationID, &event.StartedAt, &event.Sequence, &event.Kind, &event.At, &raw); err != nil {
			return batch, err
		}
		if err := json.Unmarshal(raw, &event.Data); err != nil {
			return batch, fmt.Errorf("intent: decode run metadata: %w", err)
		}
		batch.Events = append(batch.Events, event)
	}
	if err := rows.Err(); err != nil {
		return batch, err
	}
	rows.Close()
	return batch, tx.Commit()
}
