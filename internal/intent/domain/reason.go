// Human-readable explanations for stable intent reason codes.
// 稳定意图原因码的人类可读说明。
package domain

var reasonText = map[string]string{
	"explicit_mention":             "明确 @ 或引用机器人",
	"active_thread_continuation":   "延续机器人最近的话题",
	"interjection":                 "适合自然插话",
	"directed_to_bot":              "识别为对机器人发言",
	"addressed_to_other":           "正在与另一位真人对话，优先抑制",
	"closing":                      "告别结束，保持安静",
	"ambiguous":                    "语义结果不明确",
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
