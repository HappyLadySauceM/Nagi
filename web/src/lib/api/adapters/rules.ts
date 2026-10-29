import type { RuleActionType, RuleEntry, RuleSimulation, Ruleset, ScopeType } from '../contracts'
import { asArray, asRecord, readBoolean, readNumber, readOptionalString, readString } from './guard'

const RULES_ENDPOINT = '/api/v1/intent/rulesets'

const ACTIONS: RuleActionType[] = ['reply', 'silent', 'require_semantic']

/**
 * Parses the version list. Entries are omitted by the list endpoint and only
 * present on the detail response.
 * 解析版本列表。列表接口不返回条目，只有详情接口会带上。
 */
export function toRulesetList(raw: unknown): Ruleset[] {
  const record = asRecord(raw, RULES_ENDPOINT, 'list')
  return asArray(record.Items, RULES_ENDPOINT, 'Items').map(toRuleset)
}

export function toRuleset(raw: unknown): Ruleset {
  const record = asRecord(raw, RULES_ENDPOINT, 'ruleset')
  const status = readString(record, 'Status', RULES_ENDPOINT)
  if (status !== 'draft' && status !== 'published' && status !== 'archived') {
    throw new Error(`规则集状态未知：${status}`)
  }
  const scopeType = readString(record, 'ScopeType', RULES_ENDPOINT)
  return {
    id: readNumber(record, 'ID', RULES_ENDPOINT),
    botId: readNumber(record, 'BotID', RULES_ENDPOINT),
    scopeType: scopeType as ScopeType,
    scopeId: readNumber(record, 'ScopeID', RULES_ENDPOINT),
    version: readNumber(record, 'Version', RULES_ENDPOINT),
    status,
    note: readString(record, 'Note', RULES_ENDPOINT, ''),
    entries: record.Entries == null ? [] : asArray(record.Entries, RULES_ENDPOINT, 'Entries').map(toEntry),
  }
}

function toEntry(raw: unknown): RuleEntry {
  const record = asRecord(raw, RULES_ENDPOINT, 'entry')
  const action = asRecord(record.Action, RULES_ENDPOINT, 'Action')
  const type = readString(action, 'Type', RULES_ENDPOINT)
  if (!ACTIONS.includes(type as RuleActionType)) {
    throw new Error(`规则动作未知：${type}`)
  }
  const match = record.Match == null ? {} : asRecord(record.Match, RULES_ENDPOINT, 'Match')
  return {
    priority: readNumber(record, 'Priority', RULES_ENDPOINT),
    match: {
      replyMode: readOptionalString(match, 'reply_mode', RULES_ENDPOINT),
      kind: readOptionalString(match, 'kind', RULES_ENDPOINT),
      explicit: typeof match.explicit === 'boolean' ? match.explicit : undefined,
      textContains: readOptionalString(match, 'text_contains', RULES_ENDPOINT),
      minSecondsSinceReply:
        typeof match.min_seconds_since_reply === 'number' ? match.min_seconds_since_reply : undefined,
    },
    action: { type: type as RuleActionType, reasonCode: readString(action, 'ReasonCode', RULES_ENDPOINT, '') },
  }
}

export function toRuleSimulation(raw: unknown): RuleSimulation {
  const record = asRecord(raw, '/api/v1/intent/simulate', 'result')
  return {
    blocked: readBoolean(record, 'Blocked', '/api/v1/intent/simulate'),
    explicit: readBoolean(record, 'Explicit', '/api/v1/intent/simulate'),
    needSemantic: readBoolean(record, 'NeedSemantic', '/api/v1/intent/simulate'),
    alwaysReply: readBoolean(record, 'AlwaysReply', '/api/v1/intent/simulate'),
    forceReply: readBoolean(record, 'ForceReply', '/api/v1/intent/simulate'),
    reasonCode: readString(record, 'ReasonCode', '/api/v1/intent/simulate', ''),
    rulesetVersion: readString(record, 'RulesetVersion', '/api/v1/intent/simulate', ''),
  }
}
