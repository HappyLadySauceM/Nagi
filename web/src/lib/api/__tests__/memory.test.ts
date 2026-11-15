import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import { toMemoryCreateResult, toMemoryManagement, toMemoryPage, toMemorySupersedeResult } from '../adapters/memory'

const ENTRY = {
  ID: '2f7c9a52-4c1f-4f0e-8a2a-3f0f5d9f1c11',
  BotID: 10001,
  Visibility: 'group',
  OwnerKey: '555',
  Content: '每周五晚值班交接',
  SourceMessageID: 0,
  Source: 'manual',
  Confidence: 0.8,
  Status: 'candidate',
  ApprovedBy: '',
  ExpiresAt: '0001-01-01T00:00:00Z',
  CreatedAt: '2026-10-24T10:00:00Z',
  UpdatedAt: '2026-10-24T11:00:00Z',
}

describe('memory adapter', () => {
  it('maps the lowercase page envelope with PascalCase entries', () => {
    const page = toMemoryPage({ items: [ENTRY], total: 1, page: 2, limit: 20 }, { page: 1, limit: 20 })

    expect(page.total).toBe(1)
    expect(page.page).toBe(2)
    expect(page.items[0]).toEqual({
      id: ENTRY.ID,
      botId: 10001,
      visibility: 'group',
      ownerKey: '555',
      content: '每周五晚值班交接',
      source: 'manual',
      confidence: 0.8,
      status: 'candidate',
      createdAt: '2026-10-24T10:00:00Z',
      updatedAt: '2026-10-24T11:00:00Z',
    })
  })

  it('falls back to the requested paging when the handler echoes zeros', () => {
    const page = toMemoryPage({ items: [], total: 0, page: 0, limit: 0 }, { page: 3, limit: 50 })

    expect(page.page).toBe(3)
    expect(page.limit).toBe(50)
  })

  it('rejects an unknown visibility', () => {
    expect(() =>
      toMemoryPage(
        { items: [{ ...ENTRY, Visibility: 'everyone' }], total: 1, page: 1, limit: 20 },
        { page: 1, limit: 20 },
      ),
    ).toThrowError(ApiError)
  })

  it('rejects a page without items', () => {
    expect(() => toMemoryPage({ total: 0 }, { page: 1, limit: 20 })).toThrowError(ApiError)
  })

  it('reads the create result including the duplicate flag', () => {
    expect(toMemoryCreateResult({ id: 'abc', duplicate: true })).toEqual({
      id: 'abc',
      duplicate: true,
    })
  })

  it('reads management detail with subject and replacement relations', () => {
    const view = toMemoryManagement({
      Memory: {
        ...ENTRY,
        Status: 'superseded',
        SubjectType: 'user',
        SubjectID: '42',
        Origin: 'manual',
        SourceSenderID: 1001,
      },
      Replacements: [
        {
          ReplacementID: 'replacement-1',
          Direction: 'as_old',
          NewMemoryID: 'new-memory',
          OldMemoryID: ENTRY.ID,
          Actor: 'admin',
          Reason: '用户补充了新事实',
          NewStatus: 'active',
          OldStatus: 'superseded',
          CreatedAt: '2026-11-16T00:00:00Z',
          Counterpart: { MemoryID: 'new-memory', Status: 'active', Missing: false },
        },
      ],
    })
    expect(view.memory.subjectType).toBe('user')
    expect(view.memory.subjectId).toBe('42')
    expect(view.memory.sourceSenderId).toBe(1001)
    expect(view.replacements[0]).toMatchObject({
      direction: 'as_old',
      actor: 'admin',
      newStatus: 'active',
      oldStatus: 'superseded',
      counterpart: { memoryId: 'new-memory', status: 'active', missing: false },
    })
  })

  it('reads the supersede result', () => {
    expect(
      toMemorySupersedeResult({
        NewMemoryID: 'new-memory',
        OldMemoryID: 'old-memory',
        NewStatus: 'active',
        OldStatus: 'superseded',
        ReplacedAt: '2026-11-16T00:00:00Z',
        ReplacedBy: 'admin',
        Idempotent: false,
      }),
    ).toMatchObject({
      newMemoryId: 'new-memory',
      oldMemoryId: 'old-memory',
      newStatus: 'active',
      oldStatus: 'superseded',
      replacedBy: 'admin',
      idempotent: false,
    })
  })
})
