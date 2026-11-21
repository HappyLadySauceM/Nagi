package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/application"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	scope "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

const (
	comparisonSchema = "nagi-synthetic-policy-v1"
	comparisonLimit  = 8 << 20
)

type comparisonSpec struct {
	Schema         string             `json:"schema"`
	DatasetVersion string             `json:"dataset_version"`
	Arms           []comparisonArm    `json:"arms"`
	Samples        []comparisonSample `json:"samples"`
}

type comparisonArm struct {
	Name           string         `json:"name"`
	PolicyRef      string         `json:"policy_ref"`
	ModelConfigRef string         `json:"model_config_ref"`
	PromptRef      string         `json:"prompt_ref"`
	Builtin        bool           `json:"builtin"`
	Ruleset        *frozenRuleset `json:"ruleset,omitempty"`
	TopicsEnabled  *bool          `json:"topics_enabled"`
}

type frozenRuleset struct {
	ID        int64                            `json:"id"`
	Version   int                              `json:"version"`
	Questions map[string]domain.PromptQuestion `json:"questions"`
}

type comparisonSample struct {
	ID                         string                   `json:"id"`
	Input                      *frozenContext           `json:"input"`
	Policies                   map[string]*frozenPolicy `json:"policies"`
	Semantic                   *domain.SemanticAnswer   `json:"semantic,omitempty"`
	Missing                    []string                 `json:"missing,omitempty"`
	Participation              string                   `json:"participation,omitempty"`
	AcceptableTopicIDs         []int64                  `json:"acceptable_topic_ids,omitempty"`
	AcceptableTargetMessageIDs []int64                  `json:"acceptable_target_message_ids,omitempty"`
}

type frozenPolicy struct {
	Enabled            *bool           `json:"enabled"`
	ReplyMode          scope.ReplyMode `json:"reply_mode"`
	AllowHistoryRead   *bool           `json:"allow_history_read"`
	MinReplyIntervalMS *int            `json:"min_reply_interval_ms"`
	MaxRepliesPerHour  *int            `json:"max_replies_per_hour"`
}

type frozenContext struct {
	Task            domain.Task          `json:"task"`
	BotName         *string              `json:"bot_name"`
	Now             time.Time            `json:"now"`
	Recent          *[]domain.Message    `json:"recent"`
	ContextCaptured bool                 `json:"context_captured"`
	Activity        *domain.BotActivity  `json:"activity"`
	Awaited         *domain.Message      `json:"awaited,omitempty"`
	Topics          *domain.TopicContext `json:"topics,omitempty"`
	Attachments     *[]frozenAttachment  `json:"attachments"`
}

type frozenAttachment struct {
	Type  string `json:"type"`
	State string `json:"state"`
}

type comparisonReport struct {
	Schema          string              `json:"schema"`
	EvaluationLevel string              `json:"evaluation_level"`
	DatasetVersion  string              `json:"dataset_version"`
	ManifestSHA256  string              `json:"manifest_sha256"`
	Build           comparisonBuild     `json:"build"`
	FlowVersion     string              `json:"flow_version"`
	Arms            []armEvidence       `json:"arms"`
	Items           []comparisonItem    `json:"items"`
	Summary         []comparisonSummary `json:"summary"`
}

type comparisonBuild struct {
	Revision         string `json:"revision,omitempty"`
	Modified         string `json:"modified,omitempty"`
	ExecutableSHA256 string `json:"executable_sha256"`
}

type armEvidence struct {
	Name           string             `json:"name"`
	PolicyRef      string             `json:"policy_ref"`
	ModelConfigRef string             `json:"model_config_ref"`
	PromptRef      string             `json:"prompt_ref"`
	Ruleset        string             `json:"ruleset"`
	Thresholds     map[string]float64 `json:"thresholds"`
	TopicsEnabled  bool               `json:"topics_enabled"`
}

type comparisonItem struct {
	ID            string       `json:"id"`
	Participation string       `json:"participation,omitempty"`
	Arms          []armOutcome `json:"arms"`
}

