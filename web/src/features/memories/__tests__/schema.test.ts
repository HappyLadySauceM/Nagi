import { describe, expect, it } from 'vitest'
import { memoryCreateSchema, memoryEditSchema } from '../schema'

const createValues = {
  visibility: 'group',
  ownerKey: '555',
  content: '每周五值班交接',
  source: 'manual',
  confidence: '0.8',
  approve: false,
}

function createMessages(input: unknown): string[] {
  const result = memoryCreateSchema.safeParse(input)
  return result.success ? [] : result.error.issues.map((issue) => issue.message)
}

function editMessages(input: unknown): string[] {
  const result = memoryEditSchema.safeParse(input)
  return result.success ? [] : result.error.issues.map((issue) => issue.message)
}

describe('memoryCreateSchema', () => {
  it('accepts a scoped memory with an owner', () => {
    expect(memoryCreateSchema.safeParse(createValues).success).toBe(true)
  })

  it('rejects a bot_shared memory that carries an owner', () => {
    const messages = createMessages({
      ...createValues,
      visibility: 'bot_shared',
    })
    expect(messages).toContain('全局共享记忆不能带个人归属，请清空该字段')
  })

  it('accepts a bot_shared memory without an owner', () => {
    expect(
      memoryCreateSchema.safeParse({
        ...createValues,
        visibility: 'bot_shared',
        ownerKey: '',
      }).success,
    ).toBe(true)
  })

  it('requires an owner for every non-shared visibility', () => {
    expect(createMessages({ ...createValues, ownerKey: '  ' })).toContain(
      '该可见性必须填写归属',
    )
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

  it('still enforces the owner rule when switching to bot_shared', () => {
    const messages = editMessages({
      visibility: 'bot_shared',
      ownerKey: '10001',
      content: '共享内容',
    })

    expect(messages).toContain('全局共享记忆不能带个人归属，请清空该字段')
  })
})
