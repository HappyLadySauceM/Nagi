package infrastructure

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func addGroupState(state map[string]any, in domain.Input) {
	evidence := *in.GroupContext
	if !in.Policy.AllowHistoryRead {
		evidence.Messages = nil
		evidence.Truncated = false
		evidence.HistoryUnits = 0
	}
	state["group_context"] = evidence
	if !in.Policy.AllowHistoryRead || in.Topics == nil || len(in.Topics.Candidates) == 0 {
		return
	}
	topics := make(map[string]any, len(in.Topics.Candidates))
	for i, candidate := range in.Topics.Candidates {
		key := domain.TopicQuestionKey(i)
		topics[key] = map[string]bool{"active": candidate.Active, "waiting": candidate.Waiting}
		if candidate.ID == in.Topics.QuotedID {
			state["quoted_topic"] = key
		}
	}
	state["active_topics"] = topics
}

func groupParticipants(in domain.Input) map[string]string {
	people := make(map[string]string)
	if in.GroupContext == nil {
		return people
	}
	add := func(id int64) {
		if id <= 0 || id == in.Task.BotID || len(people) >= 10 {
			return
		}
		idText := strconv.FormatInt(id, 10)
		people["user_"+idText] = "QQ账号 " + idText
	}
	fromMessage := func(message domain.GroupEvidence) {
		for _, mention := range message.Mentions {
			id, _ := strconv.ParseInt(mention, 10, 64)
			add(id)
		}
	}
	fromMessage(in.GroupContext.Current)
	if in.Policy.AllowHistoryRead {
		for _, message := range slices.Backward(in.GroupContext.Messages) {
			fromMessage(message)
			add(message.SenderID)
		}
	}
	return people
}

func responderQuestion(people map[string]string) domain.PromptQuestion {
	criteria := map[string]string{"bot": "当前机器人", "other": "其他明确的对象（包括没有数字账号的文字称呼）", "group": "对全群发问或未指定回答者", "uncertain": "无法判断或不期待回答"}
	maps.Copy(criteria, people)
	return domain.PromptQuestion{Type: "choice", Criteria: criteria, Instructions: "发送者期待谁回答当前消息？结合当前消息和获准同群证据。区分被叫来回答的人与句中被讨论的人。只有对话确实指向机器人才选 bot。"}
}

func subjectQuestion(people map[string]string) domain.PromptQuestion {
	criteria := map[string]string{"bot": "当前机器人", "other": "其他可确定的对象（包括没有数字账号的文字称呼）", "none": "无特定谈论对象", "uncertain": "有指代但身份待定"}
	maps.Copy(criteria, people)
	return domain.PromptQuestion{Type: "choice", Criteria: criteria, Instructions: "当前消息所谈论的人或对象是谁？被叫来回答的人不必是谈论对象。无法确认指代时选 uncertain。"}
}

func addGroupQuestion(questions map[string]jevQuestion, key string, q domain.PromptQuestion) string {
	for {
		if _, exists := questions[key]; !exists {
			questions[key] = jevQuestion{Type: q.Type, Instructions: q.Instructions, Criteria: q.Criteria}
			return key
		}
		key += "_"
	}
}

func decodeGroupChoice(raw json.RawMessage, q domain.PromptQuestion) string {
	var value struct {
		Choice   string `json:"choice"`
		Label    string `json:"label"`
		Category string `json:"category"`
	}
	if json.Unmarshal(raw, &value) == nil {
		for _, result := range []string{value.Choice, value.Label, value.Category} {
			if _, ok := q.Criteria[result]; ok {
				return result
			}
		}
	}
	return "uncertain"
}

func groupQuestionLabels(q domain.PromptQuestion) (string, []string, []string) {
	keys := slices.Sorted(maps.Keys(q.Criteria))
	var body strings.Builder
	body.WriteString(q.Instructions)
	labels := make([]string, len(keys))
	for i, key := range keys {
		labels[i] = string(rune('A' + i))
		body.WriteString(" " + labels[i] + "=" + q.Criteria[key] + "。")
	}
	return body.String(), labels, keys
}

func strongestGroupChoice(values []float64, keys []string) string {
	best := -1
	for i, value := range values {
		if best < 0 || value > values[best] {
			best = i
		}
	}
	if best < 0 || best >= len(keys) {
		return "uncertain"
	}
	for i, value := range values {
		if i != best && value == values[best] {
			return "uncertain"
		}
	}
	return keys[best]
}

func settleGroupAddressing(in domain.Input, answer *domain.SemanticAnswer) {
	if in.MentionsBot() {
		answer.ExpectedResponder = "bot"
		return
	}
	switch answer.ExpectedResponder {
	case "bot", "uncertain", "":
	case "other", "group":
		answer.CallAddressing = domain.CallNo
	default:
		if strings.HasPrefix(answer.ExpectedResponder, "user_") {
			answer.CallAddressing = domain.CallNo
		}
	}
}
