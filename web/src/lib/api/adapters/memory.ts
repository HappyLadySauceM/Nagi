import type {
  MemoryCreateResult,
  MemoryEntry,
  MemoryManagementView,
  MemoryReplacement,
  MemoryStatus,
  MemorySubjectType,
  MemorySupersedeResult,
  MemoryVisibility,
  PageResult,
} from '../contracts'
import {
  asArray,
  asRecord,
  contractError,
  readBoolean,
  readNumber,
  readOptionalNumber,
  readOptionalString,
  readOptionalTime,
  readString,
} from './guard'

const MEMORY_ENDPOINT = '/api/v1/memories'

const VISIBILITIES: readonly MemoryVisibility[] = [
  'bot_shared',
  'group',
  'private_user',
  'conversation',
]

const STATUSES: readonly MemoryStatus[] = ['candidate', 'active', 'superseded', 'deleted']

const SUBJECT_TYPES: readonly MemorySubjectType[] = ['user', 'group', 'unknown']

export function isMemoryVisibility(value: unknown): value is MemoryVisibility {
  return typeof value === 'string' && VISIBILITIES.includes(value as MemoryVisibility)
}

export function isMemoryStatus(value: unknown): value is MemoryStatus {
  return typeof value === 'string' && STATUSES.includes(value as MemoryStatus)
}

function toMemory(raw: unknown): MemoryEntry {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'memory')
  const visibility = readString(record, 'Visibility', MEMORY_ENDPOINT)
  if (!isMemoryVisibility(visibility)) {
    throw contractError(MEMORY_ENDPOINT, `Visibility 取值未知：${visibility}`)
  }
  const status = readString(record, 'Status', MEMORY_ENDPOINT)
  if (!isMemoryStatus(status)) {
    throw contractError(MEMORY_ENDPOINT, `Status 取值未知：${status}`)
  }

  const entry: MemoryEntry = {
    id: readString(record, 'ID', MEMORY_ENDPOINT),
    botId: readNumber(record, 'BotID', MEMORY_ENDPOINT),
    visibility,
    ownerKey: readString(record, 'OwnerKey', MEMORY_ENDPOINT, ''),
    content: readString(record, 'Content', MEMORY_ENDPOINT),
    confidence: readNumber(record, 'Confidence', MEMORY_ENDPOINT, 0),
    status,
  }

  const sourceMessageId = readOptionalNumber(record, 'SourceMessageID', MEMORY_ENDPOINT)
  if (sourceMessageId !== undefined && sourceMessageId > 0) {
    entry.sourceMessageId = sourceMessageId
  }
  const sourceSenderId = readOptionalNumber(record, 'SourceSenderID', MEMORY_ENDPOINT)
  if (sourceSenderId !== undefined && sourceSenderId > 0) {
    entry.sourceSenderId = sourceSenderId
  }
  const subjectType = readOptionalString(record, 'SubjectType', MEMORY_ENDPOINT)
  if (subjectType !== undefined) {
    if (!SUBJECT_TYPES.includes(subjectType as MemorySubjectType)) {
      throw contractError(MEMORY_ENDPOINT, `SubjectType 取值未知：${subjectType}`)
    }
    entry.subjectType = subjectType as MemorySubjectType
  }
  const subjectId = readOptionalString(record, 'SubjectID', MEMORY_ENDPOINT)
  if (subjectId !== undefined) {
    entry.subjectId = subjectId
  }
  const origin = readOptionalString(record, 'Origin', MEMORY_ENDPOINT)
  if (origin !== undefined) {
    if (origin !== 'manual' && origin !== 'extraction') {
      throw contractError(MEMORY_ENDPOINT, `Origin 取值未知：${origin}`)
    }
    entry.origin = origin
  }
  const source = readOptionalString(record, 'Source', MEMORY_ENDPOINT)
  if (source !== undefined) {
    entry.source = source
  }
  const approvedBy = readOptionalString(record, 'ApprovedBy', MEMORY_ENDPOINT)
  if (approvedBy !== undefined) {
    entry.approvedBy = approvedBy
  }
  const expiresAt = readOptionalTime(record, 'ExpiresAt', MEMORY_ENDPOINT)
  if (expiresAt !== undefined) {
    entry.expiresAt = expiresAt
  }
  const createdAt = readOptionalTime(record, 'CreatedAt', MEMORY_ENDPOINT)
  if (createdAt !== undefined) {
    entry.createdAt = createdAt
  }
  const updatedAt = readOptionalTime(record, 'UpdatedAt', MEMORY_ENDPOINT)
  if (updatedAt !== undefined) {
    entry.updatedAt = updatedAt
  }

  return entry
}

