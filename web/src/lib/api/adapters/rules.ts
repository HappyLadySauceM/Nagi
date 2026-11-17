import type {
  BuiltinRuleCatalog,
  BuiltinRuleDefinition,
  BuiltinRuleState,
  FixedConstraintDefinition,
  FixedConstraintState,
  ManagedPromptQuestion,
  ManagedPromptRole,
  RuleActionType,
  RuleEntry,
  RuleSimulation,
  RulesEffectiveView,
  Ruleset,
  ScopeType,
} from '../contracts'
import { asArray, asRecord, readBoolean, readNumber, readOptionalString, readString } from './guard'
import { toIntentCost, toIntentRun } from './flow'

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
    questions: toQuestions(record.Questions),
  }
}

/**
 * Parses the per-version SystemOne question set. The backend serialises
 * `PromptQuestion` without JSON tags, so every field is PascalCase and the
 * zero values (`Role:""`, `Weight:0`) mean "unset". A missing set becomes `{}`.
 * 解析本版本的 SystemOne 问题集。后端序列化 `PromptQuestion` 时不带 JSON tag，
 * 字段均为 PascalCase，零值（`Role:""`、`Weight:0`）表示未设置；缺失时为 `{}`。
 */
function toQuestions(raw: unknown): Record<string, ManagedPromptQuestion> {
  if (raw == null) return {}
  const record = asRecord(raw, RULES_ENDPOINT, 'Questions')
  const questions: Record<string, ManagedPromptQuestion> = {}
  for (const [key, value] of Object.entries(record)) {
    const question = asRecord(value, RULES_ENDPOINT, 'Question')
    const type = readString(question, 'Type', RULES_ENDPOINT)
    if (type !== 'noul' && type !== 'choice') {
      throw new Error(`问题类型未知：${type}`)
    }
    const role = readOptionalString(question, 'Role', RULES_ENDPOINT) as ManagedPromptRole | undefined
    const criteria =
      question.Criteria == null
        ? undefined
        : Object.fromEntries(
            Object.entries(asRecord(question.Criteria, RULES_ENDPOINT, 'Criteria')).map(([name, text]) => {
              if (typeof text !== 'string') throw new Error('Invalid question criterion')
              return [name, text]
            }),
          )
    // A zero Weight only carries meaning for noul questions with a Role;
    // otherwise the legacy default for the role applies downstream.
    // 仅当问题为 noul 且 Role 存在时零 Weight 才有意义，否则下游沿用该 Role 的旧版默认权重。
    const weight =
      type === 'noul' && role !== undefined && typeof question.Weight === 'number'
        ? question.Weight
        : undefined
    questions[key] = {
      type: type as ManagedPromptQuestion['type'],
      instructions: readString(question, 'Instructions', RULES_ENDPOINT, ''),
      ...(criteria ? { criteria } : {}),
      ...(role ? { role } : {}),
      ...(weight !== undefined ? { weight } : {}),
    }
  }
  return questions
}

function toBuiltinRuleDefinition(value: unknown, endpoint: string): BuiltinRuleDefinition {
  const rule = asRecord(value, endpoint, 'Rule')
  const action = readString(rule, 'Action', endpoint)
  if (!ACTIONS.includes(action as RuleActionType)) {
    throw new Error(`内置规则动作未知：${action}`)
  }
  return {
    ruleId: readString(rule, 'RuleID', endpoint),
    order: readNumber(rule, 'Order', endpoint),
    title: readString(rule, 'Title', endpoint),
    condition: readString(rule, 'Condition', endpoint, ''),
    action: action as RuleActionType,
    editable: readBoolean(rule, 'Editable', endpoint, false),
    source: readString(rule, 'Source', endpoint, ''),
  }
}

function toFixedConstraintDefinition(value: unknown, endpoint: string): FixedConstraintDefinition {
  const constraint = asRecord(value, endpoint, 'FixedConstraint')
  return {
    constraintId: readString(constraint, 'ConstraintID', endpoint),
    order: readNumber(constraint, 'Order', endpoint),
    title: readString(constraint, 'Title', endpoint),
    condition: readString(constraint, 'Condition', endpoint, ''),
    action: readString(constraint, 'Action', endpoint, ''),
    reasonCode: readString(constraint, 'ReasonCode', endpoint, ''),
    editable: readBoolean(constraint, 'Editable', endpoint, false),
    source: readString(constraint, 'Source', endpoint, ''),
  }
}

