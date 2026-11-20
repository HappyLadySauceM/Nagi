package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	intent "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scope "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

type selection struct {
	dataset, split, conversation string
	origin, release              string
	bot, first, last             int64
	secret                       []byte
}

type datasetItem struct {
	ID                                  string       `json:"id"`
	DatasetName                         string       `json:"datasetName"`
	Input                               datasetInput `json:"input"`
	Metadata                            itemMetadata `json:"metadata"`
	SourceTraceID                       string       `json:"sourceTraceId,omitempty"`
	snapshotRun                         string
	quotedPlatformID, awaitedPlatformID int64
	quotedBot                           bool
}

type datasetInput struct {
	Current         dialogue   `json:"current"`
	Recent          []dialogue `json:"recent,omitempty"`
	Quoted          *dialogue  `json:"quoted,omitempty"`
	Awaited         *dialogue  `json:"awaited,omitempty"`
	Topics          []topic    `json:"topics,omitempty"`
	DecisionTime    time.Time  `json:"decisionTime,omitempty"`
	BotLastReply    time.Time  `json:"botLastReply,omitzero"`
	RepliesLastHour int        `json:"repliesLastHour"`
	ActivityKnown   bool       `json:"activityKnown"`
}

type dialogue struct {
	ID         string    `json:"id"`
	Sender     string    `json:"sender"`
	Text       string    `json:"text"`
	Types      []string  `json:"segmentTypes,omitempty"`
	Mentions   []string  `json:"mentions,omitempty"`
	References []string  `json:"references,omitempty"`
	At         time.Time `json:"at,omitzero"`
}

type topic struct {
	ID      string     `json:"id"`
	Active  bool       `json:"active"`
	Waiting bool       `json:"waiting"`
	Recent  []dialogue `json:"recent,omitempty"`
}

type itemMetadata struct {
	Schema        string   `json:"schema"`
	Split         string   `json:"split"`
	Conversation  string   `json:"conversation"`
	SourceMessage int64    `json:"sourceMessage"`
	Ruleset       string   `json:"ruleset,omitempty"`
	PromptHash    string   `json:"promptHash,omitempty"`
	PromptVersion int      `json:"promptVersion,omitempty"`
	Missing       []string `json:"missing,omitempty"`
	SourceURL     string   `json:"sourceURL,omitempty"`
	Release       string   `json:"release,omitempty"`
	ReplyMode     string   `json:"replyMode,omitempty"`
	Model         string   `json:"model,omitempty"`
	ActiveTopic   bool     `json:"hadActiveTopic"`
	Media         []string `json:"attachmentAvailability,omitempty"`
	TraceStatus   string   `json:"traceStatus"`
	QuotedMedia   []string `json:"quotedAttachmentAvailability,omitempty"`
	AwaitedMedia  []string `json:"awaitedAttachmentAvailability,omitempty"`
}

