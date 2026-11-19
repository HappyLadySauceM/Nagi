// Human-readable explanations for stable intent reason codes.
// 稳定意图原因码的人类可读说明。
package domain

var reasonText = map[string]string{
	"awaiting_content":             "等待同一位发送人补充内容",
	"awaited_content_received":     "已收到同一位发送人补充的内容",
	"direct_name_call":             "直接呼叫机器人名称",
	"unanswered_call_followup":     "同一人追问尚未回应的呼叫",
	"explicit_mention":             "明确 @ 或引用机器人",
	"active_thread_continuation":   "延续机器人最近的话题",
	"topic_ambiguous":              "无法可靠关联活跃话题",
	"topic_personal_exit":          "发言者退出，话题仍活跃",
	"topic_close":                  "话题已结束",
	"interjection":                 "适合自然插话",
	"directed_to_bot":              "识别为对机器人发言",
	"addressed_to_other":           "正在与另一位真人对话，优先抑制",
	"closing":                      "告别结束，保持安静",
	"ambiguous":                    "语义结果不明确",
	"no_decision_question":         "没有决策问题，群聊保持安静",
	"jev_unavailable":              "语义服务不可用，群聊保持安静",
	"private_semantic_unavailable": "私聊语义不可用，按一对一策略回复",
	"private_normal":               "私聊正常问候/提问/闲聊，按一对一策略回复",
	"always_reply":                 "回复模式要求始终回复",
	"policy_disabled":              "作用域未启用",
	"observe_only":                 "作用域处于仅观察模式",
	"cooldown":                     "未达到最小回复间隔",
	"rate_limited":                 "达到每小时回复上限",
	"not_addressed":                "仅明确指向模式且未被指向",
	"rules_sufficient":             "规则已给出结论，无需语义判定",
	"semantic_unavailable":         "语义服务不可用",
	"rule_blocked":                 "规则明确拦截",
	"force_reply":                  "版本化规则要求回复",
}

// ReasonText returns a concise Chinese explanation, falling back to the raw
// stable reason code when no translation is registered.
// ReasonText 返回简短中文说明；未登记时回退到稳定原因码本身。
func ReasonText(code string) string {
	if text, ok := reasonText[code]; ok {
		return text
	}
	return code
}
