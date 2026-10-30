import { fireEvent, render, screen } from '@testing-library/react'
import { useForm } from 'react-hook-form'
import { describe, expect, it } from 'vitest'
import { DEFAULT_SCOPE_FORM } from '../../../lib/api/adapters/scope'
import type { ToolCatalogEntry, ToolsCatalog } from '../../../lib/api/contracts'
import { ScopeFormFields } from '../components/scope-form'
import { toScopeFormValues, type ScopeFormValues } from '../schema'

function tool(overrides: Partial<ToolCatalogEntry> & { id: string }): ToolCatalogEntry {
  const { id, ...rest } = overrides
  return {
    id,
    provider: 'builtin.qq',
    description: id,
    category: 'group',
    scopeKinds: ['group'],
    riskLevel: 'read',
    readOnly: true,
    sideEffect: false,
    permission: 'qq.group.read',
    // The form reads the permission-level default; this per-tool flag only
    // satisfies the catalogue contract and never drives the switch.
    // 表单读取权限级默认值；工具级取值仅为满足目录契约，不参与开关判断。
    defaultGranted: false,
    applicable: true,
    unavailableReason: '',
    ...rest,
  }
}

function catalogOf(tools: ToolCatalogEntry[], defaultGranted = true): ToolsCatalog {
  return {
    providers: ['builtin.qq'],
    tools,
    permissions: [
      { id: 'qq.group.read', tools: tools.map((entry) => entry.id), defaultGranted },
    ],
    allowSideEffects: false,
  }
}

function renderCatalog(catalog: ToolsCatalog) {
  function Harness() {
    const form = useForm<ScopeFormValues>({
      defaultValues: toScopeFormValues(DEFAULT_SCOPE_FORM),
    })
    return <ScopeFormFields form={form} catalog={catalog} />
  }
  render(<Harness />)
}

describe('ScopeFormFields capability switches', () => {
  it('shows only tools applicable to the edited conversation', () => {
    renderCatalog(catalogOf([
      tool({ id: 'qq.get_group_info' }),
      tool({ id: 'qq.get_private_info', applicable: false, unavailableReason: 'scope_kind_mismatch' }),
    ]))
    expect(screen.getByRole('switch', { name: '查询当前群信息' })).toBeEnabled()
    expect(screen.queryByText(/qq.get_private_info/)).toBeNull()
  })

  it('hides an inapplicable permission even when previously granted', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info', applicable: false })], true))
    expect(screen.queryByRole('switch', { name: '查询当前群信息' })).toBeNull()
  })

  it.each([true, false])('keeps side effects blocked for grant %s', (granted) => {
    renderCatalog(catalogOf([tool({ id: 'qq.send_message', sideEffect: true })], granted))
    const control = screen.getByRole('switch', { name: 'qq.send_message' })
    expect(control).toBeDisabled()
    expect(control).toHaveAttribute('aria-checked', String(granted))
    expect(screen.getByText(/包含有副作用的工具/)).toBeInTheDocument()
  })

  it('allows revoking an applicable read tool', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info' })], true))
    const control = screen.getByRole('switch', { name: '查询当前群信息' })
    expect(control).toBeChecked()
    fireEvent.click(control)
    expect(control).not.toBeChecked()
    expect(control).toBeEnabled()
  })

  it('allows dedicated replies without enabling group administration side effects', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.send_current', sideEffect: true, readOnly: false })], false))
    const control = screen.getByRole('switch', { name: '连续回复当前会话' })
    expect(control).toBeEnabled()
    fireEvent.click(control)
    expect(control).toBeChecked()
  })
})
