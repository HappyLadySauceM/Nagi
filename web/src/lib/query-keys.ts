import type { MemoryStatus, MemoryVisibility, ScopeType } from './api/contracts'

export interface PageParams {
  search: string
  page: number
  limit: number
}

export interface MemoryParams extends PageParams {
  status: MemoryStatus | ''
  visibility: MemoryVisibility | ''
  ownerKey: string
  botId?: number
}

/**
 * Every account-scoped key carries the bot id, so switching accounts can never
 * reuse another account's cached rows.
 * 所有账号级查询的 key 都带 bot id，切换账号不会复用其他账号的缓存。
 */
export const queryKeys = {
  livez: ['system', 'livez'],
  readyz: ['system', 'readyz'],
  status: ['system', 'status'],
  accounts: ['system', 'accounts'],
  metrics: (botId?: number) => ['metrics', botId ?? null] as const,
  contacts: (botId: number | undefined, params: PageParams) =>
    ['directory', 'contacts', botId ?? null, params] as const,
  groups: (botId: number | undefined, params: PageParams) =>
    ['directory', 'groups', botId ?? null, params] as const,
  members: (botId: number | undefined, groupId: number, params: PageParams) =>
    ['directory', 'members', botId ?? null, groupId, params] as const,
  scopeIndex: (botId?: number) => ['scopes', 'index', botId ?? null] as const,
  scope: (botId: number | undefined, scopeType: ScopeType, scopeId: number) =>
    ['scopes', 'detail', botId ?? null, scopeType, scopeId] as const,
  memories: (params: MemoryParams) => ['memories', 'list', params] as const,
  // The tool registry is process-wide, so its key carries no account id.
  // 工具注册表属于进程级数据，因此 key 不带账号。
  toolsCatalog: ['tools', 'catalog'] as const,
} as const

/** Keys invalidated after a directory sync; members refresh separately. */
export const DIRECTORY_QUERY_KEYS = [
  ['directory', 'contacts'],
  ['directory', 'groups'],
  ['directory', 'members'],
] as const