type armOutcome struct {
	Name             string          `json:"name"`
	Decision         domain.Decision `json:"decision,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	TopicID          int64           `json:"topic_id,omitempty"`
	TopicAction      string          `json:"topic_action,omitempty"`
	ParticipationHit *bool           `json:"participation_hit,omitempty"`
	TopicHit         *bool           `json:"topic_hit,omitempty"`
	Missing          []string        `json:"missing,omitempty"`
}

type comparisonRate struct {
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	Rate        *float64 `json:"rate"`
}

type comparisonSummary struct {
	Arm           string          `json:"arm"`
	Evaluated     comparisonRate  `json:"evaluated"`
	Paired        comparisonRate  `json:"paired"`
	Missing       map[string]int  `json:"missing"`
	Labeled       comparisonRate  `json:"participation_label_coverage"`
	Recall        comparisonRate  `json:"must_reply_recall"`
	Interruption  comparisonRate  `json:"false_interruption_rate"`
	OptionalReply comparisonRate  `json:"optional_reply_rate"`
	TopicHit      comparisonRate  `json:"topic_hit_rate"`
	TargetLabels  int             `json:"target_labels"`
	TargetHit     *comparisonRate `json:"target_hit_rate"`
	ModelCalls    int             `json:"model_calls"`
	Tokens        int             `json:"tokens"`
	LatencyP95MS  *float64        `json:"latency_p95_ms"`
}

func runComparison(args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("nagi-eval compare", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("compare: expected a synthetic manifest on stdin")
	}
	build, err := currentComparisonBuild()
	if err != nil {
		return err
	}
	report, err := compareFrozen(in, build)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func currentComparisonBuild() (comparisonBuild, error) {
	path, err := os.Executable()
	if err != nil {
		return comparisonBuild{}, fmt.Errorf("compare: cannot identify executable")
	}
	file, err := os.Open(path)
	if err != nil {
		return comparisonBuild{}, fmt.Errorf("compare: cannot read executable")
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return comparisonBuild{}, fmt.Errorf("compare: cannot hash executable")
	}
	build := comparisonBuild{ExecutableSHA256: fmt.Sprintf("%x", hash.Sum(nil))}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				build.Revision = setting.Value
			case "vcs.modified":
				build.Modified = setting.Value
			}
		}
	}
	return build, nil
}

func compareFrozen(in io.Reader, build comparisonBuild) (comparisonReport, error) {
	raw, err := io.ReadAll(io.LimitReader(in, comparisonLimit+1))
	if err != nil || len(raw) > comparisonLimit {
		return comparisonReport{}, fmt.Errorf("compare: unreadable manifest or size exceeds %d bytes", comparisonLimit)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var spec comparisonSpec
	if err := decoder.Decode(&spec); err != nil {
		return comparisonReport{}, fmt.Errorf("compare: invalid manifest JSON near byte %d", decoder.InputOffset())
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return comparisonReport{}, fmt.Errorf("compare: expected exactly one JSON manifest")
	}
	if spec.Schema != comparisonSchema || spec.DatasetVersion == "" || len(spec.Arms) != 2 || len(spec.Samples) == 0 {
		return comparisonReport{}, fmt.Errorf("compare: synthetic v1 schema, dataset version, two arms and samples required")
	}
	report := comparisonReport{Schema: comparisonSchema, EvaluationLevel: "policy_fixed_semantic",
		DatasetVersion: spec.DatasetVersion, ManifestSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Build: build, FlowVersion: domain.FlowVersion}
	if spec.Arms[0].Name == spec.Arms[1].Name {
		return comparisonReport{}, fmt.Errorf("compare: arm names must differ")
	}
	var sets [2]*domain.Ruleset
	for i, arm := range spec.Arms {
		set, evidence, err := arm.frozen()
		if err != nil {
			return comparisonReport{}, fmt.Errorf("compare: arm %d: %w", i+1, err)
		}
		sets[i] = set
		report.Arms = append(report.Arms, evidence)
		report.Summary = append(report.Summary, comparisonSummary{Arm: arm.Name, Missing: make(map[string]int)})
	}
	seen := make(map[string]bool, len(spec.Samples))
	for i, sample := range spec.Samples {
		if sample.ID == "" || seen[sample.ID] {
			return comparisonReport{}, fmt.Errorf("compare: sample %d requires a unique alias", i+1)
		}
		seen[sample.ID] = true
		switch sample.Participation {
		case "", "must_reply", "must_be_silent", "optional":
		default:
			return comparisonReport{}, fmt.Errorf("compare: sample %d has an invalid participation label", i+1)
		}
		item := comparisonItem{ID: sample.ID, Participation: sample.Participation}
		for j, arm := range spec.Arms {
			outcome := sample.evaluate(arm, sets[j])
			item.Arms = append(item.Arms, outcome)
		}
		paired := item.Arms[0].Decision != "" && item.Arms[1].Decision != ""
		for j, outcome := range item.Arms {
			report.Summary[j].add(sample, outcome, paired)
		}
		report.Items = append(report.Items, item)
	}
	for i := range report.Summary {
		s := &report.Summary[i]
		for _, rate := range []*comparisonRate{&s.Evaluated, &s.Paired, &s.Labeled, &s.Recall, &s.Interruption, &s.OptionalReply, &s.TopicHit} {
			if rate.Denominator > 0 {
				value := float64(rate.Numerator) / float64(rate.Denominator)
				rate.Rate = &value
			}
		}
	}
	return report, nil
}

func (arm comparisonArm) frozen() (*domain.Ruleset, armEvidence, error) {
	if arm.Name == "" || arm.PolicyRef == "" || arm.ModelConfigRef == "" || arm.PromptRef == "" || arm.TopicsEnabled == nil {
		return nil, armEvidence{}, fmt.Errorf("explicit name, policy, model config, prompt and topics switch required")
	}
	if arm.Builtin == (arm.Ruleset != nil) {
		return nil, armEvidence{}, fmt.Errorf("choose built-in rules or one frozen ruleset")
	}
	var set *domain.Ruleset
	var questions map[string]domain.PromptQuestion
	version := domain.BuiltinRulesVersion
	if arm.Builtin {
		questions = domain.DefaultDecisionQuestions()
	} else {
		r := arm.Ruleset
		if r.ID <= 0 || r.Version <= 0 || r.Questions == nil {
			return nil, armEvidence{}, fmt.Errorf("ruleset ID, version and question map required")
		}
		set = &domain.Ruleset{ID: r.ID, Version: r.Version, Questions: r.Questions}
		if err := set.Validate(); err != nil {
			return nil, armEvidence{}, fmt.Errorf("invalid frozen ruleset questions")
		}
		questions = domain.WithDecisionDefaults(r.Questions)
		version = fmt.Sprintf("scope:%d@%d", set.ID, set.Version)
	}
	thresholds := make(map[string]float64, len(questions))
	for key, question := range questions {
		thresholds[key] = question.Threshold
	}
	return set, armEvidence{Name: arm.Name, PolicyRef: arm.PolicyRef, ModelConfigRef: arm.ModelConfigRef,
		PromptRef: arm.PromptRef, Ruleset: version, Thresholds: thresholds, TopicsEnabled: *arm.TopicsEnabled}, nil
}

func (sample comparisonSample) evaluate(arm comparisonArm, set *domain.Ruleset) armOutcome {
	result := armOutcome{Name: arm.Name, Missing: slices.Clone(sample.Missing)}
	frozen := sample.Input
	policy := sample.Policies[arm.Name]
	if !policy.complete() {
		result.Missing = append(result.Missing, "policy")
	}
	if frozen == nil {
		result.Missing = append(result.Missing, "input")
	} else {
		result.Missing = append(result.Missing, frozen.missing()...)
	}
	if len(result.Missing) != 0 {
		slices.Sort(result.Missing)
		result.Missing = slices.Compact(result.Missing)
		return result
	}
	in := domain.Input{Task: frozen.Task, BotName: *frozen.BotName, Now: frozen.Now, Activity: *frozen.Activity,
		Policy: scope.Policy{ScopeType: scope.ScopeType(frozen.Task.Kind), ScopeID: frozen.Task.TargetID,
			Enabled: *policy.Enabled, ReplyMode: policy.ReplyMode, AllowHistoryRead: *policy.AllowHistoryRead,
			MinReplyIntervalMS: *policy.MinReplyIntervalMS, MaxRepliesPerHour: *policy.MaxRepliesPerHour}}
	if in.Policy.AllowHistoryRead {
		in.Recent = *frozen.Recent
		in.AwaitedRequest = frozen.Awaited
		if *arm.TopicsEnabled {
			in.Topics = frozen.Topics
		}
	}
	decision, missing := application.EvaluateFrozenPolicy(in, set, sample.Semantic)
	result.Decision, result.Reason, result.Missing = decision.Decision, decision.ReasonCode, missing
	if decision.Topic != nil {
		result.TopicID, result.TopicAction = decision.Topic.ID, decision.Topic.Action
	}
	if len(missing) == 0 {
		switch sample.Participation {
		case "must_reply", "must_be_silent":
			hit := (result.Decision == domain.DecisionReply) == (sample.Participation == "must_reply")
			result.ParticipationHit = &hit
		}
	}
	if len(missing) == 0 && len(sample.AcceptableTopicIDs) != 0 {
		hit := result.TopicID > 0 && slices.Contains(sample.AcceptableTopicIDs, result.TopicID)
		result.TopicHit = &hit
	}
	return result
}

func (p *frozenPolicy) complete() bool {
	if p == nil || p.Enabled == nil || p.AllowHistoryRead == nil || p.MinReplyIntervalMS == nil || p.MaxRepliesPerHour == nil ||
		*p.MinReplyIntervalMS < 0 || *p.MaxRepliesPerHour < 0 {
		return false
	}
	switch p.ReplyMode {
	case scope.ModeIntentBased, scope.ModeAlwaysReply, scope.ModeMentionOnly, scope.ModeObserveOnly:
		return true
	}
	return false
}

func (frozen frozenContext) missing() []string {
	var missing []string
	task := frozen.Task
	if (task.Kind != "group" && task.Kind != "private") || task.BotID <= 0 || task.TargetID <= 0 ||
		task.JobID <= 0 || task.Message.MessageID <= 0 || task.Message.CreatedAt.IsZero() || frozen.Now.Before(task.Message.CreatedAt) {
		missing = append(missing, "input")
	}
	if frozen.BotName == nil {
		missing = append(missing, "bot_name")
	}
	if frozen.Recent == nil || !frozen.ContextCaptured {
		missing = append(missing, "history")
	}
	atArrival := func(message *domain.Message) bool {
		return message == nil || !message.CreatedAt.IsZero() && !message.CreatedAt.After(task.Message.CreatedAt) &&
			message.MessageID > 0 && message.MessageID != task.Message.MessageID
	}
	validHistory := func(messages []domain.Message) bool {
		for i, message := range messages {
			if !atArrival(&message) || (i > 0 && message.CreatedAt.Before(messages[i-1].CreatedAt)) {
				return false
			}
		}
		return true
	}
	if frozen.Recent != nil && !validHistory(*frozen.Recent) {
		missing = append(missing, "arrival_history")
	}
	if !atArrival(frozen.Awaited) {
		missing = append(missing, "awaited_context")
	}
	if frozen.Topics != nil {
		reference := frozen.Topics.Reference
		quoted := frozen.Topics.QuotedBot || frozen.Topics.QuotedID != 0
		if frozen.Topics.IdleSeconds <= 0 || len(frozen.Topics.Candidates) > domain.TopicMaxCandidates || !atArrival(reference) ||
			(quoted && reference == nil) || (frozen.Topics.QuotedBot && !reference.IsBot) {
			missing = append(missing, "topic_context")
		} else {
			for _, candidate := range frozen.Topics.Candidates {
				if candidate.ID <= 0 || !validHistory(candidate.History) {
					missing = append(missing, "topic_context")
					break
				}
			}
		}
	}
	if frozen.Activity == nil || !frozen.Activity.Known || frozen.Activity.RepliesLastHour < 0 || frozen.Activity.LastReplyAt.After(task.Message.CreatedAt) {
		missing = append(missing, "activity")
	}
	if !frozen.referencesCaptured() {
		missing = append(missing, "reference")
	}
	if !frozen.attachmentsCaptured() {
		missing = append(missing, "attachment_state")
	}
	return missing
}

func (frozen frozenContext) referencesCaptured() bool {
	for _, segment := range frozen.Task.Segments {
		if segment.Type != "reply" {
			continue
		}
		id, err := strconv.ParseInt(segment.Data["id"], 10, 64)
		if err != nil || id <= 0 {
			return false
		}
		if frozen.Topics != nil && frozen.Topics.Reference != nil && frozen.Topics.Reference.MessageID == id {
			continue
		}
		if frozen.Recent == nil || !slices.ContainsFunc(*frozen.Recent, func(message domain.Message) bool { return message.MessageID == id }) {
			return false
		}
	}
	return true
}

func (frozen frozenContext) attachmentsCaptured() bool {
	if frozen.Attachments == nil {
		return false
	}
	counts := make(map[string]int)
	for _, segment := range frozen.Task.Segments {
		switch segment.Type {
		case "image", "record", "video", "file":
			counts[string(segment.Type)]++
		}
	}
	for _, attachment := range *frozen.Attachments {
		if counts[attachment.Type] == 0 || (attachment.State != "available" && attachment.State != "unavailable") {
			return false
		}
		counts[attachment.Type]--
	}
	for _, remaining := range counts {
		if remaining != 0 {
			return false
		}
	}
	return true
}

func (s *comparisonSummary) add(sample comparisonSample, outcome armOutcome, paired bool) {
	s.Evaluated.Denominator++
	s.Labeled.count(sample.Participation != "")
	if len(sample.AcceptableTargetMessageIDs) != 0 {
		s.TargetLabels++
	}
	for _, missing := range outcome.Missing {
		s.Missing[missing]++
	}
	if outcome.Decision != "" {
		s.Evaluated.Numerator++
	}
	s.Paired.count(paired)
	if !paired {
		return
	}
	reply := outcome.Decision == domain.DecisionReply
	switch sample.Participation {
	case "must_reply":
		s.Recall.count(reply)
	case "must_be_silent":
		s.Interruption.count(reply)
	case "optional":
		s.OptionalReply.count(reply)
	}
	if outcome.TopicHit != nil {
		s.TopicHit.count(*outcome.TopicHit)
	}
}

func (r *comparisonRate) count(hit bool) {
	r.Denominator++
	if hit {
		r.Numerator++
	}
}
