import type {
  SettingsAgent,
  SettingsAI,
  SettingsIntent,
  SettingsTools,
  SystemSettings,
  SystemSettingsView,
} from '../contracts'
import {
  asArray,
  asRecord,
  contractError,
  readBoolean,
  readNumber,
  readOptionalTime,
  readString,
} from './guard'

const SETTINGS_ENDPOINT = '/api/v1/settings'

/**
 * Parses the settings view. Every field is validated: a missing number would
 * otherwise silently become NaN in the form and be rejected only on save.
 * 解析配置视图。每个字段都会被校验：否则缺失的数字会在表单里变成 NaN，
 * 直到保存时才被后端拒绝。
 */
export function toSettingsView(raw: unknown): SystemSettingsView {
  const record = asRecord(raw, SETTINGS_ENDPOINT, 'view')
  const credentials = asRecord(record.Credentials, SETTINGS_ENDPOINT, 'Credentials')
  return {
    settings: toSettings(record.Settings),
    defaults: toSettings(record.Defaults),
    version: readNumber(record, 'Version', SETTINGS_ENDPOINT),
    updatedAt: readOptionalTime(record, 'UpdatedAt', SETTINGS_ENDPOINT),
    updatedBy: readString(record, 'UpdatedBy', SETTINGS_ENDPOINT, ''),
    persisted: readBoolean(record, 'Persisted', SETTINGS_ENDPOINT, false),
    credentials: {
      ai: readBoolean(credentials, 'AI', SETTINGS_ENDPOINT, false),
      intent: readBoolean(credentials, 'Intent', SETTINGS_ENDPOINT, false),
    },
    providers: asArray(record.Providers, SETTINGS_ENDPOINT, 'Providers').map((provider) => {
      if (typeof provider !== 'string') {
        throw contractError(SETTINGS_ENDPOINT, 'Providers 含非字符串项')
      }
      return provider
    }),
  }
}

/** Parses the whole settings model; shared by the current and default values. */
export function toSettings(raw: unknown): SystemSettings {
  const record = asRecord(raw, SETTINGS_ENDPOINT, 'Settings')
  return {
    ai: toAI(record.AI),
    intent: toIntent(record.Intent),
    agent: toAgent(record.Agent),
    tools: toTools(record.Tools),
  }
}

function toAI(raw: unknown): SettingsAI {
  const record = asRecord(raw, SETTINGS_ENDPOINT, 'AI')
  return {
    enabled: readBoolean(record, 'Enabled', SETTINGS_ENDPOINT),
    baseURL: readString(record, 'BaseURL', SETTINGS_ENDPOINT, ''),
    model: readString(record, 'Model', SETTINGS_ENDPOINT, ''),
    maxOutputTokens: readNumber(record, 'MaxOutputTokens', SETTINGS_ENDPOINT),
    generationTimeoutSeconds: readNumber(record, 'GenerationTimeoutSeconds', SETTINGS_ENDPOINT),
  }
}

function toIntent(raw: unknown): SettingsIntent {
  const record = asRecord(raw, SETTINGS_ENDPOINT, 'Intent')
  const provider = readString(record, 'Provider', SETTINGS_ENDPOINT, '')
  return {
    enabled: readBoolean(record, 'Enabled', SETTINGS_ENDPOINT),
    provider: provider === 'jev' || provider === 'openai' ? provider : '',
    endpoint: readString(record, 'Endpoint', SETTINGS_ENDPOINT, ''),
    model: readString(record, 'Model', SETTINGS_ENDPOINT, ''),
    botName: readString(record, 'BotName', SETTINGS_ENDPOINT, ''),
    timeoutSeconds: readNumber(record, 'TimeoutSeconds', SETTINGS_ENDPOINT),
    continuationThreshold: readNumber(record, 'ContinuationThreshold', SETTINGS_ENDPOINT),
    interjectionThreshold: readNumber(record, 'InterjectionThreshold', SETTINGS_ENDPOINT),
    addressedOtherMax: readNumber(record, 'AddressedOtherMax', SETTINGS_ENDPOINT),
    historyLimit: readNumber(record, 'HistoryLimit', SETTINGS_ENDPOINT),
  }
}