/**
 * The memory list envelope uses lowercase `items/total/page/limit` while each
 * entry is PascalCase. The handler echoes the *requested* page and limit
 * without normalising them, so the caller's values are the fallback.
 * 记忆列表的分页键是小写，条目是 PascalCase；handler 原样回显请求的
 * page/limit（未归一化），因此以前端请求值作为兜底。
 */
export function toMemoryPage(
  raw: unknown,
  request: { page: number; limit: number },
): PageResult<MemoryEntry> {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'page')
  if (!Array.isArray(record.items)) {
    throw contractError(MEMORY_ENDPOINT, 'items 不是数组')
  }
  const total = readNumber(record, 'total', MEMORY_ENDPOINT)
  const page = readOptionalNumber(record, 'page', MEMORY_ENDPOINT)
  const limit = readOptionalNumber(record, 'limit', MEMORY_ENDPOINT)

  return {
    items: record.items.map(toMemory),
    total,
    page: page !== undefined && page > 0 ? page : request.page,
    limit: limit !== undefined && limit > 0 ? limit : request.limit,
  }
}

function readOptionalMemoryStatus(
  record: Record<string, unknown>,
  key: string,
): MemoryStatus | '' {
  const value = readOptionalString(record, key, MEMORY_ENDPOINT)
  if (value === undefined) return ''
  if (!isMemoryStatus(value)) {
    throw contractError(MEMORY_ENDPOINT, `${key} 取值未知：${value}`)
  }
  return value
}

function toMemoryReplacement(raw: unknown): MemoryReplacement {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'replacement')
  const counterpart = asRecord(record.Counterpart, MEMORY_ENDPOINT, 'Counterpart')
  return {
    replacementId: readString(record, 'ReplacementID', MEMORY_ENDPOINT),
    direction: readString(record, 'Direction', MEMORY_ENDPOINT, ''),
    newMemoryId: readString(record, 'NewMemoryID', MEMORY_ENDPOINT),
    oldMemoryId: readString(record, 'OldMemoryID', MEMORY_ENDPOINT),
    actor: readString(record, 'Actor', MEMORY_ENDPOINT, ''),
    reason: readString(record, 'Reason', MEMORY_ENDPOINT, ''),
    newStatus: readOptionalMemoryStatus(record, 'NewStatus'),
    oldStatus: readOptionalMemoryStatus(record, 'OldStatus'),
    createdAt: readOptionalTime(record, 'CreatedAt', MEMORY_ENDPOINT),
    counterpart: {
      memoryId: readString(counterpart, 'MemoryID', MEMORY_ENDPOINT, ''),
      status: readOptionalMemoryStatus(counterpart, 'Status'),
      missing: readBoolean(counterpart, 'Missing', MEMORY_ENDPOINT, false),
    },
  }
}

/** `GET /api/v1/memories/{id}/management` → memory plus replacement relations. */
export function toMemoryManagement(raw: unknown): MemoryManagementView {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'management')
  return {
    memory: toMemory(record.Memory),
    replacements:
      record.Replacements == null
        ? []
        : asArray(record.Replacements, MEMORY_ENDPOINT, 'Replacements').map(toMemoryReplacement),
  }
}

/** `POST /api/v1/memories/{candidate_id}/supersede` result. */
export function toMemorySupersedeResult(raw: unknown): MemorySupersedeResult {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'supersede')
  const newStatus = readString(record, 'NewStatus', MEMORY_ENDPOINT)
  const oldStatus = readString(record, 'OldStatus', MEMORY_ENDPOINT)
  if (!isMemoryStatus(newStatus) || !isMemoryStatus(oldStatus)) {
    throw contractError(MEMORY_ENDPOINT, '替代结果状态未知')
  }
  return {
    newMemoryId: readString(record, 'NewMemoryID', MEMORY_ENDPOINT),
    oldMemoryId: readString(record, 'OldMemoryID', MEMORY_ENDPOINT),
    newStatus,
    oldStatus,
    replacedAt: readOptionalTime(record, 'ReplacedAt', MEMORY_ENDPOINT),
    replacedBy: readString(record, 'ReplacedBy', MEMORY_ENDPOINT, ''),
    idempotent: readBoolean(record, 'Idempotent', MEMORY_ENDPOINT, false),
  }
}

/** `POST /api/v1/memories` → `{id, duplicate}`; duplicates are never activated. */
export function toMemoryCreateResult(raw: unknown): MemoryCreateResult {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'create')
  return {
    id: readString(record, 'id', MEMORY_ENDPOINT),
    duplicate: record.duplicate === true,
  }
}
