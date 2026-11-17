import type {
  ManagedPromptContent,
  ManagedPromptDetail,
  ManagedPromptDiff,
  ManagedPromptDiffLine,
  ManagedPromptDiffSide,
  ManagedPromptEffective,
  ManagedPromptEffectiveLayer,
  ManagedPromptKind,
  ManagedPromptLayer,
  ManagedPromptListItem,
  ManagedPromptPreview,
  ManagedPromptProvider,
  ManagedPromptPublishResult,
  ManagedPromptQuestion,
  ManagedPromptRole,
  ManagedPromptRestoreResult,
  ManagedPromptScope,
  ManagedPromptSource,
  ManagedPromptStatus,
  ManagedPromptVersion,
} from '../contracts'
import {
  asArray,
  asRecord,
  contractError,
  readNumber,
  readOptionalNumber,
  readOptionalString,
  readOptionalTime,
  readString,
} from './guard'

const PROMPTS_ENDPOINT = '/api/v1/prompts'

const PROMPT_KINDS: ManagedPromptKind[] = [
  'chat_reply',
  'intent_participation',
  'memory_extraction',
]
const PROMPT_SCOPES: ManagedPromptScope[] = ['account', 'group', 'private']
const PROMPT_PROVIDERS: ManagedPromptProvider[] = ['openai', 'jev']
const PROMPT_STATUSES: ManagedPromptStatus[] = ['draft', 'published', 'archived']
const PROMPT_SOURCES: ManagedPromptSource[] = ['builtin', 'local', 'langfuse']
const QUESTION_TYPES: ManagedPromptQuestion['type'][] = ['noul', 'choice']
const PROMPT_ROLES: ManagedPromptRole[] = [
  'directed_to_bot',
  'continues_bot_thread',
  'addressed_to_other',
  'appropriate_to_join',
  'intent',
  'custom',
]

function readEnum<T extends string>(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
  values: readonly T[],
): T {
  const value = readString(record, key, endpoint)
  if (!values.includes(value as T)) {
    throw contractError(endpoint, `${key} 取值未知：${value}`)
  }
  return value as T
}

function readNullableTime(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): string | null {
  const value = record[key]
  if (value == null) {
    return null
  }
  return readOptionalTime(record, key, endpoint) ?? null
}

function toPromptQuestion(raw: unknown): ManagedPromptQuestion {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'Question')
  const question: ManagedPromptQuestion = {
    type: readEnum(record, 'Type', PROMPTS_ENDPOINT, QUESTION_TYPES),
    instructions: readString(record, 'Instructions', PROMPTS_ENDPOINT, ''),
  }
  if (record.Criteria != null) {
    const criteria = asRecord(record.Criteria, PROMPTS_ENDPOINT, 'Criteria')
    question.criteria = Object.fromEntries(
      Object.entries(criteria).map(([key, value]) => {
        if (typeof value !== 'string') {
          throw contractError(PROMPTS_ENDPOINT, `Criteria.${key} 不是字符串`)
        }
        return [key, value]
      }),
    )
  }
  const role = readOptionalString(record, 'Role', PROMPTS_ENDPOINT)
  if (role !== undefined) {
    if (!PROMPT_ROLES.includes(role as ManagedPromptRole)) {
      throw contractError(PROMPTS_ENDPOINT, `Role 取值未知：${role}`)
    }
    question.role = role as ManagedPromptRole
  }
  const weight = readOptionalNumber(record, 'Weight', PROMPTS_ENDPOINT)
  if (weight !== undefined) {
    question.weight = weight
  }
  return question
}

/** Parses the provider-specific PromptContent union without inventing missing branches.
 * 解析按 provider 区分的 PromptContent 联合类型，不虚构缺失分支。
 */
