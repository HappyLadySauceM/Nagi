import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import {
  toContactPage,
  toDirectorySyncResult,
  toGroupPage,
  toMemberPage,
} from '../adapters/directory'

describe('directory adapters', () => {
  it('maps the PascalCase contact page', () => {
    const page = toContactPage({
      Items: [
        {
          UserID: 10001,
          Nickname: '阿岚',
          Remark: '项目群管理员',
          LastSeenAt: '2026-10-24T10:00:00Z',
        },
      ],
      Total: 1,
      Page: 2,
      Limit: 20,
    })

    expect(page).toEqual({
      items: [
        {
          userId: 10001,
          nickname: '阿岚',
          remark: '项目群管理员',
          lastSeenAt: '2026-10-24T10:00:00Z',
        },
      ],
      total: 1,
      page: 2,
      limit: 20,
    })
  })

  it('maps the PascalCase group page', () => {
    const page = toGroupPage({
      Items: [{ GroupID: 555, Name: '值班室', MemberCount: 42, LastSeenAt: '2026-10-23T09:00:00Z' }],
      Total: 1,
      Page: 1,
      Limit: 20,
    })

    expect(page.items[0]).toMatchObject({ groupId: 555, name: '值班室', memberCount: 42 })
  })

  it('maps the PascalCase member page', () => {
    const page = toMemberPage({
      Items: [
        {
          GroupID: 555,
          UserID: 10001,
          Nickname: '阿岚',
          Card: '夜班',
          Role: 'admin',
          LastSeenAt: '2026-10-24T10:00:00Z',
        },
      ],
      Total: 1,
      Page: 1,
      Limit: 20,
    })

    expect(page.items[0]).toEqual({
      groupId: 555,
      userId: 10001,
      nickname: '阿岚',
      card: '夜班',
      role: 'admin',
      lastSeenAt: '2026-10-24T10:00:00Z',
    })
  })

  it('drops the Go zero timestamp instead of showing year 1', () => {
    const page = toMemberPage({
      Items: [
        {
          GroupID: 1,
          UserID: 2,
          Nickname: '新成员',
          Card: '',
          Role: 'member',
          LastSeenAt: '0001-01-01T00:00:00Z',
        },
      ],
      Total: 1,
      Page: 1,
      Limit: 20,
    })

    expect(page.items[0].lastSeenAt).toBeUndefined()
  })

  it('maps the sync result counters', () => {
    expect(
      toDirectorySyncResult({ BotID: 10001, Contacts: 12, Groups: 3, Members: 0 }),
    ).toEqual({ botId: 10001, contacts: 12, groups: 3, members: 0 })
  })

  it('rejects a page without an Items array', () => {
    expect(() => toContactPage({ Total: 0, Page: 1, Limit: 20 })).toThrowError(ApiError)
  })
})
