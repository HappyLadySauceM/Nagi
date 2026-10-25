import { z } from 'zod'
import type { ScopeForm } from '../../lib/api/contracts'

/**
 * Numbers are kept as strings in the form so an empty field fails validation
 * instead of silently becoming 0; conversion happens on submit.
 * 数字在表单中保持字符串，空值会报错而不是静默变成 0；提交时再转换。
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
  enabled: z.boolean(),
  replyMode: z.enum(['mention_only', 'intent_based', 'always_reply', 'observe_only']),
  storeMessages: z.boolean(),
  allowHistoryRead: z.boolean(),
  allowMemoryWrite: z.boolean(),
  minReplyIntervalMs: numericField('最小回复间隔', 600_000),
  maxRepliesPerHour: numericField('每小时回复上限', 3_600),
  retentionDays: numericField('留存天数', 3_650),
  // Only explicit overrides are stored; an absent key means "backend default".
  // 只存显式覆盖；缺少键表示「后端默认」。
  toolPermissions: z.record(z.string(), z.boolean()),
})

export type ScopeFormValues = z.infer<typeof scopeFormSchema>

export function toScopeFormValues(form: ScopeForm): ScopeFormValues {
  return {
    enabled: form.enabled,
    replyMode: form.replyMode,
    storeMessages: form.storeMessages,
    allowHistoryRead: form.allowHistoryRead,
    allowMemoryWrite: form.allowMemoryWrite,
    minReplyIntervalMs: String(form.minReplyIntervalMs),
    maxRepliesPerHour: String(form.maxRepliesPerHour),
    retentionDays: String(form.retentionDays),
    toolPermissions: { ...form.toolPermissions },
  }
}

export function fromScopeFormValues(values: ScopeFormValues): ScopeForm {
  return {
    enabled: values.enabled,
    replyMode: values.replyMode,
    storeMessages: values.storeMessages,
    allowHistoryRead: values.allowHistoryRead,
    allowMemoryWrite: values.allowMemoryWrite,
    minReplyIntervalMs: Number(values.minReplyIntervalMs),
    maxRepliesPerHour: Number(values.maxRepliesPerHour),
    retentionDays: Number(values.retentionDays),
    toolPermissions: { ...values.toolPermissions },
  }
}