export function toManagedPromptContent(raw: unknown): ManagedPromptContent {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'Content')
  const content: ManagedPromptContent = {
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
  }
  if (record.OpenAI != null) {
    const openai = asRecord(record.OpenAI, PROMPTS_ENDPOINT, 'OpenAI')
    content.openai = {}
    const systemPrompt = readOptionalString(openai, 'SystemPrompt', PROMPTS_ENDPOINT)
    if (systemPrompt !== undefined) {
      content.openai.systemPrompt = systemPrompt
    }
    const userTemplate = readOptionalString(openai, 'UserTemplate', PROMPTS_ENDPOINT)
    if (userTemplate !== undefined) {
      content.openai.userTemplate = userTemplate
    }
    const responseShape = readOptionalString(openai, 'ResponseShape', PROMPTS_ENDPOINT)
    if (responseShape !== undefined) {
      content.openai.responseShape = responseShape
    }
  }
  if (record.SystemOne != null) {
    const systemOne = asRecord(record.SystemOne, PROMPTS_ENDPOINT, 'SystemOne')
    content.systemOne = { questions: {} }
    if (systemOne.Questions != null) {
      const questions = asRecord(systemOne.Questions, PROMPTS_ENDPOINT, 'Questions')
      content.systemOne.questions = Object.fromEntries(
        Object.entries(questions).map(([key, value]) => [key, toPromptQuestion(value)]),
      )
    }
  }
  return content
}

function toPromptVersion(raw: unknown): ManagedPromptVersion {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'PromptVersion')
  return {
    id: readNumber(record, 'ID', PROMPTS_ENDPOINT, 0),
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    kind: readEnum(record, 'Kind', PROMPTS_ENDPOINT, PROMPT_KINDS),
    name: readString(record, 'Name', PROMPTS_ENDPOINT, ''),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    botId: readNumber(record, 'BotID', PROMPTS_ENDPOINT, 0),
    scopeType: readEnum(record, 'ScopeType', PROMPTS_ENDPOINT, PROMPT_SCOPES),
    scopeId: readNumber(record, 'ScopeID', PROMPTS_ENDPOINT, 0),
    version: readNumber(record, 'Version', PROMPTS_ENDPOINT, 0),
    status: readEnum(record, 'Status', PROMPTS_ENDPOINT, PROMPT_STATUSES),
    source: readEnum(record, 'Source', PROMPTS_ENDPOINT, PROMPT_SOURCES),
    sourceRef: readString(record, 'SourceRef', PROMPTS_ENDPOINT, ''),
    hash: readString(record, 'Hash', PROMPTS_ENDPOINT, ''),
    note: readString(record, 'Note', PROMPTS_ENDPOINT, ''),
    content: toManagedPromptContent(record.Content),
    createdAt: readOptionalString(record, 'CreatedAt', PROMPTS_ENDPOINT) ?? '',
    publishedAt: readNullableTime(record, 'PublishedAt', PROMPTS_ENDPOINT),
    archivedAt: readNullableTime(record, 'ArchivedAt', PROMPTS_ENDPOINT),
  }
}

function toPromptLayer(raw: unknown): ManagedPromptLayer {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'Layer')
  return {
    botId: readNumber(record, 'BotID', PROMPTS_ENDPOINT, 0),
    scopeType: readEnum(record, 'ScopeType', PROMPTS_ENDPOINT, PROMPT_SCOPES),
    scopeId: readNumber(record, 'ScopeID', PROMPTS_ENDPOINT, 0),
    activeVersion: readNumber(record, 'ActiveVersion', PROMPTS_ENDPOINT, 0),
    activeVersionId: readNumber(record, 'ActiveVersionID', PROMPTS_ENDPOINT, 0),
    activeSource:
      record.ActiveSource == null || record.ActiveSource === ''
        ? ''
        : readEnum(record, 'ActiveSource', PROMPTS_ENDPOINT, PROMPT_SOURCES),
    status: readString(record, 'Status', PROMPTS_ENDPOINT, ''),
    updatedAt: readOptionalString(record, 'UpdatedAt', PROMPTS_ENDPOINT) ?? '',
  }
}

function toPromptListItem(raw: unknown): ManagedPromptListItem {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'PromptListItem')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    kind: readEnum(record, 'Kind', PROMPTS_ENDPOINT, PROMPT_KINDS),
    name: readString(record, 'Name', PROMPTS_ENDPOINT, ''),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    builtinVersion: readNumber(record, 'BuiltinVersion', PROMPTS_ENDPOINT, 1),
    layers: asArray(record.Layers, PROMPTS_ENDPOINT, 'Layers').map(toPromptLayer),
  }
}

export function toManagedPromptList(raw: unknown): ManagedPromptListItem[] {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'list')
  return asArray(record.Items, PROMPTS_ENDPOINT, 'Items').map(toPromptListItem)
}

