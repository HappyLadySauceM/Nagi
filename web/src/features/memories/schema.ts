import { z } from 'zod'

const visibilityField = z.enum(['bot_shared', 'group', 'private_user', 'conversation'])

const contentField = z.string().trim().min(1, '内容不能为空').max(2_000, '内容不能超过 2000 字')
const ownerField = z.string().trim().max(100, '归属不能超过 100 字')

/**
 * `bot_shared` must never carry an owner, every other visibility requires one.
 * 全局共享记忆不得带归属，其余可见性必须填写归属。
 */
function checkOwner(values: { visibility: string; ownerKey: string }, ctx: z.RefinementCtx) {
  const owner = values.ownerKey.trim()
  if (values.visibility === 'bot_shared') {
    if (owner !== '') {
      ctx.addIssue({
        code: 'custom',
        path: ['ownerKey'],
        message: '全局共享记忆不能带个人归属，请清空该字段',
      })
    }
    return
  }
  if (owner === '') {
    ctx.addIssue({ code: 'custom', path: ['ownerKey'], message: '该可见性必须填写归属' })
  }
}

export const memoryCreateSchema = z
  .object({
    visibility: visibilityField,
    ownerKey: ownerField,
    content: contentField,
    source: z.string().trim().max(100, '来源不能超过 100 字'),
    confidence: z
      .string()
      .trim()
      .min(1, '请填写置信度')
      .regex(/^(0(\.\d{1,2})?|1(\.0{1,2})?)$/, '置信度需在 0 到 1 之间，最多两位小数'),
    approve: z.boolean(),
  })
  .superRefine(checkOwner)

export type MemoryCreateValues = z.infer<typeof memoryCreateSchema>

/**
 * The edit sheet only touches content, visibility and owner — the fields that
 * `PATCH` actually changes. Confidence is not editable, so it is not validated
 * here (stored floats would fail a two-decimal rule the user cannot fix).
 * 编辑只改 PATCH 支持的内容、可见性与归属；置信度不可编辑，因此不参与校验
 * （库中的浮点精度会让用户无法修正两位小数的规则）。
 */
export const memoryEditSchema = z
  .object({
    visibility: visibilityField,
    ownerKey: ownerField,
    content: contentField,
  })
  .superRefine(checkOwner)

export type MemoryEditValues = z.infer<typeof memoryEditSchema>

export function parseConfidence(value: string): number {
  return Number(value)
}
