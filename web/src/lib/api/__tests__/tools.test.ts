import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import { toToolsCatalog } from '../adapters/tools'

const CATALOG = {
  Providers: ['builtin.qq', 'builtin.time'],
  Tools: [
    {
      ID: 'qq.get_group_info',
      Provider: 'builtin.qq',
      Description: '查询当前群信息',
      ReadOnly: true,
      SideEffect: false,
      Permission: 'qq.group.read',
      DefaultGranted: true,
    },
    {
      ID: 'system.current_time',
      Provider: 'builtin.time',
      Description: '当前时间',
      ReadOnly: true,
      SideEffect: false,
      Permission: '',
      DefaultGranted: true,
    },
  ],
  Permissions: [{ ID: 'qq.group.read', Tools: ['qq.get_group_info'], DefaultGranted: true }],
  AllowSideEffects: false,
}

describe('tools adapter', () => {
  it('maps the registered catalogue', () => {
    const catalog = toToolsCatalog(CATALOG)

    expect(catalog.providers).toEqual(['builtin.qq', 'builtin.time'])
    expect(catalog.tools[0].permission).toBe('qq.group.read')
    expect(catalog.tools[1].permission).toBe('')
    expect(catalog.permissions[0].tools).toEqual(['qq.get_group_info'])
    expect(catalog.allowSideEffects).toBe(false)
  })

  it('rejects a tool entry without an id instead of inventing one', () => {
    expect(() =>
      toToolsCatalog({ ...CATALOG, Tools: [{ ...CATALOG.Tools[0], ID: undefined }] }),
    ).toThrowError(ApiError)
  })

  it('rejects a non-boolean default grant', () => {
    expect(() =>
      toToolsCatalog({
        ...CATALOG,
        Permissions: [{ ID: 'qq.group.read', Tools: [], DefaultGranted: 'yes' }],
      }),
    ).toThrowError(ApiError)
  })
})
