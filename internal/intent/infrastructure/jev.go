// Jev (TypeSafe SystemOne) HTTP adapter for semantic intent judgment.
// 用于语义意图判定的 Jev（TypeSafe SystemOne）HTTP 适配器。
package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// JevConfig configures the Jev HTTP adapter.
// JevConfig 配置 Jev HTTP 适配器。
// ErrDecisionModelUnavailable identifies a server that has no usable native decision model.
// ErrDecisionModelUnavailable 表示服务端没有可用的原生决策模型。
var ErrDecisionModelUnavailable = errors.New("SystemOne服务端未找到指定决策模型，请下载并加载支持决策接口的模型")

type JevConfig struct {
	HTTPHeaders     map[string]string
	Transport       http.RoundTripper
	MaxOutputTokens int
	Thinking        bool
	ReasoningEffort string
	ContextWindow   int
	Endpoint        string // 形如 https://api.typesafe.ai/v1/systemone
	APIKey          string // Bearer 凭据；来自环境变量
	Model           string
	BotName         string
	Timeout         time.Duration
}

// JevClient calls the Jev SystemOne API; any malformed or out-of-range response
// is an error so the pipeline can degrade to rules-only.
// JevClient 调用 Jev SystemOne API；任何畸形或越界响应都视为错误，
// 让流水线降级为仅规则决策。
type JevClient struct {
	cfg    JevConfig
	client *http.Client
	logger *slog.Logger
}