/** Parses the backend-owned builtin rule catalog; titles, order and conditions are never duplicated in the page.
 * 解析后端持有的内置规则目录；标题、顺序与条件不在页面重复定义。
 */
export function toBuiltinRuleCatalog(raw: unknown): BuiltinRuleCatalog {
  const endpoint = '/api/v1/intent/builtin-rules'
  const record = asRecord(raw, endpoint, 'catalog')
  return {
    version: readString(record, 'Version', endpoint, ''),
    source: readString(record, 'Source', endpoint, ''),
    fixedConstraints:
      record.FixedConstraints == null
        ? []
        : asArray(record.FixedConstraints, endpoint, 'FixedConstraints').map((value) =>
            toFixedConstraintDefinition(value, endpoint),
          ),
    rules: asArray(record.Rules, endpoint, 'Rules').map((value) =>
      toBuiltinRuleDefinition(value, endpoint),
    ),
  }
}

/** Parses the effective rules view for one exact target.
 * 解析一个精确目标的实际生效规则视图。
 */
export function toRulesEffective(raw: unknown): RulesEffectiveView {
  const endpoint = '/api/v1/intent/rules/effective'
  const record = asRecord(raw, endpoint, 'effective')
  const builtinRules: BuiltinRuleState[] = asArray(record.BuiltinRules, endpoint, 'BuiltinRules').map(
    (value): BuiltinRuleState => {
      const definition = toBuiltinRuleDefinition(value, endpoint)
      const rule = asRecord(value, endpoint, 'Rule')
      return {
        ...definition,
        enabled: readBoolean(rule, 'Enabled', endpoint, true),
        applicable: readBoolean(rule, 'Applicable', endpoint, true),
        status: readString(rule, 'Status', endpoint, ''),
        reason: readString(rule, 'Reason', endpoint, ''),
      }
    },
  )
  const fixedConstraints: FixedConstraintState[] =
    record.FixedConstraints == null
      ? []
      : asArray(record.FixedConstraints, endpoint, 'FixedConstraints').map((value): FixedConstraintState => {
          const definition = toFixedConstraintDefinition(value, endpoint)
          const constraint = asRecord(value, endpoint, 'FixedConstraint')
          return {
            ...definition,
            ...(constraint.Value === undefined ? {} : { value: constraint.Value }),
            applicable: readBoolean(constraint, 'Applicable', endpoint, true),
            status: readString(constraint, 'Status', endpoint, ''),
            reason: readString(constraint, 'Reason', endpoint, ''),
          }
        })
  return {
    builtinRules,
    fixedConstraints,
    activeRuleset: record.ActiveRuleset == null ? null : toRuleset(record.ActiveRuleset),
    version: readString(record, 'Version', endpoint, ''),
    builtinRulesVersion: readString(record, 'BuiltinRulesVersion', endpoint, ''),
    source: readString(record, 'Source', endpoint, ''),
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
    run: record.Run == null ? undefined : toIntentRun(record.Run),
    cost: toIntentCost(record.Cost),
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

/**
 * Serialises the question set to the PascalCase shape accepted by the PATCH
 * handler. Criteria, Role and Weight are omitted when unset; a zero Weight is
 * kept only for noul questions because it explicitly excludes them from scoring.
 * 把问题集序列化为 PATCH 接口接受的 PascalCase 结构。Criteria、Role、Weight
 * 未设置时省略；noul 问题显式保留 0 权重，表示该题不计入综合分。
 */
export function toQuestionsRequest(questions: Record<string, ManagedPromptQuestion>) {
  const request: Record<
    string,
    { Type: string; Instructions: string; Criteria?: Record<string, string>; Role?: string; Weight?: number }
  > = {}
  for (const [key, question] of Object.entries(questions)) {
    const item: { Type: string; Instructions: string; Criteria?: Record<string, string>; Role?: string; Weight?: number } = {
      Type: question.type,
      Instructions: question.instructions,
    }
    if (question.criteria && Object.keys(question.criteria).length > 0) {
      item.Criteria = question.criteria
    }
    if (question.role) {
      item.Role = question.role
    }
    if (question.type === 'noul' && question.weight !== undefined) {
      item.Weight = question.weight
    }
    request[key] = item
  }
  return request
}