export function toManagedPromptDetail(raw: unknown): ManagedPromptDetail {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'detail')
  const builtin = asRecord(record.Builtin, PROMPTS_ENDPOINT, 'Builtin')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    kind: readEnum(record, 'Kind', PROMPTS_ENDPOINT, PROMPT_KINDS),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    builtin: {
      version: readNumber(builtin, 'Version', PROMPTS_ENDPOINT, 1),
      status: 'builtin',
      source: 'builtin',
      hash: readString(builtin, 'Hash', PROMPTS_ENDPOINT, ''),
      content: toManagedPromptContent(builtin.Content),
    },
    active: record.Active == null ? null : toPromptVersion(record.Active),
    versions:
      record.Versions == null
        ? []
        : asArray(record.Versions, PROMPTS_ENDPOINT, 'Versions').map(toPromptVersion),
  }
}

function toDiffSide(raw: unknown): ManagedPromptDiffSide {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'DiffSide')
  const side: ManagedPromptDiffSide = {
    source: readString(record, 'Source', PROMPTS_ENDPOINT, ''),
    version: readNumber(record, 'Version', PROMPTS_ENDPOINT, 0),
    status: readString(record, 'Status', PROMPTS_ENDPOINT, ''),
    hash: readString(record, 'Hash', PROMPTS_ENDPOINT, ''),
  }
  const id = readOptionalNumber(record, 'ID', PROMPTS_ENDPOINT)
  if (id !== undefined) {
    side.id = id
  }
  return side
}

function toDiffLine(raw: unknown): ManagedPromptDiffLine {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'DiffLine')
  const op = readString(record, 'Op', PROMPTS_ENDPOINT)
  if (op !== '+' && op !== '-' && op !== ' ') {
    throw contractError(PROMPTS_ENDPOINT, `DiffLine.Op 取值未知：${op}`)
  }
  return { op, text: readString(record, 'Text', PROMPTS_ENDPOINT, '') }
}

export function toManagedPromptDiff(raw: unknown): ManagedPromptDiff {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'diff')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    from: toDiffSide(record.From),
    to: toDiffSide(record.To),
    changed: record.Changed === true,
    unifiedDiff: readString(record, 'UnifiedDiff', PROMPTS_ENDPOINT, ''),
    lines: asArray(record.Lines, PROMPTS_ENDPOINT, 'Lines').map(toDiffLine),
  }
}

function toEffectiveLayer(raw: unknown): ManagedPromptEffectiveLayer {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'EffectiveLayer')
  const layer: ManagedPromptEffectiveLayer = {
    layer: readString(record, 'Layer', PROMPTS_ENDPOINT),
  }
  const versionId = readOptionalNumber(record, 'VersionID', PROMPTS_ENDPOINT)
  if (versionId !== undefined) layer.versionId = versionId
  const version = readOptionalNumber(record, 'Version', PROMPTS_ENDPOINT)
  if (version !== undefined) layer.version = version
  const source = readOptionalString(record, 'Source', PROMPTS_ENDPOINT)
  if (source !== undefined) layer.source = source
  const hash = readOptionalString(record, 'Hash', PROMPTS_ENDPOINT)
  if (hash !== undefined) layer.hash = hash
  if (record.Content != null) layer.content = toManagedPromptContent(record.Content)
  if (typeof record.Editable === 'boolean') layer.editable = record.Editable
  const summary = readOptionalString(record, 'Summary', PROMPTS_ENDPOINT)
  if (summary !== undefined) layer.summary = summary
  return layer
}

function toFinalResult(record: Record<string, unknown>): {
  content: ManagedPromptContent
  variables: Record<string, string>
} {
  const final = asRecord(record.Final, PROMPTS_ENDPOINT, 'Final')
  const variables: Record<string, string> = {}
  if (final.Variables != null) {
    const raw = asRecord(final.Variables, PROMPTS_ENDPOINT, 'Variables')
    for (const [key, value] of Object.entries(raw)) {
      variables[key] = typeof value === 'string' ? value : String(value)
    }
  }
  return { content: toManagedPromptContent(final.Content), variables }
}

