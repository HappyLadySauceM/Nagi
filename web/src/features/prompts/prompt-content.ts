import type {
  ManagedPromptContent,
  ManagedPromptKind,
  ManagedPromptProvider,
  ManagedPromptQuestion,
  ManagedPromptScope,
  ManagedPromptSource,
  ManagedPromptStatus,
} from '../../lib/api/contracts'

export const PROMPT_KIND_LABELS: Record<ManagedPromptKind, string> = {
  chat_reply: '聊天回复',
  intent_participation: '意图判断',
  memory_extraction: '记忆抽取',
}

export const PROMPT_KIND_IDS: Record<ManagedPromptKind, string> = {
  chat_reply: 'chat.reply',
  intent_participation: 'intent.participation',
  memory_extraction: 'memory.extraction',
}

export const PROMPT_KIND_FROM_ID: Record<string, ManagedPromptKind> = {
  'chat.reply': 'chat_reply',
  'intent.participation': 'intent_participation',
  'memory.extraction': 'memory_extraction',
}

export const PROMPT_SCOPE_LABELS: Record<ManagedPromptScope, string> = {
  account: '账号默认',
  group: '群聊类型补充',
  private: '私聊类型补充',
}

export const PROMPT_SCOPE_HINTS: Record<ManagedPromptScope, string> = {
  account: '该账号所有会话的基础 Prompt；没有本地发布版本时使用内置版本。',
  group: '该账号所有群聊共用的追加文本，不按具体群号覆盖。',
  private: '该账号所有私聊共用的追加文本，不按具体用户覆盖。',
}

export const PROMPT_STATUS_LABELS: Record<ManagedPromptStatus, string> = {
  draft: '草稿',
  published: '已发布',
  archived: '已归档',
}

export const PROMPT_SOURCE_LABELS: Record<ManagedPromptSource | 'builtin', string> = {
  builtin: '内置',
  local: '本地',
  langfuse: 'Langfuse（仅来源标签）',
}

export const PROMPT_PROVIDER_LABELS: Record<ManagedPromptProvider, string> = {
  openai: 'OpenAI 协议',
  jev: 'Jev 协议',
}

export interface FixedQuestion {
  key: string
  label: string
  type: ManagedPromptQuestion['type']
  criteria?: Record<string, string>
}

/** Fixed Jev question set: keys and types belong to the server schema and are read-only on the page.
 * 固定的 Jev 题目集：key 与 Type 属于服务端 schema，页面只读。
 */
export const JEV_QUESTIONS: FixedQuestion[] = [
  { key: 'directed_to_bot', label: '指向机器人', type: 'noul' },
  { key: 'continues_bot_thread', label: '续聊机器人话题', type: 'noul' },
  { key: 'addressed_to_other', label: '对他人说话', type: 'noul' },
  { key: 'appropriate_to_join', label: '适合插话', type: 'noul' },
  {
    key: 'intent',
    label: '聊天功能',
    type: 'choice',
    criteria: {
      question: '求助或提问',
      chat: '普通闲聊',
      command: '要求执行动作',
      closing: '告别结束',
      other: '其它',
    },
  },
]

export function emptyPromptContent(kind: ManagedPromptKind, provider: ManagedPromptProvider): ManagedPromptContent {
  if (kind === 'intent_participation' && provider === 'jev') {
    return { provider: 'jev', systemOne: { questions: {} } }
  }
  return { provider: 'openai', openai: { systemPrompt: '' } }
}

/** Serialises the domain content to the PascalCase wire shape accepted by the management API.
 * 把领域内容序列化为管理 API 接受的 PascalCase wire 结构。
 */
export function promptContentToWire(content: ManagedPromptContent, scope: ManagedPromptScope = 'account'): Record<string, unknown> {
  const wire: Record<string, unknown> = { Provider: content.provider }
  if (content.openai) {
    const openai: Record<string, unknown> = {}
    if (content.openai.systemPrompt !== undefined) openai.SystemPrompt = content.openai.systemPrompt
    if (content.openai.userTemplate !== undefined) openai.UserTemplate = content.openai.userTemplate
    if (content.openai.responseShape !== undefined) openai.ResponseShape = content.openai.responseShape
    wire.OpenAI = openai
  }
  if (content.systemOne) {
    const questions: Record<string, unknown> = {}
    for (const [key, question] of Object.entries(content.systemOne.questions ?? {})) {
      const item: Record<string, unknown> = {
        Instructions: question.instructions,
      }
      if (scope === 'account') {
        item.Type = question.type
        if (question.criteria && Object.keys(question.criteria).length > 0) {
          item.Criteria = question.criteria
        }
      }
      questions[key] = item
    }
    wire.SystemOne = { Questions: questions }
  }
  return wire
}

const VARIABLE_PATTERN = /\{\{([a-zA-Z0-9_]+)\}\}/g

export function extractVariables(text: string): string[] {
  const matches: string[] = []
  for (const match of text.matchAll(VARIABLE_PATTERN)) {
    matches.push(match[1])
  }
  return matches
}

function occurrences(text: string, variable: string): number {
  return extractVariables(text).filter((item) => item === variable).length
}

