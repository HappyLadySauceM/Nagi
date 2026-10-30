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
    builtinRules: record.BuiltinRules == null ? {} : Object.fromEntries(Object.entries(asRecord(record.BuiltinRules, RULES_ENDPOINT, 'BuiltinRules')).map(([key,value]) => { if (typeof value !== 'boolean') throw new Error('Invalid builtin rule'); return [key,value] })),
    targets: record.Targets == null ? [] : asArray(record.Targets, RULES_ENDPOINT, 'Targets').map(raw => { const target = asRecord(raw, RULES_ENDPOINT, 'Target'); const kind = readString(target, 'kind', RULES_ENDPOINT); if (kind !== 'private' && kind !== 'group') throw new Error('Invalid target kind'); return {kind, id: readNumber(target, 'id', RULES_ENDPOINT)} }),
    entries: record.Entries == null ? [] : asArray(record.Entries, RULES_ENDPOINT, 'Entries').map(toEntry),
  }
}

function toEntry(raw: unknown): RuleEntry {
  const record = asRecord(raw, RULES_ENDPOINT, 'entry')
  const action = asRecord(record.Action, RULES_ENDPOINT, 'Action')
  const type = readString(action, 'type', RULES_ENDPOINT)
  if (!ACTIONS.includes(type as RuleActionType)) {
    throw new Error(`规则动作未知：${type}`)
  }
  const match = record.Match == null ? {} : asRecord(record.Match, RULES_ENDPOINT, 'Match')
  return {
    enabled: record.Enabled == null ? true : readBoolean(record, 'Enabled', RULES_ENDPOINT),
    priority: readNumber(record, 'Priority', RULES_ENDPOINT),
    match: {
      replyMode: readOptionalString(match, 'reply_mode', RULES_ENDPOINT),
      kind: readOptionalString(match, 'kind', RULES_ENDPOINT),
      explicit: typeof match.explicit === 'boolean' ? match.explicit : undefined,
      textContains: readOptionalString(match, 'text_contains', RULES_ENDPOINT),
      minSecondsSinceReply:
        typeof match.min_seconds_since_reply === 'number' ? match.min_seconds_since_reply : undefined,
    },
    action: { type: type as RuleActionType, reasonCode: readString(action, 'reason_code', RULES_ENDPOINT, '') },
  }
}

export function toRuleSimulation(raw: unknown): RuleSimulation {
  const record = asRecord(raw, '/api/v1/intent/simulate', 'result')
  const final = record.Final == null ? undefined : asRecord(record.Final, '/api/v1/intent/simulate', 'Final')
  const steps = record.Steps == null ? [] : asArray(record.Steps, '/api/v1/intent/simulate', 'Steps').map(step => asRecord(step, '/api/v1/intent/simulate', 'Step'))
  const semantic = steps.find(step => step.Phase === 'semantic')
  const semanticDetail = semantic ? asRecord(semantic.Detail, '/api/v1/intent/simulate', 'Detail') : undefined
  const scores = final?.Scores == null ? undefined : Object.fromEntries(Object.entries(asRecord(final.Scores, '/api/v1/intent/simulate', 'Scores')).map(([key,value]) => { if(typeof value !== 'number') throw new Error('Invalid score'); return [key,value] }))
  return {
    decision: final ? readString(final, 'Decision', '/api/v1/intent/simulate', '') : undefined,
    provider: final ? readString(final, 'Provider', '/api/v1/intent/simulate', '') : undefined,
    model: final ? readString(final, 'Model', '/api/v1/intent/simulate', '') : undefined,
    latencyMs: final ? readNumber(final, 'LatencyMS', '/api/v1/intent/simulate', 0) : undefined,
    semanticCategory: semanticDetail ? readString(semanticDetail, 'intent_category', '/api/v1/intent/simulate', '') : undefined,
    semanticAvailable: semanticDetail ? readBoolean(semanticDetail, 'available', '/api/v1/intent/simulate', false) : undefined,
    failureCode: semanticDetail ? readString(semanticDetail, 'failure_code', '/api/v1/intent/simulate', '') : undefined,
    scores,
    blocked: readBoolean(record, 'Blocked', '/api/v1/intent/simulate'),
    explicit: readBoolean(record, 'Explicit', '/api/v1/intent/simulate'),
    needSemantic: readBoolean(record, 'NeedSemantic', '/api/v1/intent/simulate'),
    alwaysReply: readBoolean(record, 'AlwaysReply', '/api/v1/intent/simulate'),
    forceReply: readBoolean(record, 'ForceReply', '/api/v1/intent/simulate'),
    reasonCode: final ? readString(final, 'ReasonCode', '/api/v1/intent/simulate', '') : readString(record, 'ReasonCode', '/api/v1/intent/simulate', ''),
    rulesetVersion: readString(record, 'RulesetVersion', '/api/v1/intent/simulate', ''),
  }
}

export function toRuleEntriesRequest(entries: RuleEntry[]) {
  return entries.map(entry => ({
    enabled: entry.enabled ?? true,
    priority: entry.priority,
    match: {
      reply_mode: entry.match.replyMode,
      kind: entry.match.kind,
      explicit: entry.match.explicit,
      text_contains: entry.match.textContains,
      min_seconds_since_reply: entry.match.minSecondsSinceReply,
    },
    action: { type: entry.action.type, reason_code: entry.action.reasonCode },
  }))
}
