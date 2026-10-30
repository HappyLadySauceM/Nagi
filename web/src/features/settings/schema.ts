import type { SystemSettings } from '../../lib/api/contracts'

/**
 * Field paths mirror the backend's validation keys, so a 400 response can be
 * rendered on exactly the same inputs the client already checked.
 * 字段路径与后端的校验键一致，400 响应因此可以落在客户端已校验的同一批输入上。
 */
export type SettingsErrors = Record<string, string>

const RANGES = {
  'AI.MaxOutputTokens': [1, 32000, '取值范围 1-32000'],
  'AI.GenerationTimeoutSeconds': [1, 300, '取值范围 1-300 秒'],
  'Intent.TimeoutSeconds': [1, 120, '取值范围 1-120 秒'],
  'Intent.HistoryLimit': [1, 200, '取值范围 1-200'],
  'Agent.MaxSteps': [1, 50, '取值范围 1-50'],
  'Agent.MaxToolCalls': [1, 100, '取值范围 1-100'],
  'Agent.MaxRunDurationSeconds': [1, 600, '取值范围 1-600 秒'],
  'Agent.Sending.MaxMessagesPerRun': [1, 20, '取值范围 1-20'],
  'Agent.Sending.MaxCharsPerMessage': [1, 4000, '取值范围 1-4000'],
  'Agent.Sending.MaxTotalCharsPerRun': [1, 20000, '取值范围 1-20000'],
  'Tools.TimeoutSeconds': [1, 120, '取值范围 1-120 秒'],
  'Tools.MaxResultBytes': [256, 1048576, '取值范围 256-1048576'],
} as const

function add(errors: SettingsErrors, field: string, message: string) {
  if (!errors[field]) {
    errors[field] = message
  }
}

function checkRange(errors: SettingsErrors, field: keyof typeof RANGES, value: number) {
  const [min, max, message] = RANGES[field]
  if (!Number.isFinite(value) || value < min || value > max) {
    add(errors, field, message)
  }
}

function isHttpURL(value: string): boolean {
  if (!/^https?:\/\//i.test(value)) {
    return false
  }
  return URL.canParse(value)
}

/**
 * Validates the draft before it is sent. The backend validates again; this copy
 * only exists to point at the input immediately instead of after a round trip.
 * 发送前校验草稿。后端会再次校验；这里的副本只是让错误立刻落在输入框旁，
 * 而不是等一次往返。
 */
export function validateSettings(draft: SystemSettings, permissions: string[]): SettingsErrors {
  const errors: SettingsErrors = {}

  if (draft.ai.enabled) {
    if (!draft.intent.enabled) {
      add(errors, 'AI.Enabled', '启用生成模型前必须启用意图流水线')
    }
    if (!isHttpURL(draft.ai.baseURL)) {
      add(errors, 'AI.BaseURL', '需要以 http:// 或 https:// 开头的地址')
    }
    if (!draft.ai.model.trim()) {
      add(errors, 'AI.Model', '模型名不能为空')
    }
  }
  checkRange(errors, 'AI.MaxOutputTokens', draft.ai.maxOutputTokens)
  checkRange(errors, 'AI.GenerationTimeoutSeconds', draft.ai.generationTimeoutSeconds)

  if (draft.intent.provider !== 'jev' && draft.intent.provider !== 'openai') {
    add(errors, 'Intent.Provider', '仅支持 jev 或 openai')
  }
  if (draft.intent.enabled) {
    if (!isHttpURL(draft.intent.endpoint)) {
      add(errors, 'Intent.Endpoint', '需要以 http:// 或 https:// 开头的地址')
    }
    if (!draft.intent.model.trim()) {
      add(errors, 'Intent.Model', '模型名不能为空')
    }
    for (const [field, value] of [
      ['Intent.ContinuationThreshold', draft.intent.continuationThreshold],
      ['Intent.InterjectionThreshold', draft.intent.interjectionThreshold],
      ['Intent.AddressedOtherMax', draft.intent.addressedOtherMax],
    ] as const) {
      if (!Number.isFinite(value) || value <= 0 || value > 1) {
        add(errors, field, '阈值必须落在 (0,1] 区间')
      }
    }
    checkRange(errors, 'Intent.TimeoutSeconds', draft.intent.timeoutSeconds)
    checkRange(errors, 'Intent.HistoryLimit', draft.intent.historyLimit)
  }

  checkRange(errors, 'Agent.MaxSteps', draft.agent.maxSteps)
  checkRange(errors, 'Agent.MaxToolCalls', draft.agent.maxToolCalls)
  checkRange(errors, 'Agent.MaxRunDurationSeconds', draft.agent.maxRunDurationSeconds)
  checkRange(errors, 'Agent.Sending.MaxMessagesPerRun', draft.agent.sending.maxMessagesPerRun)
  checkRange(errors, 'Agent.Sending.MaxCharsPerMessage', draft.agent.sending.maxCharsPerMessage)
  checkRange(errors, 'Agent.Sending.MaxTotalCharsPerRun', draft.agent.sending.maxTotalCharsPerRun)
  if (draft.agent.sending.maxCharsPerMessage > draft.agent.sending.maxTotalCharsPerRun) {
    add(errors, 'Agent.Sending.MaxCharsPerMessage', '单条字数不得超过整轮字数')
  }

  checkRange(errors, 'Tools.TimeoutSeconds', draft.tools.timeoutSeconds)
  checkRange(errors, 'Tools.MaxResultBytes', draft.tools.maxResultBytes)
  for (const permission of Object.keys(draft.tools.permissionDefaults)) {
    if (permissions.length > 0 && !permissions.includes(permission)) {
      add(errors, 'Tools.PermissionDefaults', `未知的权限 ${permission}`)
      break
    }
  }

  return errors
}

/** Counts differing leaf fields between the stored revision and the draft. */
export function countChanges(current: SystemSettings, draft: SystemSettings): number {
  const before = new Map(flatten(current))
  let total = 0
  for (const [path, value] of flatten(draft)) {
    if (before.get(path) !== value) {
      total += 1
    }
  }
  return total
}

/** Structural equality used for the dirty flag. */
export function sameSettings(left: SystemSettings, right: SystemSettings): boolean {
  return countChanges(left, right) === 0
}

/** Flattens nested objects into `path -> primitive` pairs. */
export function flatten(settings: SystemSettings): Array<[string, string | number | boolean]> {
  const out: Array<[string, string | number | boolean]> = []
  const walk = (value: unknown, prefix: string) => {
    if (Array.isArray(value)) {
      out.push([prefix, [...value].sort().join(',')])
      return
    }
    if (typeof value === 'object' && value !== null) {
      for (const [key, child] of Object.entries(value)) {
        walk(child, prefix ? `${prefix}.${key}` : key)
      }
      return
    }
    out.push([prefix, value as string | number | boolean])
  }
  walk(settings, '')
  return out
}
