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

export const queryKeys = {
  livez: ['system', 'livez'],
  readyz: ['system', 'readyz'],
  status: ['system', 'status'],
  accounts: ['system', 'accounts'],
  metrics: ['metrics'],
  contacts: (params: PageParams) => ['directory', 'contacts', params] as const,
  groups: (params: PageParams) => ['directory', 'groups', params] as const,
  members: (groupId: number, params: PageParams) => ['directory', 'members', groupId, params] as const,
  scopeIndex: ['scopes', 'index'] as const,
  scope: (scopeType: ScopeType, scopeId: number) =>
    ['scopes', 'detail', scopeType, scopeId] as const,
  memories: (params: MemoryParams) => ['memories', 'list', params] as const,
} as const

/** Keys invalidated after a directory sync; members refresh separately. */
export const DIRECTORY_QUERY_KEYS = [
  ['directory', 'contacts'],
  ['directory', 'groups'],
  ['directory', 'members'],
] as const
