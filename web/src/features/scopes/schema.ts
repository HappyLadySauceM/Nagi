import { z } from 'zod'
import type { ScopeForm } from '../../lib/api/contracts'

/**
 * Numbers are kept as strings in the form so an empty field fails validation
 * instead of silently becoming 0; conversion happens on submit. Zero is a valid
 * value and means "unlimited" for the sending limits.
 * 数字在表单中保持字符串，空值会报错而不是静默变成 0；提交时再转换。
 * 对发送上限而言，0 是有效取值，表示不限。
 */
function numericField(label: string, max: number) {
  return z
    .string()
    .trim()
    .min(1, `${label}不能为空`)
    .regex(/^\d+$/, `${label}必须是非负整数`)
    .refine((value) => Number(value) <= max, `${label}不能超过 ${max}`)
}

export const scopeFormSchema = z.object({
  maxMessagesPerRun: numericField('每轮消息条数', 1000),
  maxCharsPerMessage: numericField('单条字数', 100000),
  maxTotalCharsPerRun: numericField('整轮字数', 1000000),
  messageIntervalMs: numericField('气泡间隔', 60000),
  enabled: z.boolean(),
  replyMode: z.enum(['mention_only', 'intent_based', 'always_reply', 'observe_only']),
  storeMessages: z.boolean(),
  allowHistoryRead: z.boolean(),
  allowMemoryWrite: z.boolean(),
  minReplyIntervalMs: numericField('最小回复间隔', 600_000),
  maxRepliesPerHour: numericField('每小时回复上限', 100000),
  // Only explicit overrides are stored; an absent key means "backend default".
  // 只存显式覆盖；缺少键表示「后端默认」。
  toolPermissions: z.record(z.string(), z.boolean()),
}).refine(
  // A zero whole-round budget means unlimited, so the cross-field rule only
  // applies when the round is actually bounded.
  // 整轮上限为 0 表示不限，因此只有存在整轮上限时才校验单条。
  values => Number(values.maxTotalCharsPerRun) === 0 ||
    Number(values.maxCharsPerMessage) <= Number(values.maxTotalCharsPerRun),
  { message: '整轮字数不得小于单条字数', path: ['maxTotalCharsPerRun'] },
)

export type ScopeFormValues = z.infer<typeof scopeFormSchema>

export function toScopeFormValues(form: ScopeForm): ScopeFormValues {
  return {
    maxMessagesPerRun: String(form.maxMessagesPerRun ?? 0), maxCharsPerMessage: String(form.maxCharsPerMessage ?? 4500), maxTotalCharsPerRun: String(form.maxTotalCharsPerRun ?? 0), messageIntervalMs: String(form.messageIntervalMs ?? 700),
    enabled: form.enabled,
    replyMode: form.replyMode,
    storeMessages: form.storeMessages,
    allowHistoryRead: form.allowHistoryRead,
    allowMemoryWrite: form.allowMemoryWrite,
    minReplyIntervalMs: String(form.minReplyIntervalMs),
    maxRepliesPerHour: String(form.maxRepliesPerHour),
    toolPermissions: { ...form.toolPermissions },
  }
}

export function fromScopeFormValues(values: ScopeFormValues): ScopeForm {
  return {
    maxMessagesPerRun: Number(values.maxMessagesPerRun), maxCharsPerMessage: Number(values.maxCharsPerMessage), maxTotalCharsPerRun: Number(values.maxTotalCharsPerRun), messageIntervalMs: Number(values.messageIntervalMs),
    enabled: values.enabled,
    replyMode: values.replyMode,
    storeMessages: true,
    allowHistoryRead: values.allowHistoryRead,
    allowMemoryWrite: values.allowMemoryWrite,
    minReplyIntervalMs: Number(values.minReplyIntervalMs),
    maxRepliesPerHour: Number(values.maxRepliesPerHour),
    toolPermissions: { ...values.toolPermissions },
  }
}
