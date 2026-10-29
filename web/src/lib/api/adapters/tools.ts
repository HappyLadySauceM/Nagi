import type {
  ScopeType,
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
  const scopeKinds = toScopeKinds(record.ScopeKinds)
  // Applicability is authoritative in the response; the kind list is display
  // metadata only, so an unknown vocabulary must not override the server.
  // 适用性以响应为准，类型列表只用于展示；未知类型词汇不能覆盖服务端结果。
  const applicable = readBoolean(record, 'Applicable', TOOLS_ENDPOINT, true)
  return {
    id: readString(record, 'ID', TOOLS_ENDPOINT),
    provider: readString(record, 'Provider', TOOLS_ENDPOINT),
    description: readString(record, 'Description', TOOLS_ENDPOINT, ''),
    category: readString(record, 'Category', TOOLS_ENDPOINT, ''),
    scopeKinds,
    riskLevel: toRiskLevel(record.RiskLevel),
    readOnly: readBoolean(record, 'ReadOnly', TOOLS_ENDPOINT),
    sideEffect: readBoolean(record, 'SideEffect', TOOLS_ENDPOINT),
    permission: readString(record, 'Permission', TOOLS_ENDPOINT, ''),
    defaultGranted: readBoolean(record, 'DefaultGranted', TOOLS_ENDPOINT),
    // Legacy backends predate applicability; an unfiltered catalogue treats
    // every tool as usable, so the default stays true.
    // 旧后端没有适用性字段；未过滤的清单里每个工具都可使用，因此默认 true。
    applicable,
    // A reason only describes a tool this response marked as unusable.
    // 原因只对本次响应标记为不可用的工具有意义。
    unavailableReason: applicable
      ? ''
      : readString(record, 'UnavailableReason', TOOLS_ENDPOINT, ''),
  }
}

/**
 * Reads the optional scope-kind list, dropping kinds this build does not know.
 * A legacy backend omits the field and a newer one may advertise a kind added
 * later, so an unknown string must not fail the whole catalogue; a non-string
 * element still breaks the list contract. The result is display metadata only:
 * applicability always comes from Applicable.
 * 读取可选的会话类型列表，丢弃本版本不认识的类型。旧后端会省略该字段，
 * 新后端可能下发后来新增的类型，未知字符串不应让整个清单失败；
 * 非字符串元素仍视为契约错误。结果只用于展示，适用性始终取自 Applicable。
 */
function toScopeKinds(raw: unknown): ScopeType[] {
  if (raw === undefined || raw === null) {
    return []
  }
  return asArray(raw, TOOLS_ENDPOINT, 'ScopeKinds')
    .map((kind) => {
      if (typeof kind !== 'string') {
        throw contractError(TOOLS_ENDPOINT, 'ScopeKinds 含非字符串项')
      }
      return kind
    })
    .filter((kind): kind is ScopeType => kind === 'private' || kind === 'group')
}

/**
 * Narrows the risk level to the values this build renders. A missing or newer
 * level reads as empty because the field only ranks tools for display.
 * 收窄为本版本能展示的风险级别。缺失或更新的级别按空串处理，
 * 因为该字段只用于给工具分级展示。
 */
function toRiskLevel(raw: unknown): ToolCatalogEntry['riskLevel'] {
  if (raw === undefined || raw === null) {
    return ''
  }
  if (typeof raw !== 'string') {
    throw contractError(TOOLS_ENDPOINT, 'RiskLevel 不是字符串')
  }
  return raw === 'read' || raw === 'write' || raw === 'admin' ? raw : ''
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
