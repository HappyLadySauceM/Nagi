/**
 * Front-end DTOs use camelCase only; the Go management API keeps its own wire
 * shape and every difference is normalised in `lib/api/adapters`.
 *
 * Wire facts verified against the Go handlers (2026-10-24):
 * - Catalogue pages (`/contacts`, `/groups`, `/groups/{id}/members`) serialise
 *   domain structs without JSON tags, so the payload keys are PascalCase:
 *   `Items/Total/Page/Limit`, `UserID/GroupID/Nickname/...`.
 * - The memory listing envelope is the exception: its keys are lowercase
 *   `items/total/page/limit`, while each memory entry is PascalCase.
 * - `/api/v1/metrics` keys are snake_case and hold aggregate counters, not a
 *   time series.
 * - Write endpoints only accept snake_case bodies.
 */

export interface StatusSnapshot {
  version: string
  uptimeSeconds: number
  echoEnabled: boolean
  handlers: string[]
  handledEvents: number
}

/** One configured SnowLuma endpoint and the QQ account discovered on it. */
export interface AccountSummary {
  /** Undefined until `get_login_info` or the first event identifies the account. */
  botId?: number
  nickname: string
  endpoint: string
  expectedQq?: number
  connected: boolean
  selfIdKnown: boolean
  /** Non-empty when the endpoint was refused, e.g. an unexpected QQ number. */
  rejected?: string
  heartbeatSeen: boolean
  lastHeartbeat?: string
  syncedAt?: string
}

export interface AccountsSnapshot {
  connected: boolean
  dbConfigured: boolean
  accounts: AccountSummary[]
}

export interface PingState {
  status: string
}

export interface IntentDecisionCount {
  decision: string
  reasonCode: string
  count: number
}

/** One ordered distribution row; known statuses are zero-filled by the adapter. */
export interface StatusCount {
  key: string
  count: number
}

export interface MetricsSnapshot {
  messages24h: number
  intentDecisions24h: IntentDecisionCount[]
  messageJobs: StatusCount[]
  memories: StatusCount[]
}

/** Ordered counters used by the dashboard distribution charts. */
export const JOB_STATUSES_TRACE = ['pending', 'processing', 'done', 'failed', 'cancelled'] as const

export const JOB_STATUS_LABELS: Record<string, string> = {
  pending: '等待中',
  processing: '处理中',
  done: '已完成',
  failed: '失败',
  cancelled: '已取消',
}

export const MEMORY_STATUSES_TRACE = ['candidate', 'active', 'superseded', 'deleted'] as const

export const MEMORY_STATUS_LABELS: Record<string, string> = {
  candidate: '候选',
  active: '已启用',
  superseded: '已取代',
  deleted: '已删除',
}

export const DECISION_LABELS: Record<string, string> = {
  reply: '回复',
  silent: '不回复',
  defer: '延后判断',
}

/** One server-paged list; `page` and `limit` are the values actually displayed. */
export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  limit: number
}

export interface Contact {
  policy?: ScopePolicy
  userId: number
  nickname: string
  remark: string
  lastSeenAt?: string
}

/** Bot's role inside one group; empty means the role is not known yet. */
export type GroupRole = '' | 'owner' | 'admin' | 'member'

export interface Group {
  policy?: ScopePolicy
  groupId: number
  name: string
  memberCount: number
  /** Cached role from group_members; refresh on demand before relying on it. */
  botRole?: GroupRole
  lastSeenAt?: string
}

export interface GroupMember {
  groupId: number
  userId: number
  nickname: string
  card: string
  role: string
  lastSeenAt?: string
}

export interface DirectorySyncResult {
  botId: number
  contacts: number
  groups: number
  members: number
}

export type ScopeType = 'group' | 'private'

export type ReplyMode = 'mention_only' | 'intent_based' | 'always_reply' | 'observe_only'

/**
 * Explicit per-scope overrides of tool permissions. A missing key means "use the
 * backend default", so the UI never freezes a default into the database.
 * 作用域级的工具权限显式覆盖。缺少键表示「使用后端默认」，
 * 界面因此不会把默认值冻结进数据库。
 */
export type ToolPermissionOverrides = Record<string, boolean>