export function toManagedPromptEffective(raw: unknown): ManagedPromptEffective {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'effective')
  const target = asRecord(record.Target, PROMPTS_ENDPOINT, 'Target')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    kind: readEnum(record, 'Kind', PROMPTS_ENDPOINT, PROMPT_KINDS),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    botId: readNumber(record, 'BotID', PROMPTS_ENDPOINT, 0),
    target: {
      type: readEnum(target, 'Type', PROMPTS_ENDPOINT, ['group', 'private'] as const),
      id: readNumber(target, 'ID', PROMPTS_ENDPOINT, 0),
      ...(readOptionalString(target, 'ConversationID', PROMPTS_ENDPOINT) !== undefined
        ? { conversationId: readOptionalString(target, 'ConversationID', PROMPTS_ENDPOINT) }
        : {}),
    },
    layers: asArray(record.Layers, PROMPTS_ENDPOINT, 'Layers').map(toEffectiveLayer),
    final: toFinalResult(record),
    hash: readString(record, 'Hash', PROMPTS_ENDPOINT, ''),
  }
}

export function toManagedPromptPreview(raw: unknown): ManagedPromptPreview {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'preview')
  const mode = readString(record, 'PreviewMode', PROMPTS_ENDPOINT)
  if (mode !== 'example' && mode !== 'real') {
    throw contractError(PROMPTS_ENDPOINT, `PreviewMode 取值未知：${mode}`)
  }
  const targetRaw = record.Target
  const result: ManagedPromptPreview = {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    kind: readEnum(record, 'Kind', PROMPTS_ENDPOINT, PROMPT_KINDS),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    previewMode: mode,
    versionId: readNumber(record, 'VersionID', PROMPTS_ENDPOINT, 0),
    layers: asArray(record.Layers, PROMPTS_ENDPOINT, 'Layers').map(toEffectiveLayer),
    final: toFinalResult(record),
    warnings: asArray(record.Warnings ?? [], PROMPTS_ENDPOINT, 'Warnings').map((item) => {
      if (typeof item !== 'string') throw contractError(PROMPTS_ENDPOINT, 'Warnings 项不是字符串')
      return item
    }),
    previewHash: readString(record, 'PreviewHash', PROMPTS_ENDPOINT, ''),
  }
  if (targetRaw != null) {
    const target = asRecord(targetRaw, PROMPTS_ENDPOINT, 'Target')
    const type = readString(target, 'Type', PROMPTS_ENDPOINT, '')
    const id = readNumber(target, 'ID', PROMPTS_ENDPOINT, 0)
    const conversationId = readOptionalString(target, 'ConversationID', PROMPTS_ENDPOINT)
    // Example previews have no real target, so the backend sends an empty target.
    // 示例预览没有真实目标，后端会返回空 target。
    if (type !== '' || id !== 0 || conversationId !== undefined) {
      result.target = {
        type: readEnum(target, 'Type', PROMPTS_ENDPOINT, ['group', 'private'] as const),
        id,
        ...(conversationId !== undefined ? { conversationId } : {}),
      }
    }
  }
  return result
}

export function toManagedPromptPublishResult(raw: unknown): ManagedPromptPublishResult {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'publish')
  return {
    version: toPromptVersion(record.Version),
    archivedVersionId: readNumber(record, 'ArchivedVersionID', PROMPTS_ENDPOINT, 0),
  }
}

export function toManagedPromptRestoreResult(raw: unknown): ManagedPromptRestoreResult {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'restore')
  const resolved = asRecord(record.Resolved, PROMPTS_ENDPOINT, 'Resolved')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    scopeType: readEnum(record, 'ScopeType', PROMPTS_ENDPOINT, PROMPT_SCOPES),
    scopeId: readNumber(record, 'ScopeID', PROMPTS_ENDPOINT, 0),
    provider: readEnum(record, 'Provider', PROMPTS_ENDPOINT, PROMPT_PROVIDERS),
    active: null,
    resolved: {
      source: readString(resolved, 'Source', PROMPTS_ENDPOINT),
      version: readNumber(resolved, 'Version', PROMPTS_ENDPOINT, 1),
      hash: readString(resolved, 'Hash', PROMPTS_ENDPOINT, ''),
    },
    archivedVersionId: readNumber(record, 'ArchivedVersionID', PROMPTS_ENDPOINT, 0),
  }
}

export { toPromptVersion as toManagedPromptVersion }
