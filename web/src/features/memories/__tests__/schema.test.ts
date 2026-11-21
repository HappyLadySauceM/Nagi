import { describe, expect, it } from 'vitest'
import { memoryCreateSchema, memoryEditSchema } from '../schema'

const createValues = {
  visibility: 'group',
  ownerKey: '555',
  subjectType: 'unknown',
  subjectId: '',
  content: '每周五值班交接',
  source: 'manual',
  confidence: '0.8',
  approve: false,
}

function createMessages(input: unknown): string[] {
  const result = memoryCreateSchema.safeParse(input)
  return result.success ? [] : result.error.issues.map((issue) => issue.message)
}

describe('memoryCreateSchema', () => {
  it('accepts a scoped memory with an owner', () => {
    expect(memoryCreateSchema.safeParse(createValues).success).toBe(true)
  })

  it('rejects legacy bot_shared on create with or without an owner', () => {
    for (const ownerKey of ['', '555']) {
      expect(memoryCreateSchema.safeParse({ ...createValues, visibility: 'bot_shared', ownerKey }).success).toBe(false)
    }
  })

  it('requires an owner for every non-shared visibility', () => {
    expect(createMessages({ ...createValues, ownerKey: '  ' })).toContain(
      '该可见性必须填写归属',
    )
  })

  it('requires a stable id for user and group subjects', () => {
    for (const subjectType of ['user', 'group']) {
      expect(createMessages({ ...createValues, subjectType, subjectId: '  ' })).toContain(
        '用户或群聊主体必须填写稳定 ID',
      )
      expect(memoryCreateSchema.safeParse({ ...createValues, subjectType, subjectId: '42' }).success).toBe(true)
    }
  })

  it('keeps unknown subject candidates unaddressable instead of inventing an id', () => {
    expect(memoryCreateSchema.safeParse({ ...createValues, subjectType: 'unknown', subjectId: '' }).success).toBe(true)
    expect(createMessages({ ...createValues, subjectType: 'unknown', subjectId: '42' })).toContain(
      '未知主体不能填写主体 ID',
    )
  })

  it('rejects a group subject on a private memory', () => {
    expect(
      createMessages({
        ...createValues,
        visibility: 'private_user',
        ownerKey: '10001',
        subjectType: 'group',
        subjectId: '700',
      }),
    ).toContain('个人记忆不能使用群聊主体')
  })

  it('accepts 0, 1 and two decimals but nothing outside [0,1]', () => {
    for (const confidence of ['0', '0.8', '1', '1.00']) {
      expect(
        memoryCreateSchema.safeParse({ ...createValues, confidence }).success,
        confidence,
      ).toBe(true)
    }
    for (const confidence of ['1.5', '-0.2', '0.888', 'abc', '']) {
      expect(
        memoryCreateSchema.safeParse({ ...createValues, confidence }).success,
        confidence,
      ).toBe(false)
    }
  })

  it('rejects empty content', () => {
    expect(createMessages({ ...createValues, content: '   ' })).toContain('内容不能为空')
  })
})

describe('memoryEditSchema', () => {
  it('only validates the fields PATCH can change', () => {
    const editable = {
      visibility: 'private_user',
      ownerKey: '10001',
      content: '更新后的内容',
    }

    expect(memoryEditSchema.safeParse(editable).success).toBe(true)
  })

  it('rejects legacy bot_shared on edit', () => {
    expect(memoryEditSchema.safeParse({
      visibility: 'bot_shared', ownerKey: '', content: '历史共享内容',
    }).success).toBe(false)
  })
})
