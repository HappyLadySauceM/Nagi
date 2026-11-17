import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import { toSettings, toSettingsPutBody } from '../adapters/settings'

const SETTINGS = {
  Role: { BotName: 'Nagi', Variables: [{ Name: 'user_name', Value: '小明' }] },
  Context: { HistoryLimit: 30 },
  AI: {
    Enabled: true,
    BaseURL: 'https://llm.example/v1',
    Model: 'test-model',
    MaxOutputTokens: 400,
    GenerationTimeoutSeconds: 25,
  },
  Intent: {
    Enabled: true,
    Provider: 'jev',
    Endpoint: 'https://api.example/v1/systemone',
    Model: 'jev-latest',
    TimeoutSeconds: 3,
    ContinuationThreshold: 0.8,
    InterjectionThreshold: 0.95,
    AddressedOtherMax: 0.6,
    BotName: 'legacy-name',
    HistoryLimit: 45,
  },
  Agent: {
    MaxSteps: 6,
    MaxToolCalls: 5,
    MaxRunDurationSeconds: 30,
    Sending: { MaxMessagesPerRun: 0, MaxCharsPerMessage: 4500, MaxTotalCharsPerRun: 0 },
  },
  Messages: { RetentionDays: 30 },
  Tools: {
    TimeoutSeconds: 5,
    MaxResultBytes: 8192,
    PermissionDefaults: { 'qq.reply.current': false },
  },
}

describe('settings adapter', () => {
  it('reads Role and Context and ignores legacy Intent fields', () => {
    const settings = toSettings(SETTINGS)
    expect(settings.role).toEqual({ botName: 'Nagi', variables: [{ name: 'user_name', value: '小明' }] })
    expect(settings.context).toEqual({ historyLimit: 30 })
    expect(settings.intent).not.toHaveProperty('botName')
    expect(settings.intent).not.toHaveProperty('historyLimit')
  })

  it('defaults Variables to an empty list when the section omits them', () => {
    const settings = toSettings({ ...SETTINGS, Role: { BotName: 'Nagi' } })
    expect(settings.role.variables).toEqual([])
  })

  it('sends Role and Context without legacy Intent fields', () => {
    const body = toSettingsPutBody(toSettings(SETTINGS), 7) as {
      Version: number
      Settings: Record<string, Record<string, unknown>>
    }
    expect(body.Version).toBe(7)
    expect(body.Settings.Role).toEqual({
      BotName: 'Nagi',
      Variables: [{ Name: 'user_name', Value: '小明' }],
    })
    expect(body.Settings.Context).toEqual({ HistoryLimit: 30 })
    expect(body.Settings.Intent).not.toHaveProperty('BotName')
    expect(body.Settings.Intent).not.toHaveProperty('HistoryLimit')
  })

  it('rejects a variable whose name or value is not a string', () => {
    const broken = {
      ...SETTINGS,
      Role: { BotName: 'Nagi', Variables: [{ Name: 'user_name', Value: 42 }] },
    }
    expect(() => toSettings(broken)).toThrowError(ApiError)
  })

  it('rejects a payload without the Role section', () => {
    const { Role: _role, ...withoutRole } = SETTINGS
    expect(() => toSettings(withoutRole)).toThrowError(ApiError)
  })
})