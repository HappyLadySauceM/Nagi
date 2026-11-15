import type {
  ExtractionJob,
  ExtractionJobStatus,
  ExtractionSettings,
  KnowledgePreview,
  KnowledgePreviewMemory,
  KnowledgePreviewMessage,
  KnowledgePreviewPrompt,
  KnowledgePreviewTools,
  KnowledgePromptId,
  KnowledgePromptSummary,
  KnowledgeSummary,
  ManagedPromptEffectiveLayer,
  ManagedPromptKind,
  ManagedPromptProvider,
  MemoryVisibility,
  PageResult,
} from '../contracts'
import { isMemoryStatus, isMemoryVisibility } from './memory'
import { toManagedPromptContent } from './prompts'
import {
  asArray,
  asRecord,
  contractError,
  readBoolean,
  readNumber,
  readOptionalNumber,
  readOptionalString,
  readOptionalTime,
  readPageEnvelope,
  readString,
} from './guard'

const KNOWLEDGE_ENDPOINT = '/api/v1/knowledge'
const EXTRACTION_ENDPOINT = '/api/v1/memory-extraction'

const VISIBILITIES: readonly MemoryVisibility[] = [
  'bot_shared',
  'group',
  'private_user',
  'conversation',
]
const JOB_STATUSES: readonly ExtractionJobStatus[] = [
  'pending',
  'processing',
  'done',
  'failed',
  'cancelled',
]
const PROMPT_KINDS: readonly ManagedPromptKind[] = [
  'chat_reply',
  'intent_participation',
  'memory_extraction',
]
const PROMPT_PROVIDERS: readonly ManagedPromptProvider[] = ['openai', 'jev']
const KNOWLEDGE_PROMPT_IDS: readonly KnowledgePromptId[] = [
  'chat.reply',
  'intent.participation',
  'memory.extraction',
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

function readStringMap(
  raw: unknown,
  endpoint: string,
  label: string,
): Record<string, string> {
  const record = asRecord(raw, endpoint, label)
  return Object.fromEntries(
    Object.entries(record).map(([key, value]) => {
      if (typeof value !== 'string') {
        throw contractError(endpoint, `${label}.${key} 不是字符串`)
      }
      return [key, value]
    }),
  )
}

function readBooleanMap(
  raw: unknown,
  endpoint: string,
  label: string,
): Record<string, boolean> {
  const record = asRecord(raw, endpoint, label)
  return Object.fromEntries(
    Object.entries(record).map(([key, value]) => {
      if (typeof value !== 'boolean') {
        throw contractError(endpoint, `${label}.${key} 不是布尔值`)
      }
      return [key, value]
    }),
  )
}

/** Parses the count-only knowledge aggregate; it never receives memory text.
 * 解析只含计数的知识聚合；不会接收记忆正文。
 */
export function toKnowledgeSummary(raw: unknown): KnowledgeSummary {
  const root = asRecord(raw, KNOWLEDGE_ENDPOINT, 'summary')
  const memories = asRecord(root.Memories, KNOWLEDGE_ENDPOINT, 'Memories')
  const byVisibilityRaw = asRecord(memories.ByVisibility, KNOWLEDGE_ENDPOINT, 'ByVisibility')
  const byVisibility = {
    bot_shared: 0,
    group: 0,
    private_user: 0,
    conversation: 0,
  } satisfies Record<MemoryVisibility, number>
  for (const [key, value] of Object.entries(byVisibilityRaw)) {
    if (!VISIBILITIES.includes(key as MemoryVisibility)) {
      throw contractError(KNOWLEDGE_ENDPOINT, `ByVisibility 取值未知：${key}`)
    }
    if (typeof value !== 'number' || !Number.isFinite(value)) {
      throw contractError(KNOWLEDGE_ENDPOINT, `ByVisibility.${key} 不是有效数字`)
    }
    byVisibility[key as MemoryVisibility] = value
  }

  const extraction = asRecord(root.Extraction, KNOWLEDGE_ENDPOINT, 'Extraction')
  const promptsRaw = asRecord(root.Prompts, KNOWLEDGE_ENDPOINT, 'Prompts')
  const prompts = {} as Record<KnowledgePromptId, KnowledgePromptSummary>
  for (const promptId of KNOWLEDGE_PROMPT_IDS) {
    const item = asRecord(promptsRaw[promptId], KNOWLEDGE_ENDPOINT, `Prompts.${promptId}`)
    prompts[promptId] = {
      activeVersion: readNumber(item, 'ActiveVersion', KNOWLEDGE_ENDPOINT, 0),
      source: readString(item, 'Source', KNOWLEDGE_ENDPOINT, ''),
    }
  }

  return {
    memories: {
      total: readNumber(memories, 'Total', KNOWLEDGE_ENDPOINT),
      active: readNumber(memories, 'Active', KNOWLEDGE_ENDPOINT),
      candidate: readNumber(memories, 'Candidate', KNOWLEDGE_ENDPOINT),
      superseded: readNumber(memories, 'Superseded', KNOWLEDGE_ENDPOINT),
      byVisibility,
    },
    extraction: {
      enabled: readBoolean(extraction, 'Enabled', KNOWLEDGE_ENDPOINT),
      pendingJobs: readNumber(extraction, 'PendingJobs', KNOWLEDGE_ENDPOINT, 0),
      processingJobs: readNumber(extraction, 'ProcessingJobs', KNOWLEDGE_ENDPOINT, 0),
      failedJobs: readNumber(extraction, 'FailedJobs', KNOWLEDGE_ENDPOINT, 0),
      lastErrorCode: readString(extraction, 'LastErrorCode', KNOWLEDGE_ENDPOINT, ''),
    },
    prompts,
  }
}

function toPreviewLayer(raw: unknown): ManagedPromptEffectiveLayer {
  const endpoint = `${KNOWLEDGE_ENDPOINT}/preview`
  const record = asRecord(raw, endpoint, 'PromptLayer')
  const layer: ManagedPromptEffectiveLayer = {
    layer: readString(record, 'Layer', endpoint),
  }
  const versionId = readOptionalNumber(record, 'VersionID', endpoint)
  if (versionId !== undefined) layer.versionId = versionId
  const version = readOptionalNumber(record, 'Version', endpoint)
  if (version !== undefined) layer.version = version
  const source = readOptionalString(record, 'Source', endpoint)
  if (source !== undefined) layer.source = source
  const hash = readOptionalString(record, 'Hash', endpoint)
  if (hash !== undefined) layer.hash = hash
  if (record.Content != null) layer.content = toManagedPromptContent(record.Content)
  if (typeof record.Editable === 'boolean') layer.editable = record.Editable
  const summary = readOptionalString(record, 'Summary', endpoint)
  if (summary !== undefined) layer.summary = summary
  return layer
}

function toPreviewPrompt(raw: unknown): KnowledgePreviewPrompt {
  const endpoint = `${KNOWLEDGE_ENDPOINT}/preview`
  const record = asRecord(raw, endpoint, 'Prompt')
  return {
    promptId: readString(record, 'PromptID', endpoint),
    name: readString(record, 'Name', endpoint, ''),
    kind: readEnum(record, 'Kind', endpoint, PROMPT_KINDS),
    provider: readEnum(record, 'Provider', endpoint, PROMPT_PROVIDERS),
    version: readNumber(record, 'Version', endpoint, 0),
    source: readString(record, 'Source', endpoint, ''),
    hash: readString(record, 'Hash', endpoint, ''),
    finalHash: readString(record, 'FinalHash', endpoint, ''),
    layers: asArray(record.Layers, endpoint, 'Layers').map(toPreviewLayer),
    content: toManagedPromptContent(record.Content),
    variables: readStringMap(record.Variables, endpoint, 'Variables'),
  }
}

function toPreviewTools(raw: unknown): KnowledgePreviewTools {
  const endpoint = `${KNOWLEDGE_ENDPOINT}/preview`
  const record = asRecord(raw, endpoint, 'Tools')
  const policy = asRecord(record.Policy, endpoint, 'Tools.Policy')
  return {
    skipped: readBoolean(record, 'Skipped', endpoint, false),
    reason: readString(record, 'Reason', endpoint, ''),
    policy: {
      enabled: readBoolean(policy, 'Enabled', endpoint, false),
      replyMode: readString(policy, 'ReplyMode', endpoint, ''),
      storeMessages: readBoolean(policy, 'StoreMessages', endpoint, false),
      allowMemoryRead: readBoolean(policy, 'AllowMemoryRead', endpoint, false),
      allowHistoryRead: readBoolean(policy, 'AllowHistoryRead', endpoint, false),
    },
    effectivePermissions: asArray(record.EffectivePermissions, endpoint, 'EffectivePermissions').map(
      (permission, index) => {
        if (typeof permission !== 'string') {
          throw contractError(endpoint, `EffectivePermissions[${index}] 不是字符串`)
        }
        return permission
      },
    ),
    explicitOverrides: readBooleanMap(record.ExplicitOverrides, endpoint, 'ExplicitOverrides'),
  }
}

function toPreviewMemory(raw: unknown): KnowledgePreviewMemory {
  const endpoint = `${KNOWLEDGE_ENDPOINT}/preview`
  const record = asRecord(raw, endpoint, 'Memory')
  const visibility = readString(record, 'Visibility', endpoint)
  if (!isMemoryVisibility(visibility)) {
    throw contractError(endpoint, `Visibility 取值未知：${visibility}`)
  }
  const status = readString(record, 'Status', endpoint)
  if (!isMemoryStatus(status)) {
    throw contractError(endpoint, `Status 取值未知：${status}`)
  }
  const item: KnowledgePreviewMemory = {
    id: readString(record, 'ID', endpoint),
    visibility,
    ownerKey: readString(record, 'OwnerKey', endpoint, ''),
    content: readString(record, 'Content', endpoint),
    status,
  }
  const source = readOptionalString(record, 'Source', endpoint)
  if (source !== undefined) item.source = source
  const subjectType = readOptionalString(record, 'SubjectType', endpoint)
  if (subjectType !== undefined) item.subjectType = subjectType
  const subjectId = readOptionalString(record, 'SubjectID', endpoint)
  if (subjectId !== undefined) item.subjectId = subjectId
  const updatedAt = readOptionalTime(record, 'UpdatedAt', endpoint)
  if (updatedAt !== undefined) item.updatedAt = updatedAt
  return item
}

function toPreviewMessage(raw: unknown): KnowledgePreviewMessage {
  const endpoint = `${KNOWLEDGE_ENDPOINT}/preview`
  const record = asRecord(raw, endpoint, 'Message')
  const message: KnowledgePreviewMessage = {
    id: readNumber(record, 'ID', endpoint, 0),
    messageId: readNumber(record, 'MessageID', endpoint, 0),
    senderId: readNumber(record, 'SenderID', endpoint, 0),
    isBot: readBoolean(record, 'IsBot', endpoint, false),
    text: readString(record, 'Text', endpoint, ''),
  }
  const createdAt = readOptionalTime(record, 'CreatedAt', endpoint)
  if (createdAt !== undefined) message.createdAt = createdAt
  return message
}

/** Parses one knowledge preview; skipped layers stay explicit instead of looking empty.
 * 解析一次知识预览；被跳过的层保持显式状态，不伪装成空数据。
 */
export function toKnowledgePreview(raw: unknown): KnowledgePreview {
  const endpoint = `${KNOWLEDGE_ENDPOINT}/preview`
  const record = asRecord(raw, endpoint, 'preview')
  const previewMode = readString(record, 'PreviewMode', endpoint)
  if (previewMode !== 'example' && previewMode !== 'real') {
    throw contractError(endpoint, `PreviewMode 取值未知：${previewMode}`)
  }
  const target = asRecord(record.Target, endpoint, 'Target')
  const targetType = readString(target, 'Type', endpoint)
  if (targetType !== 'group' && targetType !== 'private') {
    throw contractError(endpoint, `Target.Type 取值未知：${targetType}`)
  }
  const defaults = asRecord(record.Defaults, endpoint, 'Defaults')
  const promptsRaw = asRecord(record.Prompts, endpoint, 'Prompts')
  const prompts = Object.fromEntries(
    Object.entries(promptsRaw).map(([key, value]) => [key, toPreviewPrompt(value)]),
  )
  const context = asRecord(record.Context, endpoint, 'Context')
  const memoryRecord = asRecord(context.Memory, endpoint, 'Context.Memory')
  const historyRecord = asRecord(context.History, endpoint, 'Context.History')

  return {
    previewMode,
    target: {
      type: targetType,
      id: readNumber(target, 'ID', endpoint, 0),
      conversationId: readString(target, 'ConversationID', endpoint, ''),
    },
    defaults: {
      chatSystem: readString(defaults, 'ChatSystem', endpoint, ''),
      extractionSystem: readString(defaults, 'ExtractionSystem', endpoint, ''),
    },
    prompts,
    tools: toPreviewTools(context.Tools),
    memory: {
      skipped: readBoolean(memoryRecord, 'Skipped', endpoint, false),
      reason: readString(memoryRecord, 'Reason', endpoint, ''),
      items: asArray(memoryRecord.Items, endpoint, 'Memory.Items').map(toPreviewMemory),
    },
    history: {
      skipped: readBoolean(historyRecord, 'Skipped', endpoint, false),
      reason: readString(historyRecord, 'Reason', endpoint, ''),
      messages: asArray(historyRecord.Messages, endpoint, 'History.Messages').map(toPreviewMessage),
    },
  }
}

/** Parses the global read-only extraction settings.
 * 解析全局只读的抽取设置。
 */
export function toExtractionSettings(raw: unknown): ExtractionSettings {
  const endpoint = `${EXTRACTION_ENDPOINT}/settings`
  const record = asRecord(raw, endpoint, 'settings')
  const scope = readString(record, 'Scope', endpoint)
  if (scope !== 'global') {
    throw contractError(endpoint, `Scope 取值未知：${scope}`)
  }
  return {
    scope,
    enabled: readBoolean(record, 'Enabled', endpoint),
    concurrency: readNumber(record, 'Concurrency', endpoint, 0),
    batchMaxMessages: readNumber(record, 'BatchMaxMessages', endpoint, 0),
    batchMaxRunes: readNumber(record, 'BatchMaxRunes', endpoint, 0),
    maxWaitMs: readNumber(record, 'MaxWaitMS', endpoint, 0),
    pollIntervalMs: readNumber(record, 'PollIntervalMS', endpoint, 0),
    leaseDurationMs: readNumber(record, 'LeaseDurationMS', endpoint, 0),
    maxAttempts: readNumber(record, 'MaxAttempts', endpoint, 0),
    retryDelaysMs: asArray(record.RetryDelaysMS, endpoint, 'RetryDelaysMS').map((value, index) => {
      if (typeof value !== 'number' || !Number.isFinite(value)) {
        throw contractError(endpoint, `RetryDelaysMS[${index}] 不是有效数字`)
      }
      return value
    }),
    source: readString(record, 'Source', endpoint, ''),
    editable: readBoolean(record, 'Editable', endpoint),
  }
}

function toExtractionJob(raw: unknown): ExtractionJob {
  const endpoint = `${EXTRACTION_ENDPOINT}/jobs`
  const record = asRecord(raw, endpoint, 'job')
  const job: ExtractionJob = {
    id: readString(record, 'ID', endpoint),
    botId: readNumber(record, 'BotID', endpoint, 0),
    conversationId: readString(record, 'ConversationID', endpoint, ''),
    scopeType: readString(record, 'ScopeType', endpoint, ''),
    scopeId: readNumber(record, 'ScopeID', endpoint, 0),
    messageId: readNumber(record, 'MessageID', endpoint, 0),
    status: readEnum(record, 'Status', endpoint, JOB_STATUSES),
    attempts: readNumber(record, 'Attempts', endpoint, 0),
    roundAttempts: readNumber(record, 'RoundAttempts', endpoint, 0),
    lastErrorCode: readString(record, 'LastErrorCode', endpoint, ''),
    manualRetries: readNumber(record, 'ManualRetries', endpoint, 0),
    sourceAvailable: readBoolean(record, 'SourceAvailable', endpoint, false),
  }
  const nextRunAt = readOptionalTime(record, 'NextRunAt', endpoint)
  if (nextRunAt !== undefined) job.nextRunAt = nextRunAt
  const createdAt = readOptionalTime(record, 'CreatedAt', endpoint)
  if (createdAt !== undefined) job.createdAt = createdAt
  const updatedAt = readOptionalTime(record, 'UpdatedAt', endpoint)
  if (updatedAt !== undefined) job.updatedAt = updatedAt
  return job
}

/** Parses the lowercase extraction job page; job fields stay PascalCase.
 * 解析小写分页信封的抽取任务；任务字段保持 PascalCase。
 */
export function toExtractionJobPage(
  raw: unknown,
  request: { page: number; limit: number },
): PageResult<ExtractionJob> {
  const page = readPageEnvelope(raw, `${EXTRACTION_ENDPOINT}/jobs`, 'items')
  return {
    items: page.items.map(toExtractionJob),
    total: page.total,
    page: page.page > 0 ? page.page : request.page,
    limit: page.limit > 0 ? page.limit : request.limit,
  }
}
