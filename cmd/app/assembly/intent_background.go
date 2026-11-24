package assembly

import (
	"context"
	"errors"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
)

const (
	backgroundVersion     = 1
	backgroundReadTimeout = 200 * time.Millisecond
)

type intentBackgroundRoles interface {
	IntentRoleBackground(context.Context, intentdomain.Input) (*intentdomain.PromptIntentContext, []intentdomain.BackgroundRoleLayer, bool)
	FrozenIntentRole(context.Context, intentdomain.Input, []intentdomain.BackgroundRoleLayer, string) (*intentdomain.PromptIntentContext, bool)
}

type intentFactReader interface {
	IntentFacts(context.Context, int64, int64, string, string, string, []string, []string) ([]memorydomain.Memory, error)
}

type intentDisclosure interface {
	IntentMemoryRouteCurrent(context.Context, settingsapp.IntentDestination) bool
	MayDiscloseIntentMemory(context.Context, int64, int64, settingsapp.IntentDestination, string) bool
}

type intentBackground struct {
	roles  intentBackgroundRoles
	facts  intentFactReader
	grants intentDisclosure
	scopes scopecontracts.Authorizer
}

func (b intentBackground) checkFixedRoute(ctx context.Context, dest settingsapp.IntentDestination) string {
	ctx, cancel := context.WithTimeout(ctx, backgroundReadTimeout)
	defer cancel()
	if b.grants == nil || !b.grants.IntentMemoryRouteCurrent(ctx, dest) || ctx.Err() != nil {
		return "background_destination_changed"
	}
	return ""
}

func (b intentBackground) canRead(ctx context.Context, task intentdomain.Task, destination settingsapp.IntentDestination, endpoint string) bool {
	if task.Kind != string(scopedomain.ScopeGroup) || task.ConversationID == "" || b.scopes == nil || b.grants == nil || b.facts == nil {
		return false
	}
	policy, err := b.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeGroup, task.TargetID)
	return err == nil && ctx.Err() == nil && policy.Enabled && policy.AllowMemoryRead && b.grants.MayDiscloseIntentMemory(ctx, task.BotID, task.TargetID, destination, endpoint)
}

func (b intentBackground) baseAllowed(ctx context.Context, in intentdomain.Input) string {
	if b.scopes == nil {
		return "background_scope_denied"
	}
	ctx, cancel := context.WithTimeout(ctx, backgroundReadTimeout)
	defer cancel()
	policy, err := b.scopes.Resolve(ctx, in.Task.BotID, scopedomain.ScopeType(in.Task.Kind), in.Task.TargetID)
	if err != nil || ctx.Err() != nil || !policy.Enabled || policy.ReplyMode == scopedomain.ModeObserveOnly {
		return "background_scope_denied"
	}
	if in.Policy.AllowHistoryRead && !policy.AllowHistoryRead && (len(in.Recent) > 0 || in.AwaitedRequest != nil || in.Topics != nil) {
		return "background_history_denied"
	}
	if policy.ReplyMode == scopedomain.ModeMentionOnly || policy.ReplyMode == scopedomain.ModeAlwaysReply {
		in.Policy = policy
		rule := intentapp.EvaluateRules(in)
		if rule.Blocked || !rule.NeedSemantic {
			return "background_scope_denied"
		}
	}
	return ""
}

func (b intentBackground) prepare(ctx context.Context, in intentdomain.Input, dest settingsapp.IntentDestination) (intentdomain.SemanticBackground, *intentdomain.BackgroundEvidence, string) {
	if in.FixedBackground {
		return b.restore(ctx, in, dest, in.FrozenBackground)
	}
	var projection intentdomain.SemanticBackground
	evidence := &intentdomain.BackgroundEvidence{Version: backgroundVersion, Destination: dest.Fingerprint, MemoryOutcome: "not_allowed"}
	if in.Task.Kind != "group" {
		evidence.RoleHash = intentapp.RoleBackgroundHash(nil)
		evidence.MemoryOutcome = "not_applicable"
		return projection, evidence, ""
	}
	if b.roles == nil {
		return projection, nil, ""
	}
	roleCtx, cancelRole := context.WithTimeout(ctx, backgroundReadTimeout)
	role, refs, known := b.roles.IntentRoleBackground(roleCtx, in)
	cancelRole()
	if known {
		projection.Role = role
		evidence.RoleHash = intentapp.RoleBackgroundHash(role)
		evidence.RoleLayers = refs
	}
	if in.Policy.AllowMemoryRead && in.Task.JobID > 0 {
		readCtx, cancel := context.WithTimeout(ctx, backgroundReadTimeout)
		defer cancel()
		if b.canRead(readCtx, in.Task, dest, dest.Endpoint) {
			evidence.MemoryOutcome = "no_match"
			current, topic := intentFactQueries(in)
			facts, err := b.facts.IntentFacts(readCtx, in.Task.BotID, in.Task.TargetID, in.Task.ConversationID, current, topic, intentFactSubjects(in.Task), nil)
			if err != nil || !b.canRead(readCtx, in.Task, dest, dest.Endpoint) {
				evidence.MemoryOutcome = "read_unavailable"
			} else {
				if len(facts) > 0 {
					evidence.MemoryOutcome = "matched"
				}
				for _, fact := range facts {
					projection.Memories = append(projection.Memories, fact.Content)
					evidence.Memories = append(evidence.Memories, factRef(fact))
				}
			}
		}
	}
	return projection, evidence, ""
}