/** One registered tool exposed to the model. */
export interface ToolCatalogEntry {
  blockedReasons?: string[]
  id: string
  provider: string
  description: string
  category: string
  /** Conversation kinds the tool applies to; empty means every kind. */
  scopeKinds: ScopeType[]
  /**
   * Graded impact of the tool; empty when the backend omits it or reports a
   * level this build does not know yet. The field is informational, so an
   * unfamiliar value must not fail the catalogue.
   * 工具的影响级别；后端省略该字段或下发本版本未知的级别时为空串。
   * 该字段仅用于展示，未知取值不应让清单加载失败。
   */
  riskLevel: 'read' | 'write' | 'admin' | ''
  readOnly: boolean
  sideEffect: boolean
  permission: string
  /** Minimum bot role in the target group; empty when no role is required. */
  requiresGroupRole?: GroupRole
  defaultGranted: boolean
  /**
   * False when the catalogue was requested for a scope kind this tool does not
   * apply to. Legacy backends omit the field, which reads as true.
   * 工具不适用于请求的会话类型时为 false；旧后端不返回该字段时按 true 处理。
   */
  applicable: boolean
  /**
   * Stable machine-readable reason when `applicable` is false; empty otherwise.
   * `applicable` 为 false 时的稳定机器可读原因，其余情况为空串。
   */
  unavailableReason: string
}

/** One permission shared by one or more tools. */
export interface ToolPermission {
  /** Unconditional prerequisites; conditional target/content grants are separate.
   * 必需的前置能力；按目标或内容检查的条件授权不在此列。 */
  requires?: string[]
  description?: string
  riskLevel?: string
  id: string
  tools: string[]
  /** Minimum bot role in the target group; empty when no role is required. */
  requiresGroupRole?: GroupRole
  defaultGranted: boolean
  kind?: 'tool' | 'pipeline'
  applicable?: boolean
  unavailableReason?: string
}

/** `GET /api/v1/tools`: what is actually registered right now. */
export interface ToolsCatalog {
  providers: string[]
  tools: ToolCatalogEntry[]
  permissions: ToolPermission[]
}

/** Generation model section of the system settings. */
export interface SettingsAI {
  enabled: boolean
  baseURL: string
  model: string
  maxOutputTokens: number
  generationTimeoutSeconds: number
}

/** One user-defined prompt variable rendered as {{name}} at runtime. */
export interface SettingsPromptVariable {
  name: string
  value: string
}

/** Bot identity shared by intent judgment and reply generation. */
export interface SettingsRole {
  botName: string
  /** Custom prompt variables, ordered; names are unique identifiers. */
  variables: SettingsPromptVariable[]
}

/** Conversation history budget shared by intent and generation. */
export interface SettingsContext {
  historyLimit: number
}

/** Intent pipeline section of the system settings. Connection fields mirror the model catalogue. */
export interface SettingsIntent {
  enabled: boolean
  provider: 'jev' | 'openai' | ''
  endpoint: string
  model: string
  timeoutSeconds: number
  continuationThreshold: number
  interjectionThreshold: number
  addressedOtherMax: number
}

/** Outbound budget of one agent run; zero means unlimited. */
export interface SettingsSending {
  maxMessagesPerRun: number
  maxCharsPerMessage: number
  maxTotalCharsPerRun: number
}

/** Agent runtime limits. */
export interface SettingsAgent {
  maxSteps: number
  maxToolCalls: number
  maxRunDurationSeconds: number
  sending: SettingsSending
}

/**
 * Global message retention. Retention is no longer configured per conversation;
 * zero disables cleanup.
 * 全局消息留存。留存不再按会话配置；0 表示不清理。
 */
export interface SettingsMessages {
  retentionDays: number
}

/**
 * Tool call limits. Permission defaults stay in the payload as an internal
 * baseline but are no longer editable from the console.
 * 工具调用限制。权限默认值作为内部基线保留在载荷中，但不再由控制台编辑。
 */
export interface SettingsTools {
  timeoutSeconds: number
  maxResultBytes: number
  permissionDefaults: Record<string, boolean>
}

/** Mutable, process-wide configuration managed from the console. */
export interface SystemSettings {
  role: SettingsRole
  context: SettingsContext
  ai: SettingsAI
  intent: SettingsIntent
  agent: SettingsAgent
  messages: SettingsMessages
  tools: SettingsTools
}

/** Secret-bearing sections only report presence; values never leave the server. */
export interface SettingsCredentials {
  ai: boolean
  intent: boolean
}

