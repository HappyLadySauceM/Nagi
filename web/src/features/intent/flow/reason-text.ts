export const REPLY_MODE_TEXT: Record<string, { label: string; hint: string }> = {
  mention_only: { label: '仅响应 @ 与引用', hint: '只有被 @ 或引用时才参与对话。' },
  intent_based: { label: '由意图判定是否插话', hint: '没有被点名时，由语义判定决定要不要插话。' },
  always_reply: { label: '每条消息都回复', hint: '对每条收到的消息都生成回复。' },
  observe_only: { label: '只记录不回复', hint: '只留下消息记录，不主动发言。' },
}

export const SCOPE_KIND_TEXT: Record<string, string> = {
  group: '群聊',
  private: '私聊',
}

export const BUILTIN_RULE_TEXT: Record<string, string> = {
  closing: '告别或结束时保持沉默',
  continuation: '延续机器人刚才的话题时可以回复',
  directed_to_bot: '识别为在跟机器人说话时可以回复',
  addressed_to_other: '主要在跟另一位真人说话时保持沉默',
  interjection: '没被点名时，插话足够自然就可以回复',
}

export const SCORE_TEXT: Record<string, string> = {
  directed_to_bot: '是否在跟机器人说话',
  continues_bot_thread: '是否在追问机器人刚才的话',
  addressed_to_other: '是否主要在跟另一位真人说话',
  appropriate_to_join: '此时插话是否自然、有帮助',
  intent_category: '聊天功能',
}

export const INTENT_CATEGORY_TEXT: Record<string, string> = {
  question: '求助或提问',
  chat: '闲聊或讨论',
  command: '要求执行动作',
  closing: '告别或结束',
  other: '其他',
}

export const RULE_ACTION_TEXT: Record<string, string> = {
  reply: '直接回复',
  silent: '保持沉默',
  require_semantic: '交给语义判定',
}

export const BRANCH_TEXT: Record<string, string> = {
  authorized: '已通过策略闸门',
  blocked: '被规则拦截',
  history_read: '已读取历史',
  history_denied: '未授权读取历史',
  degraded: '读取失败，已降级',
  explicit: '明确指向机器人',
  always_reply: '回复模式要求始终回复',
  semantic: '需要语义判定',
  rule_direct: '规则已直接给出结论',
  skipped: '未调用',
  available: '语义结果可用',
  reply: '回复',
  silent: '沉默',
  defer: '延后',
}

const REASON_TEXT: Record<string, string> = {
  explicit_mention: '明确 @ 或引用机器人',
  active_thread_continuation: '延续机器人最近的话题',
  interjection: '适合自然插话',
  directed_to_bot: '识别为对机器人发言',
  addressed_to_other: '正在与另一位真人对话，优先抑制',
  closing: '告别结束，保持安静',
  ambiguous: '语义结果不明确',
  jev_unavailable: '语义服务不可用，群聊保持安静',
  private_semantic_unavailable: '私聊语义不可用，按一对一策略回复',
  private_normal: '私聊正常问候/提问/闲聊，按一对一策略回复',
  always_reply: '回复模式要求始终回复',
  policy_disabled: '作用域未启用',
  observe_only: '作用域处于仅观察模式',
  cooldown: '未达到最小回复间隔',
  rate_limited: '达到每小时回复上限',
  not_addressed: '仅明确指向模式且未被指向',
  rules_sufficient: '规则已给出结论，无需语义判定',
  semantic_unavailable: '语义服务不可用',
  rule_blocked: '规则明确拦截',
  force_reply: '版本化规则要求回复',
}

/** Chinese fallback for old records whose backend reason text is absent. */
export function reasonText(code: string, fromBackend?: string): string {
  return fromBackend?.trim() || REASON_TEXT[code] || code || '未记录原因'
}