func intentFactQueries(in intentdomain.Input) (string, string) {
	const maxRunes = 480
	bound := func(s string) string { r := []rune(s); return string(r[:min(len(r), maxRunes)]) }
	var parts []string
	if in.Policy.AllowHistoryRead && in.Topics != nil && in.Topics.QuotedID > 0 {
		for _, topic := range in.Topics.Candidates {
			if topic.ID != in.Topics.QuotedID {
				continue
			}
			if in.Topics.Reference != nil {
				parts = append(parts, bound(in.Topics.Reference.Text))
			}
			for _, message := range topic.History[max(0, len(topic.History)-2):] {
				parts = append(parts, bound(message.Text))
			}
			break
		}
	}
	return bound(in.Task.Message.Text), strings.Join(parts, " ")
}

func intentFactSubjects(task intentdomain.Task) []string {
	var subjects []string
	for _, segment := range task.Segments {
		if segment.Type != ob.SegAt {
			continue
		}
		id, err := strconv.ParseInt(segment.Data["qq"], 10, 64)
		if err == nil && id > 0 && id != task.BotID {
			key := strconv.FormatInt(id, 10)
			if !slices.Contains(subjects, key) {
				subjects = append(subjects, key)
			}
		}
	}
	if len(subjects) > 1 {
		return nil
	}
	if len(subjects) == 0 && task.Message.SenderID > 0 {
		text := strings.TrimSpace(task.Message.Text)
		for _, prefix := range []string{"我叫", "请叫我", "请称呼我", "我的名字是", "我的称呼是"} {
			if strings.HasPrefix(text, prefix) && len(text) > len(prefix) {
				return []string{strconv.FormatInt(task.Message.SenderID, 10)}
			}
		}
	}
	return subjects
}

func factRef(fact memorydomain.Memory) intentdomain.BackgroundMemoryRef {
	return intentdomain.BackgroundMemoryRef{ID: fact.ID, SourceMessageID: fact.SourceMessageID, Digest: intentdomain.PromptTextHash(fact.UpdatedAt.UTC().Format(time.RFC3339Nano) + "\n" + fact.Content)}
}

func (b intentBackground) restore(ctx context.Context, in intentdomain.Input, dest settingsapp.IntentDestination, evidence *intentdomain.BackgroundEvidence) (intentdomain.SemanticBackground, *intentdomain.BackgroundEvidence, string) {
	var projection intentdomain.SemanticBackground
	if evidence == nil || evidence.Version != backgroundVersion || evidence.RoleHash == "" || evidence.Destination == "" {
		return projection, nil, "background_evidence_missing"
	}
	if dest.Fingerprint != evidence.Destination {
		return projection, nil, "background_destination_changed"
	}
	if !validMemoryEvidence(in.Task.Kind, evidence) {
		return projection, nil, "background_evidence_missing"
	}
	if in.Task.Kind != "group" {
		return projection, evidence, ""
	}
	if b.roles == nil {
		return projection, nil, "background_role_unavailable"
	}
	roleCtx, cancelRole := context.WithTimeout(ctx, backgroundReadTimeout)
	role, ok := b.roles.FrozenIntentRole(roleCtx, in, evidence.RoleLayers, evidence.RoleHash)
	cancelRole()
	if !ok {
		return projection, nil, "background_role_unavailable"
	}
	projection.Role = role
	if len(evidence.Memories) > 0 {
		var reason string
		projection.Memories, reason = b.verifyFacts(ctx, in.Task, dest, dest.Endpoint, evidence.Memories)
		if reason != "" {
			return intentdomain.SemanticBackground{}, nil, reason
		}
	}
	return projection, evidence, ""
}

func validMemoryEvidence(kind string, evidence *intentdomain.BackgroundEvidence) bool {
	if kind != "group" {
		return evidence.MemoryOutcome == "not_applicable" && len(evidence.Memories) == 0 && len(evidence.RoleLayers) == 0 && evidence.RoleHash == intentapp.RoleBackgroundHash(nil)
	}
	switch evidence.MemoryOutcome {
	case "matched":
		return len(evidence.Memories) > 0 && len(evidence.Memories) <= 4
	case "no_match", "not_allowed", "read_unavailable", "unavailable_at_send":
		return len(evidence.Memories) == 0
	default:
		return false
	}
}

func (b intentBackground) verifyFacts(ctx context.Context, task intentdomain.Task, dest settingsapp.IntentDestination, endpoint string, refs []intentdomain.BackgroundMemoryRef) ([]string, string) {
	ctx, cancel := context.WithTimeout(ctx, backgroundReadTimeout)
	defer cancel()
	if !b.canRead(ctx, task, dest, endpoint) {
		return nil, "background_disclosure_denied"
	}
	if len(refs) > 4 {
		return nil, "background_memory_unavailable"
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	facts, err := b.facts.IntentFacts(ctx, task.BotID, task.TargetID, task.ConversationID, "", "", intentFactSubjects(task), ids)
	if err != nil || len(facts) != len(refs) {
		return nil, "background_memory_unavailable"
	}
	byID := make(map[string]memorydomain.Memory, len(facts))
	for _, fact := range facts {
		byID[fact.ID] = fact
	}
	ordered := make([]string, 0, len(refs))
	for _, ref := range refs {
		fact, ok := byID[ref.ID]
		if !ok || factRef(fact) != ref {
			return nil, "background_memory_unavailable"
		}
		ordered = append(ordered, fact.Content)
	}
	if !b.canRead(ctx, task, dest, endpoint) {
		return nil, "background_disclosure_denied"
	}
	return ordered, ""
}

type intentRequestGuardKey struct{}

type intentRequestGuard struct {
	settle  func(bool) intentcontracts.ReplayAttemptBudget
	check   func(context.Context, string) string
	partial atomic.Bool
	unknown atomic.Int32
	sent    atomic.Int32
	failure atomic.Pointer[string]
}

var errIntentBackgroundDenied = errors.New("intent: request background no longer authorized")