/** `GET /api/v1/settings`: current revision plus the restore-defaults baseline. */
export interface SystemSettingsView {
  settings: SystemSettings
  defaults: SystemSettings
  version: number
  updatedAt?: string
  updatedBy: string
  /** False when the backend runs without a database: changes are memory-only. */
  persisted: boolean
  credentials: SettingsCredentials
  /** Registered tool provider ids, in display order. */
  providers: string[]
}

/** Form-shaped policy; `PUT` always writes every field. */
export interface ScopeForm {
  maxMessagesPerRun?: number
  maxCharsPerMessage?: number
  maxTotalCharsPerRun?: number
  messageIntervalMs?: number
  enabled: boolean
  replyMode: ReplyMode
  storeMessages: boolean
  allowHistoryRead: boolean
  allowMemoryRead: boolean
  allowMemoryWrite: boolean
  minReplyIntervalMs: number
  maxRepliesPerHour: number
  toolPermissions: ToolPermissionOverrides
}

/** Effective policy of one scope, including the default-deny values. */
export interface ScopePolicy extends ScopeForm {
  source?: string
  version?: number
  templateVersion?: number
  scopeType: ScopeType
  scopeId: number
  permissionDefaults?: Record<string, boolean>
  effectiveToolPermissions?: Record<string, boolean>
}

export const SCOPE_TYPE_LABELS: Record<ScopeType, string> = {
  group: '群聊',
  private: '私聊',
}

export const REPLY_MODE_LABELS: Record<ReplyMode, string> = {
  mention_only: '仅响应 @ 与引用',
  intent_based: '由意图判定是否插话',
  always_reply: '每条消息都回复',
  observe_only: '只记录不回复',
}

export const REPLY_MODE_HINTS: Record<ReplyMode, string> = {
  mention_only: '最可控：只有被 @ 或引用时才参与对话。',
  intent_based: 'Jev 判断是否值得插话，未命中则不回复。',
  always_reply: '对每条收到的消息都生成回复，注意限频与打扰。',
  observe_only: '只写入消息与记忆，不主动发言。',
}

export type MemoryVisibility = 'bot_shared' | 'group' | 'private_user' | 'conversation'

export type MemoryStatus = 'candidate' | 'active' | 'superseded' | 'deleted'

export type MemorySubjectType = 'user' | 'group' | 'unknown'
export type MemoryOrigin = 'manual' | 'extraction'

export interface MemoryEntry {
  id: string
  botId: number
  visibility: MemoryVisibility
  ownerKey: string
  content: string
  sourceMessageId?: number
  sourceSenderId?: number
  source?: string
  origin?: MemoryOrigin
  subjectType?: MemorySubjectType
  subjectId?: string
  confidence: number
  status: MemoryStatus
  approvedBy?: string
  expiresAt?: string
  createdAt?: string
  updatedAt?: string
}

export interface MemoryCreateResult {
  id: string
  duplicate: boolean
}

export interface MemoryReplacementCounterpart {
  memoryId: string
  status: MemoryStatus | ''
  missing: boolean
}

/** One immutable replacement relation shown on the management detail. */
export interface MemoryReplacement {
  replacementId: string
  direction: string
  newMemoryId: string
  oldMemoryId: string
  actor: string
  reason: string
  newStatus: MemoryStatus | ''
  oldStatus: MemoryStatus | ''
  createdAt?: string
  counterpart: MemoryReplacementCounterpart
}

export interface MemoryManagementView {
  memory: MemoryEntry
  replacements: MemoryReplacement[]
}

export interface MemorySupersedeResult {
  newMemoryId: string
  oldMemoryId: string
  newStatus: MemoryStatus
  oldStatus: MemoryStatus
  replacedAt?: string
  replacedBy: string
  idempotent: boolean
}

export const MEMORY_SUBJECT_LABELS: Record<MemorySubjectType, string> = {
  user: '用户',
  group: '群聊',
  unknown: '未知',
}

export const MEMORY_VISIBILITY_LABELS: Record<MemoryVisibility, string> = {
  bot_shared: '全局共享',
  group: '群级',
  private_user: '个人',
  conversation: '会话级',
}

