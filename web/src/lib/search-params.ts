import { z } from 'zod'

/**
 * URL search schemas. Lists keep their filters in the address bar so a reload
 * or a shared link restores the same page; unknown values fall back to the
 * defaults instead of breaking the route.
 * 列表筛选放在 URL 中，刷新或分享链接可恢复同一页；非法值回退默认值而不是让路由报错。
 */
export const PAGE_SIZE_OPTIONS = [10, 20, 50] as const

export const DEFAULT_PAGE_LIMIT = 20

const pageFields = {
  search: z.string().trim().max(100).catch(''),
  page: z.coerce.number().int().min(1).max(10_000).catch(1),
  limit: z.coerce.number().int().min(1).max(200).catch(DEFAULT_PAGE_LIMIT),
}

export const pageSearchSchema = z.object(pageFields)

export type PageSearch = z.infer<typeof pageSearchSchema>

/**
 * Group detail carries the group name in the URL because the backend has no
 * single-group endpoint; a direct visit simply shows the numeric id.
 * 群详情把群名放在 URL 中，因为后端没有单群查询接口；直接访问时只显示群号。
 */
export const groupDetailSearchSchema = z.object({
  ...pageFields,
  name: z.string().trim().max(100).catch(''),
})

export type GroupDetailSearch = z.infer<typeof groupDetailSearchSchema>

export const DEFAULT_GROUP_DETAIL_SEARCH: GroupDetailSearch = {
  search: '',
  page: 1,
  limit: DEFAULT_PAGE_LIMIT,
  name: '',
}

export function parseGroupDetailSearch(input: Record<string, unknown>): GroupDetailSearch {
  return parseWith(groupDetailSearchSchema, DEFAULT_GROUP_DETAIL_SEARCH, input)
}

const MEMORY_VISIBILITY_VALUES = ['bot_shared', 'group', 'private_user', 'conversation'] as const
const MEMORY_STATUS_VALUES = ['candidate', 'active', 'superseded', 'deleted'] as const

export const memorySearchSchema = z.object({
  ...pageFields,
  status: z.union([z.enum(MEMORY_STATUS_VALUES), z.literal('')]).catch(''),
  visibility: z.union([z.enum(MEMORY_VISIBILITY_VALUES), z.literal('')]).catch(''),
  ownerKey: z.string().trim().max(100).catch(''),
})

export type MemorySearch = z.infer<typeof memorySearchSchema>

function parseWith<T>(schema: z.ZodType<T>, defaults: T, input: Record<string, unknown>): T {
  const result = schema.safeParse(input)
  return result.success ? result.data : defaults
}

export const DEFAULT_PAGE_SEARCH: PageSearch = {
  search: '',
  page: 1,
  limit: DEFAULT_PAGE_LIMIT,
}

export function parsePageSearch(input: Record<string, unknown>): PageSearch {
  return parseWith(pageSearchSchema, DEFAULT_PAGE_SEARCH, input)
}

export const DEFAULT_MEMORY_SEARCH: MemorySearch = {
  ...DEFAULT_PAGE_SEARCH,
  status: '',
  visibility: '',
  ownerKey: '',
}

export function parseMemorySearch(input: Record<string, unknown>): MemorySearch {
  return parseWith(memorySearchSchema, DEFAULT_MEMORY_SEARCH, input)
}

/** Drops empty filters so the URL and the query key stay canonical. */
export function toPageRequest(params: PageSearch): {
  search: string
  page: number
  limit: number
} {
  return { search: params.search, page: params.page, limit: params.limit }
}