func collectDataset(ctx context.Context, pool *db.Pool, spec selection) ([]datasetItem, error) {
	repo, err := infrastructure.NewDecisionsRepo(pool)
	if err != nil {
		return nil, err
	}
	policyRepo, err := scope.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	policies, err := scopeapp.NewService(policyRepo)
	if err != nil {
		return nil, err
	}
	var kind string
	var targetID int64
	err = pool.DB.QueryRowContext(ctx, `SELECT kind, target_id FROM conversations WHERE id=$1 AND bot_id=$2`, spec.conversation, spec.bot).Scan(&kind, &targetID)
	if err != nil {
		return nil, fmt.Errorf("evaluation conversation unavailable")
	}
	policy, err := policies.Resolve(ctx, spec.bot, scopedomain.ScopeType(kind), targetID)
	if err != nil || !policy.Enabled || !policy.StoreMessages || !policy.AllowHistoryRead {
		return nil, fmt.Errorf("evaluation source requires current conversation history authorization")
	}
	var hasArchive bool
	if err := pool.DB.QueryRowContext(ctx, "SELECT to_regclass('inbound_attachments') IS NOT NULL").Scan(&hasArchive); err != nil {
		return nil, fmt.Errorf("check attachment archive")
	}
	var items []datasetItem
	query := intent.InboundQuery{BotID: spec.bot, ConversationID: spec.conversation, BeforeID: spec.last + 1, Limit: 100}
	for {
		rows, err := repo.ListInbound(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("list evaluation source messages: %w", err)
		}
		for _, row := range rows {
			if row.MessageID < spec.first {
				slices.Reverse(items)
				return items, nil
			}
			item, err := makeItem(ctx, repo, spec, row)
			if err != nil {
				return nil, err
			}
			if hasArchive {
				availability, err := archiveAvailability(ctx, pool, row.MessageID)
				if err != nil {
					return nil, err
				}
				item.Metadata.Media = availability
			}
			item.Metadata.Missing = append(item.Metadata.Missing, missingMedia(item.Input.Current.Types, item.Metadata.Media)...)

			for _, evidence := range []struct {
				prefix       string
				dialogue     *dialogue
				platformID   int64
				outgoing     bool
				availability *[]string
			}{
				{"quoted_", item.Input.Quoted, item.quotedPlatformID, item.quotedBot, &item.Metadata.QuotedMedia},
				{"awaited_", item.Input.Awaited, item.awaitedPlatformID, false, &item.Metadata.AwaitedMedia},
			} {
				if evidence.dialogue == nil || len(missingMedia(evidence.dialogue.Types, nil)) == 0 {
					continue
				}
				if hasArchive && !evidence.outgoing && evidence.platformID != 0 {
					var messageID int64
					err := pool.DB.QueryRowContext(ctx, `
						SELECT id FROM messages WHERE bot_id=$1 AND conversation_id=$2 AND direction='incoming'
						  AND platform_message_id=$3 AND id<=$4
					`, spec.bot, spec.conversation, evidence.platformID, row.MessageID).Scan(&messageID)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return nil, fmt.Errorf("resolve evaluation evidence message")
					}
					if messageID != 0 {
						available, err := archiveAvailability(ctx, pool, messageID)
						if err != nil {
							return nil, err
						}
						*evidence.availability = available
					}
				}
				for _, missing := range missingMedia(evidence.dialogue.Types, *evidence.availability) {
					item.Metadata.Missing = append(item.Metadata.Missing, evidence.prefix+missing)
				}
			}
			if row.JobID > 0 && item.snapshotRun != "" {
				var linked string
				err := pool.DB.QueryRowContext(ctx, `
					SELECT r.trace_id FROM ai_audit_runs r JOIN ai_audit_events e ON e.run_id=r.id
					WHERE r.job_id=$1 AND r.bot_id=$2 AND e.stage='intent.decision'
					  AND e.output->'decision'->>'RunID'=$3
					ORDER BY r.started_at DESC LIMIT 1
				`, row.JobID, spec.bot, item.snapshotRun).Scan(&linked)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("read evaluation source trace")
				}
				if len(linked) == 32 {
					item.SourceTraceID = linked
					item.Metadata.TraceStatus = "available"
				}
			}
			items = append(items, item)
			if len(items) > 1000 {
				return nil, fmt.Errorf("selection contains more than 1000 messages")
			}
		}
		if len(rows) < query.Limit {
			slices.Reverse(items)
			return items, nil
		}
		query.BeforeID = rows[len(rows)-1].MessageID
	}
}

func makeItem(ctx context.Context, repo *infrastructure.DecisionsRepo, spec selection, row intent.InboundRecord) (datasetItem, error) {
	item := datasetItem{
		ID: spec.alias("item", spec.dataset+":"+strconv.FormatInt(row.MessageID, 10)), DatasetName: spec.dataset,
		Metadata: itemMetadata{Schema: "nagi.intent-eval.v1", Split: spec.split, Conversation: spec.alias("conversation", spec.conversation), SourceMessage: row.MessageID},
		Input: datasetInput{Current: dialogue{ID: spec.messageAlias(row.PlatformMessageID), Sender: spec.alias("person", strconv.FormatInt(row.SenderID, 10)),
			Text: sanitize(row.Text), Types: segmentTypes(row.SegmentTypes), At: row.ReceivedAt}},
	}
	item.Metadata.Release, item.Metadata.Model = spec.release, row.Model
	item.Metadata.TraceStatus = "missing"
	if spec.origin != "" {
		item.Metadata.SourceURL = fmt.Sprintf("%s/decisions/inbound/%d/%d", spec.origin, spec.bot, row.MessageID)
	}
	if row.DecisionID == 0 {
		item.Metadata.Missing = []string{"decision_snapshot"}
		return item, nil
	}
	snapshot, err := repo.Snapshot(ctx, row.DecisionID)
	if errors.Is(err, domain.ErrSnapshotNotFound) {
		item.Metadata.Missing = []string{"decision_snapshot"}
		return item, nil
	}
	if err != nil {
		return item, fmt.Errorf("read snapshot for evaluation input")
	}
	item.snapshotRun = snapshot.RunID
	item.Metadata.Ruleset, item.Metadata.PromptHash, item.Metadata.PromptVersion = snapshot.RulesetVersion, snapshot.Prompt.Hash, snapshot.Prompt.Version
	item.Metadata.Missing = append(item.Metadata.Missing, snapshot.MissingFields...)
	input := snapshot.Input
	item.Metadata.ReplyMode = string(input.Policy.ReplyMode)
	current := input.Task.Message
	if len(current.Segments) == 0 {
		current.Segments = input.Task.Segments
	}
	if current.MessageID != 0 {
		item.Input.Current = spec.dialogue(current)
	} else {
		item.Metadata.Missing = append(item.Metadata.Missing, "current_message_snapshot")
	}
	item.Input.Recent = spec.history(input.Recent)
	item.Input.DecisionTime = input.Now
	item.Input.ActivityKnown = input.Activity.Known
	item.Input.BotLastReply = input.Activity.LastReplyAt
	item.Input.RepliesLastHour = input.Activity.RepliesLastHour
	if input.AwaitedRequest != nil {
		context := spec.dialogue(*input.AwaitedRequest)
		item.Input.Awaited = &context
		item.awaitedPlatformID = input.AwaitedRequest.MessageID
	}
	if input.Topics != nil {
		if input.Topics.Reference != nil {
			quoted := spec.dialogue(*input.Topics.Reference)
			item.Input.Quoted = &quoted
			item.quotedPlatformID = input.Topics.Reference.MessageID
			item.quotedBot = input.Topics.Reference.IsBot
		}
		for _, candidate := range input.Topics.Candidates {
			item.Metadata.ActiveTopic = item.Metadata.ActiveTopic || candidate.Active
			item.Input.Topics = append(item.Input.Topics, topic{ID: spec.alias("topic", strconv.FormatInt(candidate.ID, 10)),
				Active: candidate.Active, Waiting: candidate.Waiting, Recent: spec.history(candidate.History)})
		}
	}
	return item, nil
}