export const MEMORY_VISIBILITY_HINTS: Record<MemoryVisibility, string> = {
  bot_shared: '对该机器人下的所有会话可见，审批前请确认内容适合全局共享。',
  group: '仅同一群内的会话可见，需要归属群号。',
  private_user: '仅该好友的私聊可见，需要归属 QQ 号。',
  conversation: '仅单个会话可见，需要会话归属标识。',
}

export type KnowledgePromptId = 'chat.reply' | 'intent.participation' | 'memory.extraction'

export interface KnowledgeMemorySummary {
  total: number
  active: number
  candidate: number
  superseded: number
  byVisibility: Record<MemoryVisibility, number>
}

export interface KnowledgeExtractionSummary {
  enabled: boolean
  pendingJobs: number
  processingJobs: number
  failedJobs: number
  lastErrorCode: string
}

export interface KnowledgePromptSummary {
  activeVersion: number
  source: string
}

export interface KnowledgeSummary {
  memories: KnowledgeMemorySummary
  extraction: KnowledgeExtractionSummary
  prompts: Record<KnowledgePromptId, KnowledgePromptSummary>
}

export type ExtractionJobStatus = 'pending' | 'processing' | 'done' | 'failed' | 'cancelled'

export const EXTRACTION_JOB_STATUS_LABELS: Record<ExtractionJobStatus, string> = {
  pending: '等待',
  processing: '处理中',
  done: '完成',
  failed: '失败',
  cancelled: '已取消',
}

export interface ExtractionJob {
  id: string
  botId: number
  conversationId: string
  scopeType: string
  scopeId: number
  messageId: number
  status: ExtractionJobStatus
  attempts: number
  roundAttempts: number
  nextRunAt?: string
  lastErrorCode: string
  manualRetries: number
  sourceAvailable: boolean
  createdAt?: string
  updatedAt?: string
}

export interface ExtractionSettings {
  scope: 'global'
  enabled: boolean
  concurrency: number
  batchMaxMessages: number
  batchMaxRunes: number
  maxWaitMs: number
  pollIntervalMs: number
  leaseDurationMs: number
  maxAttempts: number
  retryDelaysMs: number[]
  source: string
  editable: boolean
}

export interface KnowledgePreviewTarget {
  type: 'group' | 'private'
  id: number
  conversationId: string
}

export interface KnowledgePreviewPolicy {
  enabled: boolean
  replyMode: string
  storeMessages: boolean
  allowMemoryRead: boolean
  allowHistoryRead: boolean
}

export interface KnowledgePreviewTools {
  skipped: boolean
  reason: string
  policy: KnowledgePreviewPolicy
  effectivePermissions: string[]
  explicitOverrides: Record<string, boolean>
}

export interface KnowledgePreviewMemory {
  id: string
  visibility: MemoryVisibility
  ownerKey: string
  content: string
  status: MemoryStatus
  source?: string
  subjectType?: string
  subjectId?: string
  updatedAt?: string
}

export interface KnowledgePreviewMessage {
  id: number
  messageId: number
  senderId: number
  isBot: boolean
  text: string
  createdAt?: string
}

export interface KnowledgePreviewPrompt {
  promptId: string
  name: string
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  version: number
  source: string
  hash: string
  finalHash: string
  layers: ManagedPromptEffectiveLayer[]
  content: ManagedPromptContent
  variables: Record<string, string>
}

export interface KnowledgePreview {
  previewMode: 'example' | 'real'
  target: KnowledgePreviewTarget
  defaults: {
    chatSystem: string
    extractionSystem: string
  }
  prompts: Record<string, KnowledgePreviewPrompt>
  tools: KnowledgePreviewTools
  memory: {
    skipped: boolean
    reason: string
    items: KnowledgePreviewMemory[]
  }
  history: {
    skipped: boolean
    reason: string
    messages: KnowledgePreviewMessage[]
  }
}

export type IntentDecisionKind = 'reply' | 'silent' | 'defer'

export interface DecisionStepEntry {
  index: number
  phase: string
  reasonCode: string
  detail: Record<string, unknown>
  createdAt?: string
}

export interface DecisionEntry {
  id: number
  jobId: number
  botId: number
  conversationId: string
  decision: IntentDecisionKind
  reasonCode: string
  provider: string
  model: string
  rulesetVersion: string
  latencyMs: number
  createdAt?: string
}

export interface DecisionDetailEntry extends DecisionEntry {
  scores: Record<string, number>
  steps: DecisionStepEntry[]
  run?: IntentRun
}

