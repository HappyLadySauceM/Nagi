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

  it('maps category, scope kinds, risk level and applicability', () => {
    const catalog = toToolsCatalog({
      ...CATALOG,
      Tools: [
        {
          ...CATALOG.Tools[0],
          Category: 'group',
          ScopeKinds: ['group'],
          RiskLevel: 'read',
          Applicable: false,
          UnavailableReason: 'scope_kind_mismatch',
        },
        CATALOG.Tools[1],
      ],
    })

    expect(catalog.tools[0]).toMatchObject({
      category: 'group',
      scopeKinds: ['group'],
      riskLevel: 'read',
      applicable: false,
      unavailableReason: 'scope_kind_mismatch',
    })
  })

  it('defaults the new fields when a legacy backend omits them', () => {
    const tool = toToolsCatalog(CATALOG).tools[0]

    expect(tool).toMatchObject({
      category: '',
      scopeKinds: [],
      riskLevel: '',
      applicable: true,
      unavailableReason: '',
    })
  })

  it('drops an unknown scope kind and keeps the recognised ones', () => {
    const catalog = toToolsCatalog({
      ...CATALOG,
      Tools: [
        {
          ...CATALOG.Tools[0],
          ScopeKinds: ['group', 'channel'],
          Applicable: false,
          UnavailableReason: 'scope_kind_mismatch',
        },
      ],
    })

    expect(catalog.tools[0]).toMatchObject({
      scopeKinds: ['group'],
      applicable: false,
      unavailableReason: 'scope_kind_mismatch',
    })
  })

  it('keeps the server applicability when every scope kind is unknown', () => {
    const catalog = toToolsCatalog({
      ...CATALOG,
      Tools: [
        {
          ...CATALOG.Tools[0],
          ScopeKinds: ['channel'],
          Applicable: false,
          UnavailableReason: 'scope_kind_mismatch',
        },
      ],
    })

    expect(catalog.tools[0]).toMatchObject({
      scopeKinds: [],
      applicable: false,
      unavailableReason: 'scope_kind_mismatch',
    })
  })

  it('rejects a non-string scope kind instead of dropping it', () => {
    expect(() =>
      toToolsCatalog({
        ...CATALOG,
        Tools: [{ ...CATALOG.Tools[0], ScopeKinds: ['group', 42] }],
      }),
    ).toThrowError(ApiError)
  })

  it('leaves an unknown risk level empty instead of failing the catalogue', () => {
    const catalog = toToolsCatalog({
      ...CATALOG,
      Tools: [{ ...CATALOG.Tools[0], RiskLevel: 'blocked' }],
    })

    expect(catalog.tools[0].riskLevel).toBe('')
  })
})
