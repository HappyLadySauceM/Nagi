// Package domain defines the intent decision model: rules, semantic answers and final decisions.
// intent/domain 定义意图决策模型：规则、语义回答与最终决策。
package domain

import (
	"time"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// Decision is the final reply decision.
// Decision 是最终回复决策。
type Decision string

// Supported decisions.
// 支持的决策。
const (
	DecisionReply  Decision = "reply"
	DecisionSilent Decision = "silent"
	DecisionDefer  Decision = "defer"
)

// Message is one normalized history or current message.
// Message 是一条归一化的历史或当前消息。
type Message struct {
	MessageID int64
	SenderID  int64
	IsBot     bool
	Text      string
	CreatedAt time.Time
}

// Task is one decision task derived from an inbound message job.
// Task 是从入站消息任务派生的决策任务。
type Task struct {
	JobID          int64
	BotID          int64
	Kind           string
	TargetID       int64
	ConversationID string
	Message        Message
	Segments       []obdomain.Segment
}

// BotActivity holds storage-derived bot reply metadata used by the rate rules.
// It carries counts and timestamps only, never message bodies, so it is safe to
// read regardless of chat-history authorization.
// BotActivity 持有存储层统计的机器人发言元数据，供频率规则使用。
// 只包含计数与时间戳，不含消息正文，因此与聊天历史授权无关。
type BotActivity struct {
	// Known reports whether the values come from the activity store; when false
	// the rules fall back to deriving them from authorized history.
	// Known 表示数值来自活动存储；为 false 时规则退回从授权历史推导。
	Known           bool
	LastReplyAt     time.Time // zero when the bot never spoke / 从未发言时为零值
	RepliesLastHour int
}

// Input is the full decision input assembled by the pipeline.
// Input 是流水线组装的完整决策输入。
type Input struct {
	Task Task
	// Recent is chronological authorized history including bot replies.
	// Recent 是按时间正序的授权历史，包含机器人回复。
	Recent []Message
	// Activity holds DB-derived bot reply metadata; it takes precedence over
	// Recent so cooldown and hourly limits cannot be bypassed by disabling
	// history reads.
	// Activity 持有数据库统计的机器人发言元数据；优先于 Recent，
	// 保证冷却与小时限流不会因关闭历史读取而被绕过。
	Activity BotActivity
	Policy   scopedomain.Policy
	Now      time.Time
}

// MentionsBot reports whether an at segment targets the bot itself.
// MentionsBot 报告是否有 at 段指向机器人自身。
func (in Input) MentionsBot() bool {
	bot := itoa(in.Task.BotID)
	for _, s := range in.Task.Segments {
		if s.Type == obdomain.SegAt && s.Data["qq"] == bot {
			return true
		}
	}
	return false
}

// RepliesToBot reports whether the reply segment references one of the bot's messages.
// RepliesToBot 报告引用段是否指向机器人发送的消息。
func (in Input) RepliesToBot() bool {
	var replyID string
	for _, s := range in.Task.Segments {
		if s.Type == obdomain.SegReply {
			replyID = s.Data["id"]
		}
	}
	if replyID == "" {
		return false
	}
	for _, m := range in.Recent {
		if m.IsBot && itoa(m.MessageID) == replyID {
			return true
		}
	}
	return false
}

// BotLastSpoke returns seconds since the bot last sent a message in this
// conversation; -1 when it never spoke. Storage-derived activity wins over
// authorized history so the rate gate cannot be bypassed.
// BotLastSpoke 返回机器人在会话中最近发言距今的秒数；从未发言返回 -1。
// 存储统计优先于授权历史，保证频率闸门不可绕过。
func (in Input) BotLastSpoke() int64 {
	if in.Activity.Known {
		if in.Activity.LastReplyAt.IsZero() {
			return -1
		}
		return int64(in.Now.Sub(in.Activity.LastReplyAt).Seconds())
	}
	for i := len(in.Recent) - 1; i >= 0; i-- {
		if in.Recent[i].IsBot {
			return int64(in.Now.Sub(in.Recent[i].CreatedAt).Seconds())
		}
	}
	return -1
}

// BotRepliesLastHour counts bot outgoing messages in the last hour.
// BotRepliesLastHour 统计最近一小时内机器人的发言数。
func (in Input) BotRepliesLastHour() int {
	if in.Activity.Known {
		return in.Activity.RepliesLastHour
	}
	count := 0
	for _, m := range in.Recent {
		if m.IsBot && in.Now.Sub(m.CreatedAt) <= time.Hour {
			count++
		}
	}
	return count
}

// SemanticAnswer holds the Jev answer values; Available=false means unusable.
// SemanticAnswer 持有 Jev 回答值；Available=false 表示不可用。
type SemanticAnswer struct {
	Available          bool
	DirectedToBot      float64
	ContinuesBotThread float64
	AddressedToOther   float64
	AppropriateToJoin  float64
	IntentCategory     string
}

// Thresholds holds the calibrated decision thresholds.
// Thresholds 持有校准后的决策阈值。
type Thresholds struct {
	Continuation      float64
	Interjection      float64
	AddressedOtherMax float64
}

// RuleResult is the deterministic rules gate outcome.
// RuleResult 是确定性规则闸门的结果。
type RuleResult struct {
	Blocked      bool
	ReasonCode   string
	NeedSemantic bool
	// Explicit is true when the message directly @-mentions or replies to the bot.
	// Explicit 为 true 表示消息直接 @ 或引用了机器人。
	Explicit bool
	// AlwaysReply is true when the scope's reply mode demands a reply without
	// consulting the semantic decider.
	// AlwaysReply 为 true 表示该作用域的回复模式要求无条件回复，
	// 不需要语义判定。
	AlwaysReply bool
}

// Step phases recorded with every decision. The strings are stable because the
// administrative API and the console group steps by them.
// 决策步骤的阶段名。字符串保持稳定，管理 API 与控制台按它分组。
const (
	StepPolicy   = "policy"
	StepRules    = "rules"
	StepSemantic = "semantic"
	StepFinal    = "final"
)

// Step is one explainable stage of a decision.
// Step 是决策中一个可解释的阶段。
type Step struct {
	Index      int
	Phase      string
	ReasonCode string
	Detail     map[string]any
}

// FinalDecision is the pipeline outcome recorded for evaluation.
// FinalDecision 是流水线输出，记录用于评估。
type FinalDecision struct {
	Decision   Decision
	ReasonCode string
	Provider   string
	Model      string
	Scores     map[string]float64
	LatencyMS  int64
	// RulesetVersion identifies the rules that produced the decision; empty
	// means the built-in ruleset.
	// RulesetVersion 标识产生该决策的规则版本；空值表示内置规则。
	RulesetVersion string
	Steps          []Step
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