export const DECISION_KIND_LABELS: Record<IntentDecisionKind, string> = {
  reply: '回复',
  silent: '沉默',
  defer: '延后',
}

export const DECISION_PHASE_LABELS: Record<string, string> = {
  policy: '策略',
  rules: '规则',
  semantic: '语义',
  final: '结论',
}

export type RuleActionType = 'reply' | 'silent' | 'require_semantic'

export interface RuleMatch {
  replyMode?: string
  kind?: string
  explicit?: boolean
  textContains?: string
  minSecondsSinceReply?: number
}

export interface RuleEntry {
 enabled?: boolean
  priority: number
  match: RuleMatch
  action: { type: RuleActionType; reasonCode: string }
}

export interface RuleTarget { kind: ScopeType; id: number }

export interface BuiltinRuleDefinition {
  ruleId: string
  order: number
  title: string
  condition: string
  action: RuleActionType
  editable: boolean
  source: string
}

export interface BuiltinRuleState extends BuiltinRuleDefinition {
  enabled: boolean
  applicable: boolean
  status: string
  reason: string
}

export interface FixedConstraintDefinition {
  constraintId: string
  order: number
  title: string
  condition: string
  action: string
  reasonCode: string
  editable: boolean
  source: string
}

export interface FixedConstraintState extends FixedConstraintDefinition {
  value?: unknown
  applicable: boolean
  status: string
  reason: string
}

export interface BuiltinRuleCatalog {
  version: string
  source: string
  fixedConstraints: FixedConstraintDefinition[]
  rules: BuiltinRuleDefinition[]
}

export interface RulesEffectiveView {
  builtinRules: BuiltinRuleState[]
  fixedConstraints: FixedConstraintState[]
  activeRuleset: Ruleset | null
  version: string
  builtinRulesVersion: string
  source: string
}

export interface Ruleset {
 builtinRules?: Record<string, boolean>
 targets?: RuleTarget[]
  id: number
  botId: number
  scopeType: ScopeType
  scopeId: number
  version: number
  status: 'draft' | 'published' | 'archived'
  note: string
  entries: RuleEntry[]
  /** Per-version SystemOne question set; empty means the built-in questions apply.
   * 本版本覆盖的 SystemOne 问题集；为空表示沿用内置问题。 */
  questions?: Record<string, ManagedPromptQuestion>
}

export interface RuleSimulation {
  decision?: string
  provider?: string
  model?: string
  scores?: Record<string, number>
  latencyMs?: number
  semanticAvailable?: boolean
  semanticCategory?: string
  failureCode?: string

  blocked: boolean
  explicit: boolean
  needSemantic: boolean
  alwaysReply: boolean
  forceReply: boolean
  reasonCode: string
  rulesetVersion: string
  run?: IntentRun
  cost?: IntentCost
}

export type IntentNodeStatus = 'executed' | 'skipped' | 'failed' | 'degraded' | 'unknown'
export type IntentEdgeStatus = 'taken' | 'not_taken' | 'unknown'
export type IntentSnapshotStatus = 'complete' | 'partial' | 'missing' | 'legacy'

export interface IntentFlowNode {
  nodeId: string
  kind: string
  title: string
  description: string
  stageIndex: number
  version: number
}

export interface IntentFlowEdge {
  edgeId: string
  fromNodeId: string
  toNodeId: string
  label: string
  condition: string
}

export interface IntentFlow {
  flowVersion: string
  nodes: IntentFlowNode[]
  edges: IntentFlowEdge[]
}

export interface IntentEvidence {
  kind: string
  text: string
}

export interface IntentRunNode {
  nodeId: string
  version: number
  status: IntentNodeStatus
  branch: string
  reasonCode: string
  summary: string
  detail: Record<string, unknown>
  evidence: IntentEvidence[]
  durationMs: number
}

export interface IntentRunEdge {
  edgeId: string
  status: IntentEdgeStatus
}

export interface IntentDeliveryItem {
  id: string
  sequence: number
  status: string
  text: string
  platformMessageId: number
  errorCode: string
}

export interface IntentDelivery {
  outcome: string
  reason: string
  denied: number
  items: IntentDeliveryItem[]
}

export interface IntentRunSummary {
  decision: IntentDecisionKind
  reasonCode: string
  reasonText: string
}

