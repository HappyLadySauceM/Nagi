package infrastructure

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	auditpg "github.com/HappyLadySauceM/Nagi/internal/audit/infrastructure"
	conversation "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	coninfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/google/uuid"
)

func TestInboundPicturesFreezeBeforeWorkerAndPreserveOriginalSegments(t *testing.T) {
	pool := dbtest.Open(t)
	enabled, err := coninfra.NewRepo(pool, coninfra.WithInboundAttachments(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := coninfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const bot int64 = 993551
	fixture := ob.Segment{Type: ob.SegImage, Data: map[string]string{"url": "https://example.invalid/private?signature=fixture"}}
	base := conversation.IngestInput{BotID: bot, SenderID: 200, Kind: conversation.KindGroup, TargetID: 22, PlatformMessageID: time.Now().UnixNano(), Text: "fixture", Segments: []ob.Segment{fixture, ob.Text("fixture"), fixture, {Type: ob.SegImage}}}
	first, err := enabled.Ingest(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	want := media.PictureSummary{Version: 1, ImageCount: 3, Images: []media.PictureState{{SegmentIndex: 0, Status: media.PicturePending}, {SegmentIndex: 2, Status: media.PicturePending}, {SegmentIndex: 3, Status: media.PictureNoSource}}}
	check := func(id int64, ids ...int64) media.InboundMedia {
		t.Helper()
		value, err := decisions.InboundMedia(ctx, bot, id, ids)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := check(first.MessageID)
	if !reflect.DeepEqual(before.AtIngress, &want) || !reflect.DeepEqual(before.Current, want) {
		t.Fatalf("before claiming: %+v", before)
	}
	base.Segments = nil
	dupe, err := disabled.Ingest(ctx, base)
	if err != nil || !dupe.Duplicate {
		t.Fatalf("duplicate=%+v err=%v", dupe, err)
	}
	if got := check(first.MessageID); !reflect.DeepEqual(before, got) {
		t.Fatalf("duplicate changed evidence: %+v", got)
	}
	if _, err = decisions.InboundMedia(ctx, bot+1, first.MessageID, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong account error: %v", err)
	}
	frozen, err := decisions.DecisionPictures(ctx, bot, first.JobID)
	if err != nil || !reflect.DeepEqual(frozen, want) {
		t.Fatalf("frozen=%+v err=%v", frozen, err)
	}
	claim := dbtest.Claim(t, pool, first.JobID)
	if err = decisions.Record(ctx, first.JobID, domain.FinalDecision{Decision: domain.DecisionSilent, Snapshot: &domain.DecisionSnapshot{Version: "fixture-v1", RunID: uuid.NewString(), FlowVersion: "fixture", Input: domain.SnapshotInput{Pictures: &frozen, MediaEvidenceVersion: media.ModelMediaVersion}}}, claim); err != nil {
		t.Fatal(err)
	}
	var decisionID int64
	if err = pool.QueryRow(ctx, "SELECT id FROM intent_decisions WHERE job_id=$1", first.JobID).Scan(&decisionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE inbound_attachments SET status='uploaded' WHERE message_id=$1 AND segment_index=0", first.MessageID); err != nil {
		t.Fatal(err)
	}
	after := check(first.MessageID, decisionID)
	if len(after.Decisions) != 1 || !reflect.DeepEqual(after.Decisions[0].AtDecision, &want) || !reflect.DeepEqual(after.AtIngress, &want) || after.Current.Images[0].Status != media.PictureUploaded || after.Decisions[0].Intent.State != media.MediaNotCalled || after.Decisions[0].Generation.State != media.MediaNotCalled {
		t.Fatalf("frozen evidence drifted: %+v", after)
	}
	base.PlatformMessageID++
	none, err := disabled.Ingest(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if got := check(none.MessageID); got.AtIngress == nil || got.AtIngress.ImageCount != 0 {
		t.Fatalf("zero images=%+v", got)
	}
	base.PlatformMessageID++
	base.Segments = []ob.Segment{fixture}
	off, err := disabled.Ingest(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	got := check(off.MessageID, decisionID)
	if got.AtIngress == nil || got.AtIngress.Images[0].Status != media.PictureArchiveOff || got.Current.Images[0].Status != media.PictureArchiveOff || len(got.Decisions) != 0 {
		t.Fatalf("archive off or unrelated decision=%+v", got)
	}
	if _, err = pool.Exec(ctx, "UPDATE messages SET inbound_media_summary=NULL WHERE id=$1", off.MessageID); err != nil {
		t.Fatal(err)
	}
	if got := check(off.MessageID); got.AtIngress != nil || got.Current.ImageCount != 1 || got.Current.Images[0].Status != media.PictureUnknown {
		t.Fatalf("legacy evidence=%+v", got)
	}
}

func TestIntentMediaRender(t *testing.T) {
	const raw = "[CQ:image,file=fixture-resource]"
	for _, c := range []struct {
		name      string
		task      domain.Task
		wantText  string
		wantShape string
	}{
		{"raw bridge fallback", domain.Task{Message: domain.Message{Text: raw}, Segments: []ob.Segment{{Type: ob.SegImage}}}, "[图片]", media.MediaHint},
		{"missing bridge fallback", domain.Task{Segments: []ob.Segment{{Type: ob.SegImage}}}, "[图片]", media.MediaHint},
		{"current text with image", domain.Task{Message: domain.Message{Text: "fixture hello"}, Segments: []ob.Segment{ob.Text("fixture hello"), {Type: ob.SegImage}}}, "fixture hello", media.MediaTextOnly},
		{"current voice transcription", domain.Task{Message: domain.Message{Text: "fixture transcription"}, Segments: []ob.Segment{{Type: ob.SegRecord}, {Type: ob.SegImage}}}, "fixture transcription", media.MediaTextOnly},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, jev := range []bool{false, true} {
				text, got := renderIntentMessage(c.task, jev)
				if text != c.wantText || got.Shape != c.wantShape || len(got.Images) != 0 || len(got.Hints) != 0 {
					t.Fatalf("jev=%t text=%q shape=%+v want=%q/%s", jev, text, got, c.wantText, c.wantShape)
				}
			}
		})
	}
}

type intentMediaRecorder struct {
	audit.AIRecorder
	mu    sync.Mutex
	calls []media.ModelMediaCall
}

func (*intentMediaRecorder) RecordEvent(_ context.Context, event auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	event.ID = event.OperationID
	return event, nil
}

func (r *intentMediaRecorder) FinishEvent(_ context.Context, _, _, _ string, output []byte) error {
	var call media.ModelMediaCall
	if err := json.Unmarshal(output, &call); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	return nil
}

func TestIntentMediaEvidenceMatchesWirePrompt(t *testing.T) {
	for _, c := range []struct {
		name, template          string
		jev, identity, wantHint bool
		requestCount            int
	}{
		{"Jev image", "", true, false, true, 1},
		{"OpenAI image", "{{state}}\n{{question}}", false, false, true, 5},
		{"OpenAI state omitted", "{{question}}", false, false, false, 5},
		{"OpenAI state restored for v2 identity", "{{question}}", false, true, true, 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			const fixture = "fixture-media-reference"
			const earlier = "fixture-authorized-earlier"
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, err := io.ReadAll(req.Body)
				if err != nil || !json.Valid(body) || strings.Contains(string(body), fixture) || strings.Contains(string(body), "[图片]") != c.wantHint || strings.Contains(string(body), earlier) != c.wantHint {
					t.Errorf("unexpected rendered picture hint in wire request, valid=%t", json.Valid(body))
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if c.jev {
					_, _ = io.WriteString(w, validAnswers)
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"logprobs":{"content":[{"top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-2},{"token":"C","logprob":-3}]}]}}]}`)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			in := testInput()
			in.Task.Message.Text = "[CQ:image,file=" + fixture + "]"
			in.Task.Segments = []ob.Segment{ob.Image(fixture)}
			in.Task.Message.MessageID = 202
			in.Policy.AllowHistoryRead = true
			in.Recent = []domain.Message{{MessageID: 201, Text: earlier}, in.Task.Message}
			recorder := &intentMediaRecorder{}
			ctx := audit.WithRun(context.Background(), recorder, "fixture")
			var err error
			if c.jev {
				client := testClient(t, server.URL)
				client.client = server.Client()
				_, err = client.Decide(ctx, in)
			} else {
				in.Prompt.OpenAI = &domain.OpenAIPrompt{UserTemplate: c.template}
				if c.identity {
					in.Policy.ReplyMode = scopedomain.ModeIntentBased
					in.CallIdentity = &domain.CallIdentity{BotID: in.Task.BotID, CallName: "fixture"}
				}
				client := &LocalClient{Client: server.Client(), Config: JevConfig{Endpoint: server.URL, Model: "fixture", Timeout: 3 * time.Second}}
				_, err = client.Decide(ctx, in)
			}
			if err != nil || requests.Load() != int32(c.requestCount) || len(recorder.calls) != c.requestCount {
				t.Fatalf("err=%v requests=%d audit=%d", err, requests.Load(), len(recorder.calls))
			}
			wantShape := media.MediaTextOnly
			if c.wantHint {
				wantShape = media.MediaHint
			}
			for _, call := range recorder.calls {
				if call.Outcome != media.MediaSucceeded || len(call.Attempts) != 1 || call.Attempts[0].Shape != wantShape || call.Attempts[0].State != media.MediaWritten {
					t.Fatalf("report=%+v want=%s", call, wantShape)
				}
			}
			t.Logf("provider_jev=%t state_contains_hint=%t physical_requests=%d shape=%s", c.jev, c.wantHint, c.requestCount, wantShape)
		})
	}
}

func TestLocalMediaResponseStatesPreserveAdapterResult(t *testing.T) {
	const answer = `{"choices":[{"logprobs":{"content":[{"top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-2}]}]}}]}`
	for _, c := range []struct {
		mode, state string
		wantError   bool
	}{
		{"complete", media.MediaBodyComplete, false},
		{"decodable-truncation", media.MediaBodyClosedEarly, false},
		{"decodable-read-error", media.MediaBodyReadFailed, false},
		{"undecodable-truncation", media.MediaBodyReadFailed, true},
		{"early-close", media.MediaBodyClosedEarly, false},
		{"chunked-close", media.MediaBodyClosedUnknown, false},
		{"http2-length-without-end", media.MediaBodyClosedUnknown, false},
	} {
		t.Run(c.mode, func(t *testing.T) {
			var served atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				served.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch c.mode {
				case "decodable-read-error":
					var compressed bytes.Buffer
					gz := gzip.NewWriter(&compressed)
					_, _ = io.WriteString(gz, answer)
					_ = gz.Close()
					data := compressed.Bytes()
					data[len(data)-8] ^= 1
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = w.Write(data)
					return
				case "decodable-truncation", "undecodable-truncation":
					w.Header().Set("Connection", "close")
					w.Header().Set("Content-Length", fmt.Sprint(len(answer)+1))
					if c.wantError {
						_, _ = io.WriteString(w, answer[:len(answer)-5])
						return
					}
				case "early-close", "chunked-close", "http2-length-without-end":
					if c.mode == "http2-length-without-end" {
						if r.ProtoMajor != 2 {
							t.Error("expected HTTP/2")
						}
						w.Header().Set("Content-Length", fmt.Sprint(len(answer)))
					}
					if c.mode == "early-close" {
						w.Header().Set("Content-Length", fmt.Sprint(len(answer)+2048))
					}
					_, _ = io.WriteString(w, answer)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, answer)
			}))
			if c.mode == "http2-length-without-end" {
				server.EnableHTTP2 = true
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			recorder := &intentMediaRecorder{}
			ctx, cancel := context.WithTimeout(audit.WithRun(context.Background(), recorder, "fixture"), time.Second)
			defer cancel()
			client := &LocalClient{Client: server.Client(), Config: JevConfig{Endpoint: server.URL, Model: "fixture"}}
			var requests atomic.Int32
			scores, err := client.score(ctx, "fixture", "{}", "fixture", []string{"A", "B"}, "{{state}}{{question}}", media.ModelMediaAttempt{Shape: media.MediaTextOnly}, &requests)
			if (err != nil) != c.wantError || err == nil && (len(scores) != 2 || scores[0] <= scores[1]) || served.Load() != 1 || requests.Load() != 1 || len(recorder.calls) != 1 {
				t.Fatalf("scores=%v err=%v requests=%d received=%d", scores, err, requests.Load(), served.Load())
			}
			report := recorder.calls[0]
			wantWriteState := media.MediaUnknown
			if c.mode == "http2-length-without-end" {
				wantWriteState = media.MediaWritten
			}
			if len(report.Attempts) != 1 || report.Attempts[0].State != wantWriteState || report.Attempts[0].HTTPStatus != http.StatusOK || report.Attempts[0].ResponseBody != c.state || (report.Outcome == media.MediaFailed) != (c.state == media.MediaBodyReadFailed) {
				t.Fatalf("audit=%+v", report)
			}
			t.Logf("request_count=1 original_decoder_succeeded=%t body=%s status=200 audit_outcome=%s", err == nil, c.state, report.Outcome)
		})
	}
}

func TestMediaProjectionPreservesKnownFactsOnUnfinishedEvent(t *testing.T) {
	headers, err := json.Marshal(media.ModelMediaCall{
		Outcome: media.MediaStarted, HTTPStatus: http.StatusAccepted, AttemptCountUnknown: true,
		Attempts: []media.ModelMediaAttempt{{State: media.MediaWritten, Shape: media.MediaImage, Images: []int{2}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var active, ended media.ModelMedia
	appendMediaCall(&active, "started", headers, true)
	appendMediaCall(&ended, "started", headers, false)
	if active.State != media.MediaStarted || len(active.Calls) != 1 || active.Calls[0].Outcome != media.MediaStarted {
		t.Fatalf("active=%+v", active)
	}
	if ended.State != media.MediaCompleted || len(ended.Calls) != 1 {
		t.Fatalf("ended=%+v", ended)
	}
	row := ended.Calls[0]
	if row.Outcome != media.MediaUnknown || row.ResponseBody != media.MediaUnknown || row.HTTPStatus != http.StatusAccepted || !row.AttemptCountUnknown || len(row.Attempts) != 1 || row.Attempts[0].State != media.MediaWritten || !reflect.DeepEqual(row.Attempts[0].Images, []int{2}) {
		t.Fatalf("known facts changed: %+v", row)
	}
}

func TestMediaProjectionUsesOriginalWorkerLease(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	repo, err := coninfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	events, err := auditpg.NewAIRecorder(pool)
	if err != nil {
		t.Fatal(err)
	}
	const bot = 993552
	incoming, err := repo.Ingest(ctx, conversation.IngestInput{BotID: bot, SenderID: 200, Kind: conversation.KindGroup, TargetID: 23, PlatformMessageID: time.Now().UnixNano(), Text: "media lease fixture"})
	if err != nil {
		t.Fatal(err)
	}
	token := dbtest.Claim(t, pool, incoming.JobID)
	var convID string
	if err = pool.QueryRow(ctx, "SELECT conversation_id::text FROM messages WHERE id=$1", incoming.MessageID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	run, err := events.StartRun(ctx, auditdomain.AIRun{OperationID: fmt.Sprintf("job:%d:claim:%s", incoming.JobID, token), JobID: incoming.JobID, BotID: bot, ConversationID: convID})
	if err != nil {
		t.Fatal(err)
	}
	call, err := events.RecordEvent(ctx, auditdomain.AIEvent{RunID: run.ID, OperationID: "generation.media_request/1", Stage: "generation.media_request", Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = events.RecordEvent(ctx, auditdomain.AIEvent{RunID: run.ID, OperationID: "agent.generation", Stage: "agent.generation", Sequence: 2})
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(media.ModelMediaCall{Outcome: media.MediaStarted, Attempts: []media.ModelMediaAttempt{{Shape: media.MediaTextOnly, State: media.MediaWritten, HTTPStatus: http.StatusAccepted}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE ai_audit_events SET output=$2 WHERE id=$1", call.ID, output); err != nil {
		t.Fatal(err)
	}
	if err = decisions.Record(ctx, incoming.JobID, domain.FinalDecision{Decision: domain.DecisionReply, Snapshot: &domain.DecisionSnapshot{Version: "fixture-v1", RunID: uuid.NewString(), FlowVersion: "fixture", Input: domain.SnapshotInput{WorkerAuditRunID: run.ID, MediaEvidenceVersion: media.ModelMediaVersion, GenerationEligible: true}}}, token); err != nil {
		t.Fatal(err)
	}
	var decisionID int64
	if err = pool.QueryRow(ctx, "SELECT id FROM intent_decisions WHERE job_id=$1", incoming.JobID).Scan(&decisionID); err != nil {
		t.Fatal(err)
	}
	check := func(state, outcome, response string, startFailed ...bool) {
		t.Helper()
		item, err := decisions.decisionMedia(ctx, bot, incoming.MessageID, decisionID)
		if err != nil {
			t.Fatal(err)
		}
		got := item.Generation
		wantStartFailed := len(startFailed) != 0 && startFailed[0]
		if got.State != state || got.StartFailed != wantStartFailed || len(got.Calls) != 1 || got.Calls[0].Outcome != outcome || len(got.Calls[0].Attempts) != 1 {
			t.Fatalf("generation = %+v", got)
		}
		attempt := got.Calls[0].Attempts[0]
		if attempt.State != media.MediaWritten || attempt.HTTPStatus != http.StatusAccepted || attempt.ResponseBody != response {
			t.Fatalf("attempt = %+v", attempt)
		}
	}
	check(media.MediaStarted, media.MediaStarted, "")
	if _, err = pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=statement_timestamp()-interval '1 second' WHERE id=$1", incoming.JobID); err != nil {
		t.Fatal(err)
	}
	check(media.MediaCompleted, media.MediaUnknown, media.MediaUnknown)
	if err = repo.RecoverStaleJobs(ctx, conversation.RecoveryPolicy{MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	check(media.MediaCompleted, media.MediaUnknown, media.MediaUnknown)
	nextToken := uuid.NewString()
	if _, err = pool.Exec(ctx, "UPDATE message_jobs SET status='processing',claim_token=$2,lease_expires_at=statement_timestamp()+interval '1 minute' WHERE id=$1", incoming.JobID, nextToken); err != nil {
		t.Fatal(err)
	}
	check(media.MediaCompleted, media.MediaUnknown, media.MediaUnknown)
	finished := media.ModelMediaCall{Outcome: media.MediaSucceeded, Attempts: []media.ModelMediaAttempt{{Shape: media.MediaTextOnly, State: media.MediaWritten, HTTPStatus: http.StatusAccepted, ResponseBody: media.MediaBodyComplete}}}
	payload, err := json.Marshal(finished)
	if err != nil {
		t.Fatal(err)
	}
	if err = events.FinishEvent(ctx, call.ID, media.MediaSucceeded, "", payload); err != nil {
		t.Fatal(err)
	}
	check(media.MediaCompleted, media.MediaSucceeded, media.MediaBodyComplete)
	otherRun, err := events.StartRun(ctx, auditdomain.AIRun{OperationID: fmt.Sprintf("job:%d:claim:%s", incoming.JobID, nextToken), JobID: incoming.JobID, BotID: bot, ConversationID: convID})
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{otherRun.ID, run.ID} {
		worker, err := events.RecordEvent(ctx, auditdomain.AIEvent{RunID: runID, OperationID: "worker.process", Stage: "worker.process"})
		if err != nil {
			t.Fatal(err)
		}
		if err = events.FinishEvent(ctx, worker.ID, "failed", audit.ModelMediaStartFailureCode, nil); err != nil {
			t.Fatal(err)
		}
		check(media.MediaCompleted, media.MediaSucceeded, media.MediaBodyComplete, runID == run.ID)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM ai_audit_events WHERE run_id=$1 AND stage='agent.generation'", run.ID); err != nil {
		t.Fatal(err)
	}
	check(media.MediaCompleted, media.MediaSucceeded, media.MediaBodyComplete)
}
