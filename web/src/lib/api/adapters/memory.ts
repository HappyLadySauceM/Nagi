import type {
  MemoryCreateResult,
  MemoryEntry,
  MemoryStatus,
  MemoryVisibility,
  PageResult,
} from '../contracts'
import {
  asRecord,
  contractError,
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

/** `POST /api/v1/memories` → `{id, duplicate}`; duplicates are never activated. */
export function toMemoryCreateResult(raw: unknown): MemoryCreateResult {
  const record = asRecord(raw, MEMORY_ENDPOINT, 'create')
  return {
    id: readString(record, 'id', MEMORY_ENDPOINT),
    duplicate: record.duplicate === true,
  }
}