export interface IntentReplayInfo {
  input: string
  semantic: string
  prompt: string
  policy: string
  rules: string
  persisted: boolean
}

export interface IntentPromptInfo {
  id: string
  name: string
  version: number
  source: string
  sourceRef: string
  hash: string
}

export interface IntentRun {
  decisionId: number
  jobId: number
  runId: string
  flowVersion: string
  provider: string
  model: string
  rulesetVersion: string
  snapshotStatus: IntentSnapshotStatus
  missingFields: string[]
  summary: IntentRunSummary
  nodes: IntentRunNode[]
  edges: IntentRunEdge[]
  delivery: IntentDelivery
  replay?: IntentReplayInfo
  prompt?: IntentPromptInfo
  scores: Record<string, number>
  latencyMs: number
  createdAt?: string
}

export interface IntentCost {
  mayIncur: boolean
  costEstimate?: string
  known?: boolean
  amount?: number
  currency?: string
  text?: string
}

export type PromptStatus = 'draft' | 'published' | 'archived'

export interface PromptSummary {
  promptId: string
  name: string
  activeVersion: number
  status: PromptStatus
  source: string
  updatedAt: string
}

export interface PromptVersion {
  id: number
  promptId: string
  name: string
  provider: string
  version: number
  status: PromptStatus
  source: string
  sourceRef: string
  hash: string
  note: string
  content: unknown
  createdAt: string
  publishedAt: string
}

export interface PromptDetail {
  promptId: string
  name: string
  versions: PromptVersion[]
}

export type PromptSyncStatusValue = 'not_configured' | 'idle' | 'ok' | 'failed'
export type PromptSyncResultStatus = 'already_synced' | 'candidate_created'

export interface PromptSyncCandidate {
  id: number
  version: number
  name: string
  hash: string
  sourceRef: string
  createdAt: string
}

export interface PromptSyncActive {
  version: number
  source: string
  sourceRef: string
  status: PromptStatus
}

export interface PromptSyncStatus {
  promptId: string
  provider: string
  configured: boolean
  status: PromptSyncStatusValue
  reasonCode: string
  message: string
  lastAttemptAt?: string
  lastSuccessAt?: string
  active?: PromptSyncActive
  candidates: PromptSyncCandidate[]
}

export interface PromptSyncResult {
  promptId: string
  provider: string
  remoteName: string
  remoteVersion: number
  candidate: PromptVersion
  status: PromptSyncResultStatus
}

export type ReplayEvaluationMode = 'recalculate' | 'model'

export interface ReplayTokenEstimate {
  inputTokens: number
  outputTokens: number
  totalTokens: number
  method: string
}

export interface ReplayEstimate {
  decisionId: number
  provider: string
  model: string
  sampleCount: number
  estimatedExternalRequests: number
  tokenEstimate?: ReplayTokenEstimate
  cost?: IntentCost
  confirmationToken: string
  expiresAt?: string
  parametersFingerprint: string
}

export interface ReplayReproduction {
  fullyReproduced: boolean
  reasons: string[]
}

export interface ReplayEvaluationSummary {
  id: number
  decisionId: number
  mode: ReplayEvaluationMode
  name: string
  note?: string
  changed: boolean
  summary: IntentRunSummary
  createdAt?: string
  reproduction: ReplayReproduction
}

export interface ReplayParameters {
  provider: string
  model: string
  promptId: string
  promptVersion: number
  promptHash: string
  rulesetVersion: string
  sampleCount: number
  estimatedExternalRequests: number
  tokenEstimate?: ReplayTokenEstimate
}

export interface ReplayEvaluation {
  id: number
  decisionId: number
  botId: number
  mode: ReplayEvaluationMode
  name: string
  note: string
  changed: boolean
  before: IntentRun
  after: IntentRun
  parameters?: ReplayParameters
  cost?: IntentCost
  reproduction: ReplayReproduction
  createdAt?: string
}

export interface ReplayDifference {
  field: string
  before: string
  after: string
}

export interface ReplayComparison {
  evaluationId: number
  against: string
  changed: boolean
  original: IntentRun
  replay: IntentRun
  differences: ReplayDifference[]
  reproduction: ReplayReproduction
}

export const RULE_ACTION_LABELS: Record<RuleActionType, string> = {
  reply: '强制回复',
  silent: '强制沉默',
  require_semantic: '要求语义判定',
}

