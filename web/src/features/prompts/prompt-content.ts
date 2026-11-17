import type {
  ManagedPromptContent,
  ManagedPromptKind,
  ManagedPromptProvider,
  ManagedPromptQuestion,
  ManagedPromptRole,
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

/** One entry of the default Jev question template. The template is only a starting point:
 *  stored prompts may keep any set of questions, keys and roles.
 * 默认 Jev 问题模板的一项。模板只是起点：已存储的 Prompt 可保留任意问题、key 与 Role。
 */
export interface JevQuestionTemplate {
  key: string
  label: string
  role: ManagedPromptRole
  type: ManagedPromptQuestion['type']
  weight?: number
  criteria?: Record<string, string>
}

/** Standard Jev roles whose key is fixed to the role id. Custom questions use their own key.
 * 标准 Jev Role：key 固定为 Role 名。自定义问题使用用户自起的 key。
 */
export const STANDARD_PROMPT_ROLES: ManagedPromptRole[] = [
  'directed_to_bot',
  'continues_bot_thread',
  'addressed_to_other',
  'appropriate_to_join',
  'intent',
]

export const PROMPT_ROLE_LABELS: Record<ManagedPromptRole, string> = {
  directed_to_bot: '指向机器人',
  continues_bot_thread: '续聊机器人话题',
  addressed_to_other: '对他人说话',
  appropriate_to_join: '适合插话',
  intent: '聊天功能',
  custom: '自定义',
}

const KNOWN_PROMPT_ROLES = new Set<string>([...STANDARD_PROMPT_ROLES, 'custom'])

// Default weights mirror the server's backward-compatible mapping; intent (choice) carries none.
// 默认权重与服务端向后兼容映射一致；intent 为 choice，不带权重。
const DEFAULT_ROLE_WEIGHTS: Record<ManagedPromptRole, number | undefined> = {
  directed_to_bot: 1,
  continues_bot_thread: 1,
  appropriate_to_join: 1,
  addressed_to_other: -1,
  intent: undefined,
  custom: 1,
}

const DEFAULT_INTENT_CRITERIA: Record<string, string> = {
  question: '求助或提问',
  chat: '普通闲聊',
  command: '要求执行动作',
  closing: '告别结束',
  other: '其它',
}

/** Question key pattern accepted by the server contract.
 * 服务端契约接受的问题 key 格式。
 */
export const QUESTION_KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/

/** Infers the Role of a stored question from its key, matching the server's compatibility map.
 * 按存储的 key 推断问题 Role，与服务端兼容映射保持一致。
 */
export function inferRoleForKey(key: string): ManagedPromptRole {
  return KNOWN_PROMPT_ROLES.has(key) ? (key as ManagedPromptRole) : 'custom'
}

/** Returns the default weight for a role; intent and unknown weights return undefined.
 * 返回某 Role 的默认权重；intent 等无权重场景返回 undefined。
 */
export function defaultWeightForRole(role: ManagedPromptRole): number | undefined {
  return DEFAULT_ROLE_WEIGHTS[role]
}

/** Default Jev question template carrying Role and Weight.
 * 带 Role 与 Weight 的默认 Jev 问题模板。
 */
export const JEV_QUESTIONS: JevQuestionTemplate[] = [
  { key: 'directed_to_bot', label: '指向机器人', role: 'directed_to_bot', type: 'noul', weight: 1 },
  { key: 'continues_bot_thread', label: '续聊机器人话题', role: 'continues_bot_thread', type: 'noul', weight: 1 },
  { key: 'addressed_to_other', label: '对他人说话', role: 'addressed_to_other', type: 'noul', weight: -1 },
  { key: 'appropriate_to_join', label: '适合插话', role: 'appropriate_to_join', type: 'noul', weight: 1 },
  {
    key: 'intent',
    label: '聊天功能',
    role: 'intent',
    type: 'choice',
    criteria: DEFAULT_INTENT_CRITERIA,
  },
]

/** Builds a fresh question from a template entry with an empty instruction.
 * 用模板项构造一个 Instructions 为空的新问题。
 */
function questionFromTemplate(template: JevQuestionTemplate): ManagedPromptQuestion {
  return {
    type: template.type,
    instructions: '',
    ...(template.criteria ? { criteria: { ...template.criteria } } : {}),
    role: template.role,
    ...(template.weight !== undefined ? { weight: template.weight } : {}),
  }
}

/** Normalises one stored question: fills a missing Role by key and a missing Weight by Role.
 * 规范化单个存储问题：按 key 补 Role、按 Role 补 Weight。
 */
function normaliseStoredQuestion(key: string, question: ManagedPromptQuestion): ManagedPromptQuestion {
  const role = question.role ?? inferRoleForKey(key)
  const template = JEV_QUESTIONS.find((item) => item.key === key)
  const weight = question.weight ?? defaultWeightForRole(role)
  const criteria = template?.criteria
    ? { ...template.criteria, ...(question.criteria ?? {}) }
    : question.criteria
  return {
    type: question.type,
    instructions: question.instructions ?? '',
    ...(criteria ? { criteria } : {}),
    role,
    ...(weight !== undefined ? { weight } : {}),
  }
}

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
        const role = question.role ?? inferRoleForKey(key)
        item.Type = question.type
        item.Role = role
        if (question.criteria && Object.keys(question.criteria).length > 0) {
          item.Criteria = question.criteria
        }
        if (question.type === 'noul') {
          const weight = question.weight ?? defaultWeightForRole(role)
          if (weight !== undefined) item.Weight = weight
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

/** Human-readable label for one question; custom questions fall back to their key.
 * 某个问题的展示名；自定义问题回退为 key。
 */
function questionLabel(key: string, question: ManagedPromptQuestion): string {
  const role = question.role ?? inferRoleForKey(key)
  return role === 'custom' ? key : PROMPT_ROLE_LABELS[role]
}

/** Validates the full account-level Jev question map against the Phase 2 contract.
 * 按 Phase 2 契约校验账号层完整的 Jev 问题集。
 */
function validateJevQuestions(
  questions: Record<string, ManagedPromptQuestion>,
  allowedVariables: string[],
): string[] {
  const errors: string[] = []
  const entries = Object.entries(questions)
  if (entries.length < 1) {
    errors.push('至少需要 1 个问题')
  }
  if (entries.length > 20) {
    errors.push('问题数量不能超过 20 个')
  }
  const roleCounts = new Map<ManagedPromptRole, number>()
  for (const [key, question] of entries) {
    const label = questionLabel(key, question)
    if (!QUESTION_KEY_PATTERN.test(key)) {
      errors.push(`问题 key ${key} 不合法`)
    }
    if ((question.instructions ?? '').trim() === '') {
      errors.push(`题目 ${label} 的 Instructions 不能为空`)
    }
    errors.push(...checkAllowedVariables(question.instructions ?? '', allowedVariables, `题目 ${label} Instructions `))
    const role = question.role ?? inferRoleForKey(key)
    roleCounts.set(role, (roleCounts.get(role) ?? 0) + 1)
    if (role === 'custom') {
      if (question.type !== 'noul') {
        errors.push(`自定义问题 ${key} 的 Type 必须为 noul`)
      }
    } else if (role === 'intent') {
      if (question.type !== 'choice') {
        errors.push('intent 问题的 Type 必须为 choice')
      }
      if (!question.criteria || !Object.prototype.hasOwnProperty.call(question.criteria, 'closing')) {
        errors.push('intent 问题必须包含 closing 分类')
      }
    } else if (question.type !== 'noul') {
      errors.push(`${label} 的 Type 必须为 noul`)
    }
    if (question.type === 'noul' && question.weight !== undefined) {
      if (question.weight < -10 || question.weight > 10) {
        errors.push(`题目 ${label} 的 Weight 必须在 -10 到 10 之间`)
      }
    }
  }
  for (const [role, count] of roleCounts) {
    if (role !== 'custom' && count > 1) {
      errors.push(`Role ${PROMPT_ROLE_LABELS[role]} 最多只能有一个问题`)
    }
  }
  return errors
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
    if (account) {
      errors.push(...validateJevQuestions(questions, systemPromptAllowed))
    } else {
      for (const [key, item] of Object.entries(questions)) {
        errors.push(
          ...checkAllowedVariables(
            item.instructions ?? '',
            systemPromptAllowed,
            `题目 ${questionLabel(key, item)} Instructions `,
          ),
        )
      }
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
    const entries = Object.entries(sourceQuestions)
    const questions: Record<string, ManagedPromptQuestion> = {}
    if (entries.length === 0) {
      // Seed the default template so a brand-new draft starts with editable cards.
      // 新草稿为空时用默认模板起步，便于直接编辑。
      for (const template of JEV_QUESTIONS) {
        questions[template.key] = questionFromTemplate(template)
      }
    } else {
      for (const [key, item] of entries) {
        questions[key] = normaliseStoredQuestion(key, item)
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
          { type: question.type, instructions: question.instructions, role: question.role },
        ]),
      )
    }
  }
  return content
}