func missingMedia(types, availability []string) []string {
	expected := map[string]int{}
	for _, kind := range types {
		switch kind {
		case "image", "record", "video", "file", "mface", "bface":
			expected[kind]++
		}
	}
	for _, available := range availability {
		if kind, status, ok := strings.Cut(available, ":"); ok && status == "uploaded" {
			expected[kind]--
		}
	}
	var missing []string
	for kind, count := range expected {
		if count > 0 {
			missing = append(missing, "media_"+kind+"_unavailable")
		}
	}
	slices.Sort(missing)
	return missing
}

func archiveAvailability(ctx context.Context, pool *db.Pool, messageID int64) ([]string, error) {
	rows, err := pool.DB.QueryContext(ctx, `SELECT kind, CASE WHEN expires_at<=now() THEN 'expired' ELSE status END
	    FROM inbound_attachments WHERE message_id=$1 ORDER BY segment_index`, messageID)
	if err != nil {
		return nil, fmt.Errorf("read attachment availability")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, status string
		if err := rows.Scan(&kind, &status); err != nil {
			return nil, err
		}
		out = append(out, kind+":"+status)
	}
	return out, rows.Err()
}

func (s selection) alias(namespace, value string) string {
	mac := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(mac, "%s\x00%d\x00%s", namespace, s.bot, value)
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func (s selection) messageAlias(id int64) string {
	return s.alias("message", strconv.FormatInt(id, 10))
}

func (s selection) dialogue(message domain.Message) dialogue {
	types := make([]string, 0, len(message.Segments))
	var mentions, references []string
	for _, segment := range message.Segments {
		types = append(types, string(segment.Type))
		switch segment.Type {
		case ob.SegAt:
			target := segment.Data["qq"]
			switch target {
			case "all":
				mentions = append(mentions, "all")
			case strconv.FormatInt(s.bot, 10):
				mentions = append(mentions, "Nagi")
			default:
				if id, err := strconv.ParseInt(target, 10, 64); err == nil && id > 0 {
					mentions = append(mentions, s.alias("person", strconv.FormatInt(id, 10)))
				}
			}
		case ob.SegReply:
			if id, err := strconv.ParseInt(segment.Data["id"], 10, 64); err == nil && id != 0 {
				references = append(references, s.messageAlias(id))
			}
		}
	}
	text := ob.PlainText(message.Segments)
	if text == "" {
		text = message.Text
	}
	sender := s.alias("person", strconv.FormatInt(message.SenderID, 10))
	if message.IsBot {
		sender = "Nagi"
	}
	return dialogue{ID: s.messageAlias(message.MessageID), Sender: sender, Text: sanitize(text), Types: segmentTypes(types), Mentions: mentions, References: references, At: message.CreatedAt}
}

func (s selection) history(messages []domain.Message) []dialogue {
	out := make([]dialogue, 0, len(messages))
	for _, message := range messages {
		out = append(out, s.dialogue(message))
	}
	return out
}

func segmentTypes(types []string) []string {
	var out []string
	for _, kind := range types {
		switch kind {
		case "text", "":
		case "at", "reply", "image", "record", "video", "file", "face", "mface", "bface":
			out = append(out, kind)
		default:
			out = append(out, "other")
		}
	}
	return out
}

var privateDetails = regexp.MustCompile(`(?i)https?://\S+|base64://\S+|\[CQ:[^\]]*\]|\b[0-9]{5,}\b|[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func sanitize(text string) string {
	text = observability.RedactString(strings.TrimSpace(text))
	text = privateDetails.ReplaceAllString(text, "[已隐藏]")
	runes := []rune(text)
	if len(runes) > 800 {
		return string(runes[:800]) + "…"
	}
	return text
}