export const RULESET_STATUS_LABELS: Record<Ruleset['status'], string> = {
  draft: '草稿',
  published: '已发布',
  archived: '已归档',
}

export type ManagedPromptKind = 'chat_reply' | 'intent_participation' | 'memory_extraction'
export type ManagedPromptScope = 'account' | 'group' | 'private'
export type ManagedPromptProvider = 'openai' | 'jev'
export type ManagedPromptStatus = 'draft' | 'published' | 'archived'
export type ManagedPromptSource = 'builtin' | 'local' | 'langfuse'

export type ManagedPromptRole =
  | 'directed_to_bot'
  | 'continues_bot_thread'
  | 'addressed_to_other'
  | 'appropriate_to_join'
  | 'intent'
  | 'custom'

export interface ManagedPromptQuestion {
  type: 'noul' | 'choice'
  instructions: string
  criteria?: Record<string, string>
  role?: ManagedPromptRole
  weight?: number
}

/** Provider-specific content of one Prompt version; absent branches are not sent to the server. */
export interface ManagedPromptContent {
  provider: ManagedPromptProvider
  openai?: {
    systemPrompt?: string
    userTemplate?: string
    responseShape?: string
  }
  systemOne?: {
    questions?: Record<string, ManagedPromptQuestion>
  }
}

export interface ManagedPromptLayer {
  botId: number
  scopeType: ManagedPromptScope
  scopeId: number
  activeVersion: number
  activeVersionId: number
  activeSource: ManagedPromptSource | ''
  status: string
  updatedAt: string
}

export interface ManagedPromptListItem {
  promptId: string
  kind: ManagedPromptKind
  name: string
  provider: ManagedPromptProvider
  builtinVersion: number
  layers: ManagedPromptLayer[]
}

export interface ManagedPromptBuiltin {
  version: number
  status: 'builtin'
  source: 'builtin'
  hash: string
  content: ManagedPromptContent
}

export interface ManagedPromptVersion {
  id: number
  promptId: string
  kind: ManagedPromptKind
  name: string
  provider: ManagedPromptProvider
  botId: number
  scopeType: ManagedPromptScope
  scopeId: number
  version: number
  status: ManagedPromptStatus
  source: ManagedPromptSource
  sourceRef: string
  hash: string
  note: string
  content: ManagedPromptContent
  createdAt: string
  publishedAt: string | null
  archivedAt: string | null
}

export interface ManagedPromptDetail {
  promptId: string
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  builtin: ManagedPromptBuiltin
  active: ManagedPromptVersion | null
  versions: ManagedPromptVersion[]
}

export interface ManagedPromptDiffSide {
  source: string
  id?: number
  version: number
  status: string
  hash: string
}

export interface ManagedPromptDiffLine {
  op: '+' | '-' | ' '
  text: string
}

export interface ManagedPromptDiff {
  promptId: string
  provider: ManagedPromptProvider
  from: ManagedPromptDiffSide
  to: ManagedPromptDiffSide
  changed: boolean
  unifiedDiff: string
  lines: ManagedPromptDiffLine[]
}

export interface ManagedPromptEffectiveLayer {
  layer: string
  versionId?: number
  version?: number
  source?: string
  hash?: string
  content?: ManagedPromptContent
  editable?: boolean
  summary?: string
}

export interface ManagedPromptPreview {
  promptId: string
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  previewMode: 'example' | 'real'
  versionId: number
  target?: { type: 'group' | 'private'; id: number; conversationId?: string }
  layers: ManagedPromptEffectiveLayer[]
  final: { content: ManagedPromptContent; variables: Record<string, string> }
  warnings: string[]
  previewHash: string
}

export interface ManagedPromptEffective {
  promptId: string
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  botId: number
  target: { type: 'group' | 'private'; id: number; conversationId?: string }
  layers: ManagedPromptEffectiveLayer[]
  final: { content: ManagedPromptContent; variables: Record<string, string> }
  hash: string
}

export interface ManagedPromptPublishResult {
  version: ManagedPromptVersion
  archivedVersionId: number
}

export interface ManagedPromptRestoreResult {
  promptId: string
  scopeType: ManagedPromptScope
  scopeId: number
  provider: ManagedPromptProvider
  active: null
  resolved: { source: string; version: number; hash: string }
  archivedVersionId: number
}
