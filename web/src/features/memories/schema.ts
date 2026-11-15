import { z } from 'zod'

const visibilityField = z.enum(['bot_shared', 'group', 'private_user', 'conversation'])

const contentField = z.string().trim().min(1, '内容不能为空').max(2_000, '内容不能超过 2000 字')
const ownerField = z.string().trim().max(100, '归属不能超过 100 字')
const subjectTypeField = z.enum(['user', 'group', 'unknown'])
const subjectIdField = z.string().trim().max(100, '主体 ID 不能超过 100 字')

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

/**
 * A candidate can only replace a fact when its subject is explicit. The UI
 * never derives a subject from the owner, source message or similar content.
 * 只有主体明确时候选才允许替代旧事实；界面不会从归属、来源消息或相似正文推断主体。
 */
function checkSubject(
  values: { visibility: string; subjectType: string; subjectId: string },
  ctx: z.RefinementCtx,
) {
  const subjectId = values.subjectId.trim()
  if ((values.subjectType === 'user' || values.subjectType === 'group') && subjectId === '') {
    ctx.addIssue({
      code: 'custom',
      path: ['subjectId'],
      message: '用户或群聊主体必须填写稳定 ID',
    })
  }
  if (values.subjectType === 'unknown' && subjectId !== '') {
    ctx.addIssue({
      code: 'custom',
      path: ['subjectId'],
      message: '未知主体不能填写主体 ID',
    })
  }
  if (values.visibility === 'private_user' && values.subjectType === 'group') {
    ctx.addIssue({
      code: 'custom',
      path: ['subjectType'],
      message: '个人记忆不能使用群聊主体',
    })
  }
}

export const memoryCreateSchema = z
  .object({
    visibility: visibilityField,
    ownerKey: ownerField,
    subjectType: subjectTypeField,
    subjectId: subjectIdField,
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
  .superRefine(checkSubject)

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