function checkAllowedVariables(text: string, allowed: string[], label: string): string[] {
  const unknown = [...new Set(extractVariables(text).filter((item) => !allowed.includes(item)))]
  return unknown.map((item) => `${label}包含未识别变量 {{${item}}}`)
}

/** Validates editable content before submit; the server remains the final authority.
 * `extraVariables` are user-defined names allowed in System Prompt text.
 * 提交前校验可编辑内容；最终仍以服务端校验为准。
 * `extraVariables` 为 System Prompt 文本中允许的自定义变量名。
 */
export function validatePromptContent(
  kind: ManagedPromptKind,
  provider: ManagedPromptProvider,
  scope: ManagedPromptScope,
  content: ManagedPromptContent,
  extraVariables: string[] = [],
): string[] {
  const errors: string[] = []
  const account = scope === 'account'
  const systemPromptAllowed = ['bot_name', 'scope_type', 'target_id', 'sender_id', 'bot_id', ...extraVariables]
  if (kind === 'chat_reply' || (kind === 'intent_participation' && provider === 'openai') || kind === 'memory_extraction') {
    const systemPrompt = content.openai?.systemPrompt ?? ''
    errors.push(...checkAllowedVariables(systemPrompt, systemPromptAllowed, 'System Prompt '))
    if (kind === 'intent_participation' && provider === 'openai' && account) {
      const template = content.openai?.userTemplate ?? ''
      if (occurrences(template, 'state') !== 1) errors.push('UserTemplate 必须且只能包含一次 {{state}}')
      if (occurrences(template, 'question') !== 1) errors.push('UserTemplate 必须且只能包含一次 {{question}}')
      errors.push(...checkAllowedVariables(template, ['state', 'question'], 'UserTemplate '))
      if (content.openai?.responseShape !== 'single-letter') errors.push('ResponseShape 必须保持 single-letter')
    }
    if (kind === 'memory_extraction' && account) {
      const template = content.openai?.userTemplate ?? ''
      if (occurrences(template, 'messages_json') !== 1) {
        errors.push('UserTemplate 必须且只能包含一次 {{messages_json}}')
      }
      errors.push(...checkAllowedVariables(template, ['messages_json'], 'UserTemplate '))
    }
  }
  if (kind === 'intent_participation' && provider === 'jev') {
    const questions = content.systemOne?.questions ?? {}
    for (const question of JEV_QUESTIONS) {
      const item = questions[question.key]
      if (!item) {
        errors.push(`缺少固定题目 ${question.label}`)
        continue
      }
      if (item.type !== question.type) {
        errors.push(`${question.label} 的 Type 由服务端固定，不能修改`)
      }
      errors.push(...checkAllowedVariables(item.instructions, systemPromptAllowed, `${question.label} Instructions `))
    }
  }
  if (!account) {
    const forbidden = ['state', 'question', 'messages_json']
    const allText = [
      content.openai?.systemPrompt ?? '',
      ...Object.values(content.systemOne?.questions ?? {}).map((item) => item.instructions),
    ].join('\n')
    for (const variable of forbidden) {
      if (occurrences(allText, variable) > 0) {
        errors.push(`补充层不能使用 {{${variable}}}`)
      }
    }
  }
  return errors
}

/** Builds the editable content for a layer, keeping only fields that layer may own.
 * 构造某一层的可编辑内容，只保留该层允许拥有的字段。
 */
export function normaliseContentForScope(
  kind: ManagedPromptKind,
  provider: ManagedPromptProvider,
  scope: ManagedPromptScope,
  source?: ManagedPromptContent,
): ManagedPromptContent {
  const content: ManagedPromptContent = {
    provider,
    ...(provider === 'openai' ? { openai: { ...(source?.openai ?? {}) } } : {}),
    ...(kind === 'intent_participation' && provider === 'jev'
      ? { systemOne: { questions: { ...(source?.systemOne?.questions ?? {}) } } }
      : {}),
  }
  if (kind === 'intent_participation' && provider === 'jev') {
    const sourceQuestions = source?.systemOne?.questions ?? {}
    const questions: Record<string, ManagedPromptQuestion> = {}
    for (const question of JEV_QUESTIONS) {
      const item = sourceQuestions[question.key]
      questions[question.key] = {
        type: question.type,
        instructions: item?.instructions ?? '',
        ...(question.criteria
          ? { criteria: { ...question.criteria, ...(item?.criteria ?? {}) } }
          : {}),
      }
    }
    for (const [key, item] of Object.entries(sourceQuestions)) {
      if (!(key in questions)) {
        questions[key] = item
      }
    }
    content.systemOne = { ...(content.systemOne ?? {}), questions }
  }
  if (scope !== 'account') {
    if (content.openai) {
      delete content.openai.userTemplate
      delete content.openai.responseShape
    }
    if (content.systemOne?.questions) {
      content.systemOne.questions = Object.fromEntries(
        Object.entries(content.systemOne.questions).map(([key, question]) => [
          key,
          { type: question.type, instructions: question.instructions },
        ]),
      )
    }
  }
  return content
}