// NewJevClient builds the client.
// NewJevClient 构建客户端。
func NewJevClient(cfg JevConfig, logger *slog.Logger) (*JevClient, error) {
	if cfg.Endpoint == "" || cfg.Model == "" || logger == nil {
		return nil, fmt.Errorf("intent: jev endpoint and model are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	return &JevClient{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, Transport: cfg.Transport}, logger: logger}, nil
}

// jevQuestion is one question entry of the SystemOne contract.
// jevQuestion 是 SystemOne 契约中的一个提问。
type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// questionSpecsFor resolves the effective question configuration for a call.
// The selected prompt snapshot wins; the built-in prompt is the local fallback
// and carries the documented roles and weights.
// questionSpecsFor 解析一次调用生效的问题配置。选定 Prompt 快照优先；
// 内置 Prompt 是本地兜底，并带有文档定义的角色与权重。
func questionSpecsFor(in domain.Input) map[string]domain.PromptQuestion {
	if in.Prompt.SystemOne != nil {
		return in.Prompt.SystemOne.Questions
	}
	return domain.BuiltinPrompt(domain.PromptProviderJev).SystemOne.Questions
}

// Decide builds the state payload, calls the API and parses answers defensively.
// Decide 构建状态负载、调用 API 并防御性地解析回答。
// recordSystemOneQuestions records the actual request order and question hash
// without exporting rendered instructions or the state containing chat content.
// recordSystemOneQuestions 记录实际请求顺序与问题哈希，不导出渲染后的问题文案或聊天状态。
func recordSystemOneQuestions(span trace.Span, wireQuestions map[string]jevQuestion) {
	keys := make([]string, 0, len(wireQuestions))
	for key := range wireQuestions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	wire, _ := json.Marshal(wireQuestions)
	span.SetAttributes(
		attribute.String("langfuse.observation.metadata.systemone_question_hash", domain.PromptTextHash(string(wire))),
		attribute.StringSlice("langfuse.observation.metadata.systemone_request_keys", keys),
	)
}

func (c *JevClient) Decide(ctx context.Context, in domain.Input) (domain.SemanticAnswer, error) {
	specs := questionSpecsFor(in)
	var topicKeys map[string]string
	if topics := domain.TopicQuestions(in.Topics); len(topics) != 0 {
		specs = maps.Clone(specs)
		topicKeys = make(map[string]string, len(topics))
		for key, question := range topics {
			requestKey := key
			for {
				if _, exists := specs[requestKey]; !exists {
					break
				}
				requestKey += "_"
			}
			specs[requestKey] = question
			topicKeys[key] = requestKey
		}
	}
	callQuestion, askCall := in.CallQuestion()
	wireQuestions := questionsFromSpecs(specs)
	callKey, _, _ := in.CallQuestionVersion()
	if askCall {
		for {
			if _, taken := wireQuestions[callKey]; !taken {
				break
			}
			callKey += "_"
		}
		wireQuestions[callKey] = jevQuestion{Type: callQuestion.Type, Instructions: callQuestion.Instructions, Criteria: callQuestion.Criteria}
	}
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		prompt := in.Prompt
		if prompt.SystemOne == nil {
			prompt = domain.BuiltinPrompt(domain.PromptProviderJev)
		}
		templateHash := prompt.FinalHash
		if templateHash == "" {
			templateHash = domain.PromptFinalHash(prompt)
		}
		layers := make([]map[string]any, 0, len(prompt.Layers))
		for _, layer := range prompt.Layers {
			layers = append(layers, map[string]any{"layer": layer.Layer, "version_id": layer.VersionID, "version": layer.Version, "source": layer.Source, "hash": layer.Hash})
		}
		layerJSON, _ := json.Marshal(layers)
		span.SetAttributes(
			attribute.String("langfuse.observation.metadata.prompt_rendered_hash", prompt.RenderedHash),
			attribute.String("langfuse.observation.metadata.prompt_layers", string(layerJSON)),
			attribute.String("langfuse.observation.type", "generation"),
			attribute.String("langfuse.observation.model.name", c.cfg.Model),
			attribute.String("langfuse.observation.metadata.prompt_source", prompt.Source),
			attribute.Int("langfuse.observation.metadata.prompt_version", prompt.Version),
			attribute.String("langfuse.observation.metadata.prompt_template_hash", templateHash),
		)
		recordSystemOneQuestions(span, wireQuestions)
	}
	state, shape := c.buildState(in)
	payload := map[string]any{
		"model":     c.cfg.Model,
		"state":     state,
		"questions": wireQuestions,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.SemanticAnswer{}, fmt.Errorf("jev: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.SemanticAnswer{}, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	req.GetBody = nil
	resp, err := intentMediaClient(c.client, c.cfg.HTTPHeaders).Do(req.WithContext(audit.WithModelMedia(ctx, "intent.media_request", shape)))
	if errors.Is(err, audit.ErrModelMediaStart) {
		return domain.SemanticAnswer{}, audit.ErrModelMediaStart
	}
	attempt := domain.SemanticAnswer{ExternalRequests: 1}
	if err != nil {
		return attempt, fmt.Errorf("jev: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure)
		if failure.Error.Code == "invalidModelIdentifier" {
			return attempt, ErrDecisionModelUnavailable
		}
		c.logger.Warn("jev request failed", "status", resp.StatusCode)
		return attempt, fmt.Errorf("jev: unexpected status %d", resp.StatusCode)
	}
	var parsed struct {
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   json.RawMessage            `json:"usage"`
	}
	if err := decodeSemanticResponse(resp.Body, &parsed, in.FixedBackground); err != nil {
		return attempt, fmt.Errorf("jev: decode response: %w", err)
	}
	attempt.Usage = parseSemanticUsage(parsed.Usage)
	if !askCall && len(specs) == 0 {
		return attempt, fmt.Errorf("jev: no questions configured")
	}
	answer, err := parseAnswers(parsed.Answers, specs)
	answer.ExternalRequests = attempt.ExternalRequests
	answer.Usage = attempt.Usage
	if askCall {
		answer.CallAddressing, answer.CallFailureCode = decodeCallAddressing(parsed.Answers[callKey])
	}
	hasCall := askCall && answer.CallAddressing != domain.CallUnknown
	if in.FixedBackground {
		if askCall && !hasCall {
			err = fmt.Errorf("jev: invalid explicit addressing answer")
		}
		for key, q := range specs {
			if q.Type == "choice" && len(q.Criteria) > 0 {
				if _, ok := q.Criteria[answer.Choices[key]]; !ok {
					err = fmt.Errorf("jev: invalid choice category")
					break
				}
			}
		}
		if err != nil {
			return attempt, err
		}
	}
	hasExit := false
	if len(topicKeys) != 0 {
		for i, candidate := range in.Topics.Candidates {
			scoreKey, actionKey := topicKeys[domain.TopicQuestionKey(i)], topicKeys[domain.TopicActionKey(i)]
			affinity, scored := answer.Scores[scoreKey]
			action := answer.Choices[actionKey]
			if candidate.Active && (scored || in.Topics.QuotedID == candidate.ID) && (action == domain.TopicClose || action == domain.TopicLeave) {
				hasExit = true
			}
			answer.TopicJudgments = append(answer.TopicJudgments, domain.TopicJudgment{Affinity: affinity, AffinityMissing: !scored, Action: action})
			delete(answer.Scores, scoreKey)
			delete(answer.Choices, actionKey)
		}
	}
	if err != nil && (hasCall || hasExit) {
		answer.Available = true
		answer.FailureCode = "decision_questions_degraded"
		return answer, nil
	}
	return answer, err
}

func decodeCallAddressing(raw json.RawMessage) (domain.CallAddressing, string) {
	if len(raw) == 0 {
		return domain.CallUnknown, "question_missing"
	}
	var value struct {
		Choice   domain.CallAddressing `json:"choice"`
		Label    domain.CallAddressing `json:"label"`
		Category domain.CallAddressing `json:"category"`
	}
	if json.Unmarshal(raw, &value) == nil {
		result := value.Choice
		if result == "" {
			result = value.Label
		}
		if result == "" {
			result = value.Category
		}
		switch result {
		case domain.CallYes, domain.CallNo, domain.CallUncertain:
			return result, ""
		}
	}
	return domain.CallUnknown, "invalid_answer"
}

// questionsFromSpecs maps the effective question configuration to the SystemOne
// wire payload. Role and Weight are local metadata and are not sent to the model.
// questionsFromSpecs 把生效的问题配置映射为 SystemOne 请求载荷；
// Role 与 Weight 是本地元数据，不发送给模型。
func questionsFromSpecs(specs map[string]domain.PromptQuestion) map[string]jevQuestion {
	out := make(map[string]jevQuestion, len(specs))
	for name, q := range specs {
		out[name] = jevQuestion{Type: q.Type, Instructions: q.Instructions, Criteria: q.Criteria}
	}
	return out
}

// buildState maps the decision input into the documented state shape.
// buildState 将决策输入映射为文档定义的状态形态。
func (c *JevClient) buildState(in domain.Input) (map[string]any, media.ModelMediaAttempt) {
	history, currentTime := in.HistoryBeforeCurrent()
	if currentTime.IsZero() {
		currentTime = in.Now
	}
	recent := make([]map[string]any, 0, len(history))
	for _, m := range history {
		sender := "bot"
		if !m.IsBot {
			sender = fmt.Sprintf("user_%d", m.SenderID)
		}
		entry := map[string]any{"sender": sender, "text": semanticText(m.Text)}
		if !m.CreatedAt.IsZero() && !currentTime.IsZero() {
			entry["seconds_ago"] = int64(currentTime.Sub(m.CreatedAt).Seconds())
		}
		recent = append(recent, entry)
	}
	sender := "bot"
	if !in.Task.Message.IsBot {
		sender = fmt.Sprintf("user_%d", in.Task.Message.SenderID)
	}
	botLastSpoke := in.BotLastSpoke()
	if botLastSpoke < 0 {
		botLastSpoke = -1
	}
	text, shape := renderIntentMessage(in.Task, true)
	state := map[string]any{
		"channel":                    in.Task.Kind,
		"current_message":            map[string]any{"sender": sender, "text": text},
		"recent_messages":            recent,
		"bot_last_spoke_seconds_ago": botLastSpoke,
		"mentions_bot":               in.MentionsBot(),
		"replies_to_bot":             in.RepliesToBot(),
	}
	if in.Policy.AllowHistoryRead && in.AwaitedRequest != nil {
		state["awaited_request"] = semanticText(in.AwaitedRequest.Text)
	}
	addCallIdentityState(state, in, c.cfg.BotName)
	if in.Policy.AllowHistoryRead {
		addTopicState(state, in.Topics)
	}
	if in.Task.Kind == "group" {
		addBackgroundState(state, in.Background)
	}
	return state, shape
}

func addCallIdentityState(state map[string]any, in domain.Input, configuredName string) {
	if !in.CallAddressingRequired() {
		state["bot_name"] = configuredName
	} else if in.CallIdentity == nil {
		state["bot_name"] = in.BotName
	} else {
		state["bot_identity"] = in.CallNames()
	}
}

func addTopicState(state map[string]any, topics *domain.TopicContext) {
	if topics == nil || len(topics.Candidates) == 0 {
		return
	}
	if topics.Reference != nil {
		text := []rune(semanticText(topics.Reference.Text))
		if len(text) > 500 {
			text = text[:500]
		}
		state["quoted_message"] = string(text)
	}
	candidates := make(map[string]any, len(topics.Candidates))
	for i, candidate := range topics.Candidates {
		key := domain.TopicQuestionKey(i)
		history := make([]domain.Message, len(candidate.History))
		for j, msg := range candidate.History {
			msg.Segments = nil
			msg.Text = semanticText(msg.Text)
			history[j] = msg
		}
		candidates[key] = map[string]any{"messages": history, "active": candidate.Active, "waiting": candidate.Waiting}
		if candidate.ID == topics.QuotedID {
			state["quoted_topic"] = key
		}
	}
	state["active_topics"] = candidates
}

func semanticText(text string) string {
	if !strings.Contains(text, "[CQ:") {
		return text
	}
	var out strings.Builder
	for _, segment := range ob.ParseCQ(text) {
		switch segment.Type {
		case ob.SegText:
			out.WriteString(segment.Data["text"])
		case ob.SegImage:
			out.WriteString("[图片]")
		default:
			fmt.Fprintf(&out, "[%s]", segment.Type)
		}
	}
	return out.String()
}

func intentMediaClient(client *http.Client, headers map[string]string) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	observed := *client
	observed.Transport = audit.ModelMediaTransport{Base: client.Transport}
	return modelhttp.Client(&observed, headers)
}

func renderIntentMessage(task domain.Task, jev bool) (string, media.ModelMediaAttempt) {
	shape := media.ModelMediaAttempt{Shape: media.MediaTextOnly}
	text := task.Message.Text
	if len(task.Segments) > 0 && ob.PlainText(task.Segments) == "" {
		for _, segment := range task.Segments {
			if segment.Type == ob.SegImage {
				// Only bridge fallback text is replaced. A current transcription remains text.
				// 仅替换桥接回退文本。当前语音转写仍保留为文字。
				if allImages(task.Segments) || strings.TrimSpace(text) == "" {
					shape.Shape = media.MediaHint
					return "[图片]", shape
				}
				for _, part := range ob.ParseCQ(text) {
					if part.Type == ob.SegImage {
						shape.Shape = media.MediaHint
						return semanticText(text), shape
					}
				}
				break
			}
		}
	}
	if jev {
		text = semanticText(text)
	}
	return text, shape
}

func allImages(segments []ob.Segment) bool {
	for _, segment := range segments {
		if segment.Type != ob.SegImage {
			return false
		}
	}
	return true
}

// parseAnswers extracts one answer per configured question; missing, malformed
// or out-of-range noul values make the whole answer unusable. Named-role scores
// also fill Roles, RolePresent and the legacy named fields; every noul question
// with a non-zero weight contributes to the aggregate participation score.
// parseAnswers 为每个已配置问题提取回答；noul 缺失、畸形或越界都会使整个回答不可用。
// 具名角色分数同时写入 Roles、RolePresent 与旧具名字段；每个非零权重的 noul 问题
// 都计入综合参与分。
func parseAnswers(answers map[string]json.RawMessage, questions map[string]domain.PromptQuestion) (domain.SemanticAnswer, error) {
	out := domain.SemanticAnswer{
		Scores:      make(map[string]float64, len(questions)),
		Choices:     make(map[string]string),
		Roles:       make(map[string]float64),
		RolePresent: make(map[string]bool),
	}
	if len(answers) == 0 && len(questions) != 0 {
		return out, fmt.Errorf("jev: empty answers")
	}
	if questions == nil {
		return out, fmt.Errorf("jev: no questions configured")
	}
	noul := func(name string) (float64, error) {
		raw, ok := answers[name]
		if !ok {
			return 0, fmt.Errorf("jev: answer %s missing", name)
		}
		var v struct {
			Noul *float64 `json:"noul"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Noul == nil {
			return 0, fmt.Errorf("jev: answer %s is not a noul value", name)
		}
		if math.IsNaN(*v.Noul) || *v.Noul < 0 || *v.Noul > 1 {
			return 0, fmt.Errorf("jev: answer %s out of range: %v", name, *v.Noul)
		}
		return *v.Noul, nil
	}
	choice := func(name string) (string, error) {
		raw, ok := answers[name]
		if !ok {
			return "", fmt.Errorf("jev: answer %s missing", name)
		}
		var v struct {
			Choice   string `json:"choice"`
			Label    string `json:"label"`
			Category string `json:"category"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", fmt.Errorf("jev: answer %s is not a choice value", name)
		}
		for _, value := range []string{v.Choice, v.Label, v.Category} {
			if value != "" {
				return value, nil
			}
		}
		// A choice answer with no resolvable category is a malformed response.
		// choice 回答解析不出任何分类即视为畸形响应。
		return "", fmt.Errorf("jev: answer %s has no category", name)
	}
	var numerator, denominator float64
	var firstError error
	terms := 0
	for name, q := range questions {
		role := domain.EffectiveQuestionRole(name, q)
		switch q.Type {
		case "noul":
			value, err := noul(name)
			if err != nil {
				if firstError == nil {
					firstError = err
				}
				continue
			}
			out.Scores[name] = value
			if role != domain.QuestionRoleCustom && domain.IsKnownQuestionRole(role) {
				out.Roles[role] = value
				out.RolePresent[role] = true
				assignNamedScore(&out, role, value)
			}
			if weight := domain.EffectiveQuestionWeight(name, q); weight != 0 {
				numerator += weight * value
				denominator += math.Abs(weight)
				terms++
			}
		case "choice":
			category, err := choice(name)
			if err != nil {
				if firstError == nil {
					firstError = err
				}
				continue
			}
			if role != domain.QuestionRoleCustom && domain.IsKnownQuestionRole(role) {
				out.RolePresent[role] = true
			}
			out.Choices[name] = category
			if role == domain.QuestionRoleIntent {
				out.IntentCategory = category
			}
		default:
			if firstError == nil {
				firstError = fmt.Errorf("jev: answer %s has unsupported type %q", name, q.Type)
			}
		}
	}
	if terms > 0 {
		out.ParticipationScore = numerator / denominator
		out.HasParticipation = true
	}
	out.Available = firstError == nil
	return out, firstError
}

// assignNamedScore copies a named-role noul score into its legacy field.
// assignNamedScore 把具名角色的 noul 分数写入对应的旧具名字段。
func assignNamedScore(out *domain.SemanticAnswer, role string, value float64) {
	switch role {
	case domain.QuestionRoleDirectedToBot:
		out.DirectedToBot = value
	case domain.QuestionRoleContinuesBotThread:
		out.ContinuesBotThread = value
	case domain.QuestionRoleAddressedToOther:
		out.AddressedToOther = value
	case domain.QuestionRoleAppropriateToJoin:
		out.AppropriateToJoin = value
	}
}
