package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
)

const pictureEvidenceQuery = `SELECT jsonb_path_query_array(m.segments, '$[*].type'), m.inbound_media_summary,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('Kind', a.kind, 'SegmentIndex', a.segment_index,
 'Status', CASE WHEN a.expires_at <= now() THEN 'expired' ELSE a.status END, 'FailureCode', a.failure_code))
 FROM inbound_attachments a WHERE a.message_id=m.id AND a.kind='image'), '[]'::jsonb)
 FROM messages m WHERE m.bot_id=$1 AND m.direction='incoming' AND m.id=(`

func (r *DecisionsRepo) pictures(ctx context.Context, botID, key int64, byJob bool) (media.InboundMedia, error) {
	lookup := "$2::bigint"
	if byJob {
		lookup = "SELECT incoming_message_id FROM message_jobs WHERE id=$2"
	}
	var typesJSON, inboundJSON, rowsJSON []byte
	err := r.pool.QueryRow(ctx, pictureEvidenceQuery+lookup+")", botID, key).Scan(&typesJSON, &inboundJSON, &rowsJSON)
	if err != nil {
		return media.InboundMedia{}, err
	}
	var types []string
	var rows []media.InboundAttachment
	if err = json.Unmarshal(typesJSON, &types); err != nil {
		return media.InboundMedia{}, err
	}
	if err = json.Unmarshal(rowsJSON, &rows); err != nil {
		return media.InboundMedia{}, err
	}
	atIngress := media.ReadInboundPictures(inboundJSON)
	return media.InboundMedia{AtIngress: atIngress, Current: media.CurrentPictures(types, atIngress, rows)}, nil
}

func (r *DecisionsRepo) DecisionPictures(ctx context.Context, botID, jobID int64) (media.PictureSummary, error) {
	evidence, err := r.pictures(ctx, botID, jobID, true)
	return evidence.Current, err
}

func (r *DecisionsRepo) InboundMedia(ctx context.Context, botID, messageID int64, decisionIDs []int64) (media.InboundMedia, error) {
	evidence, err := r.pictures(ctx, botID, messageID, false)
	if err != nil {
		return evidence, err
	}
	for _, decisionID := range decisionIDs {
		section, err := r.decisionMedia(ctx, botID, messageID, decisionID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return evidence, err
		}
		evidence.Decisions = append(evidence.Decisions, section)
	}
	return evidence, nil
}

func (r *DecisionsRepo) decisionMedia(ctx context.Context, botID, messageID, decisionID int64) (media.DecisionMedia, error) {
	unknown := media.ModelMedia{Version: media.ModelMediaVersion, State: media.MediaUnknown}
	item := media.DecisionMedia{DecisionID: decisionID, Intent: unknown, Generation: unknown}
	var inputJSON []byte
	var decision, reason string
	var jobID int64
	err := r.pool.QueryRow(ctx, `SELECT s.input,d.decision,d.reason_code,j.id FROM intent_decisions d
 JOIN message_jobs j ON j.id=d.job_id JOIN messages m ON m.id=j.incoming_message_id
 LEFT JOIN intent_decision_snapshots s ON s.decision_id=d.id
 WHERE d.id=$1 AND m.bot_id=$2 AND m.id=$3 AND m.direction='incoming'`, decisionID, botID, messageID).Scan(&inputJSON, &decision, &reason, &jobID)
	if err != nil {
		return item, err
	}
	// Decode the metadata projection only. The rest of the decision input is not served here.
	// 仅解码媒体所需的元数据，其余决策输入不进入此投影。
	var frozen struct {
		Pictures             *media.PictureSummary
		WorkerAuditRunID     string
		MediaEvidenceVersion int
		IntentAsked          bool
		GenerationEligible   bool
	}
	if len(inputJSON) == 0 || json.Unmarshal(inputJSON, &frozen) != nil || frozen.MediaEvidenceVersion != media.ModelMediaVersion {
		return item, nil
	}
	item.AtDecision = media.ReadDecisionPictures(frozen.Pictures)
	item.Intent.StartFailed = reason == audit.ModelMediaStartFailureCode
	if !frozen.IntentAsked {
		item.Intent.State = media.MediaNotCalled
	}
	if decision != string(domain.DecisionReply) || !frozen.GenerationEligible {
		item.Generation.State = media.MediaNotCalled
	}
	if frozen.WorkerAuditRunID == "" {
		return item, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT e.stage,e.status,e.error_code,e.output,
 (e.stage='agent.generation' AND (r.error_code=$5 OR EXISTS (
  SELECT 1 FROM ai_audit_events worker WHERE worker.run_id=r.id AND worker.sequence=0
   AND worker.stage='worker.process' AND worker.operation_id='worker.process'
   AND worker.status='failed' AND worker.error_code=$5))) AS run_start_failed,
 COALESCE(r.status='running' AND j.status='processing' AND j.lease_expires_at>statement_timestamp()
 AND r.operation_id='job:'||j.id::text||':claim:'||j.claim_token::text,false)
 FROM ai_audit_events e
 JOIN ai_audit_runs r ON r.id=e.run_id
 JOIN message_jobs j ON j.id=r.job_id
 JOIN messages m ON m.id=j.incoming_message_id AND m.bot_id=r.bot_id AND m.conversation_id=r.conversation_id
 WHERE r.id::text=$1 AND r.bot_id=$2 AND r.job_id=$3 AND m.id=$4 AND m.direction='incoming'
 AND e.stage IN ('intent.media_request','generation.media_request','agent.generation')
 ORDER BY e.sequence`, frozen.WorkerAuditRunID, botID, jobID, messageID, audit.ModelMediaStartFailureCode)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	for rows.Next() {
		var stage, status, code string
		var output []byte
		var running, runStartFailed bool
		if err = rows.Scan(&stage, &status, &code, &output, &runStartFailed, &running); err != nil {
			return item, err
		}
		switch stage {
		case "intent.media_request":
			appendMediaCall(&item.Intent, status, output, running)
		case "generation.media_request":
			appendMediaCall(&item.Generation, status, output, running)
		case "agent.generation":
			item.Generation.StartFailed = item.Generation.StartFailed || runStartFailed || (status == "failed" && code == audit.ModelMediaStartFailureCode)
			if item.Generation.State == media.MediaUnknown && status == "started" && running && !item.Generation.StartFailed {
				item.Generation.State = media.MediaStarted
			}
		}
	}
	return item, rows.Err()
}

func appendMediaCall(target *media.ModelMedia, status string, output []byte, running bool) {
	var report media.ModelMediaCall
	if err := json.Unmarshal(output, &report); err != nil {
		report = media.ModelMediaCall{}
	}
	if status == "started" {
		if running {
			target.State = media.MediaStarted
			report.Outcome = media.MediaStarted
		} else {
			target.State = media.MediaCompleted
			report.Outcome = media.MediaUnknown
			if report.ResponseBody != "" || report.HTTPStatus != 0 || len(report.Attempts) == 0 {
				report.ResponseBody = media.MediaUnknown
			}
			for i := range report.Attempts {
				report.Attempts[i].ResponseBody = media.MediaUnknown
			}
		}
		target.Calls = append(target.Calls, media.SafeMediaCall(report))
		return
	}
	if report.Outcome == "" {
		return
	}
	if target.State != media.MediaStarted {
		target.State = media.MediaCompleted
	}
	target.Calls = append(target.Calls, media.SafeMediaCall(report))
}
