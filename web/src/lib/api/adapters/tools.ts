import type {
  ToolCatalogEntry,
  ToolPermission,
  ToolsCatalog,
} from '../contracts'
import { asArray, asRecord, contractError, readBoolean, readString } from './guard'

const TOOLS_ENDPOINT = '/api/v1/tools'

/**
 * Parses the tool catalogue. The backend only lists tools that registered
 * successfully at startup, so an empty list means "no capability", not "not
 * loaded yet".
 * 解析工具清单。后端只列出启动时真正注册成功的工具，
 * 因此空列表表示「没有能力」，而不是「尚未加载」。
 */
export function toToolsCatalog(raw: unknown): ToolsCatalog {
  const record = asRecord(raw, TOOLS_ENDPOINT, 'catalog')
  return {
    providers: asArray(record.Providers, TOOLS_ENDPOINT, 'Providers').map((provider) => {
      if (typeof provider !== 'string') {
        throw contractError(TOOLS_ENDPOINT, 'Providers 含非字符串项')
      }
      return provider
    }),
    tools: asArray(record.Tools, TOOLS_ENDPOINT, 'Tools').map(toToolEntry),
    permissions: asArray(record.Permissions, TOOLS_ENDPOINT, 'Permissions').map(toPermission),
    allowSideEffects: readBoolean(record, 'AllowSideEffects', TOOLS_ENDPOINT),
  }
}

function toToolEntry(raw: unknown): ToolCatalogEntry {
  const record = asRecord(raw, TOOLS_ENDPOINT, 'tool')
  return {
    id: readString(record, 'ID', TOOLS_ENDPOINT),
    provider: readString(record, 'Provider', TOOLS_ENDPOINT),
    description: readString(record, 'Description', TOOLS_ENDPOINT, ''),
    readOnly: readBoolean(record, 'ReadOnly', TOOLS_ENDPOINT),
    sideEffect: readBoolean(record, 'SideEffect', TOOLS_ENDPOINT),
    permission: readString(record, 'Permission', TOOLS_ENDPOINT, ''),
    defaultGranted: readBoolean(record, 'DefaultGranted', TOOLS_ENDPOINT),
  }
}

function toPermission(raw: unknown): ToolPermission {
  const record = asRecord(raw, TOOLS_ENDPOINT, 'permission')
  return {
    id: readString(record, 'ID', TOOLS_ENDPOINT),
    tools: asArray(record.Tools, TOOLS_ENDPOINT, 'Tools').map((tool) => {
      if (typeof tool !== 'string') {
        throw contractError(TOOLS_ENDPOINT, 'Tools 含非字符串项')
      }
      return tool
    }),
    defaultGranted: readBoolean(record, 'DefaultGranted', TOOLS_ENDPOINT),
  }
}