function toAgent(raw: unknown): SettingsAgent {
  const record = asRecord(raw, SETTINGS_ENDPOINT, 'Agent')
  const sending = asRecord(record.Sending, SETTINGS_ENDPOINT, 'Sending')
  return {
    maxSteps: readNumber(record, 'MaxSteps', SETTINGS_ENDPOINT),
    maxToolCalls: readNumber(record, 'MaxToolCalls', SETTINGS_ENDPOINT),
    maxRunDurationSeconds: readNumber(record, 'MaxRunDurationSeconds', SETTINGS_ENDPOINT),
    sending: {
      maxMessagesPerRun: readNumber(sending, 'MaxMessagesPerRun', SETTINGS_ENDPOINT),
      maxCharsPerMessage: readNumber(sending, 'MaxCharsPerMessage', SETTINGS_ENDPOINT),
      maxTotalCharsPerRun: readNumber(sending, 'MaxTotalCharsPerRun', SETTINGS_ENDPOINT),
    },
  }
}

function toTools(raw: unknown): SettingsTools {
  const record = asRecord(raw, SETTINGS_ENDPOINT, 'Tools')
  const defaults = asRecord(record.PermissionDefaults, SETTINGS_ENDPOINT, 'PermissionDefaults')
  const permissionDefaults: Record<string, boolean> = {}
  for (const [key, value] of Object.entries(defaults)) {
    if (typeof value !== 'boolean') {
      throw contractError(SETTINGS_ENDPOINT, `PermissionDefaults.${key} 不是布尔值`)
    }
    permissionDefaults[key] = value
  }
  return {
    timeoutSeconds: readNumber(record, 'TimeoutSeconds', SETTINGS_ENDPOINT),
    maxResultBytes: readNumber(record, 'MaxResultBytes', SETTINGS_ENDPOINT),
    permissionDefaults,
  }
}

/**
 * Serialises the draft back to the backend's PascalCase shape, including the
 * revision the edit was based on so a concurrent save returns 409.
 * 把草稿序列化回后端的 PascalCase 结构，并带上编辑所基于的修订号，
 * 并发保存因此会返回 409。
 */
export function toSettingsPutBody(settings: SystemSettings, version: number): unknown {
  return {
    Version: version,
    Settings: {
      AI: {
        Enabled: settings.ai.enabled,
        BaseURL: settings.ai.baseURL,
        Model: settings.ai.model,
        MaxOutputTokens: settings.ai.maxOutputTokens,
        GenerationTimeoutSeconds: settings.ai.generationTimeoutSeconds,
      },
      Intent: {
        Enabled: settings.intent.enabled,
        Provider: settings.intent.provider,
        Endpoint: settings.intent.endpoint,
        Model: settings.intent.model,
        BotName: settings.intent.botName,
        TimeoutSeconds: settings.intent.timeoutSeconds,
        ContinuationThreshold: settings.intent.continuationThreshold,
        InterjectionThreshold: settings.intent.interjectionThreshold,
        AddressedOtherMax: settings.intent.addressedOtherMax,
        HistoryLimit: settings.intent.historyLimit,
      },
      Agent: {
        MaxSteps: settings.agent.maxSteps,
        MaxToolCalls: settings.agent.maxToolCalls,
        MaxRunDurationSeconds: settings.agent.maxRunDurationSeconds,
        Sending: {
          MaxMessagesPerRun: settings.agent.sending.maxMessagesPerRun,
          MaxCharsPerMessage: settings.agent.sending.maxCharsPerMessage,
          MaxTotalCharsPerRun: settings.agent.sending.maxTotalCharsPerRun,
        },
      },
      Tools: {
        TimeoutSeconds: settings.tools.timeoutSeconds,
        MaxResultBytes: settings.tools.maxResultBytes,
        PermissionDefaults: settings.tools.permissionDefaults,
      },
    },
  }
}
