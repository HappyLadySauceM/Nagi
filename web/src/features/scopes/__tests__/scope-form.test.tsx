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
  it('keeps the switch usable when only some of its tools apply', () => {
    renderCatalog(
      catalogOf([
        tool({ id: 'qq.get_group_info' }),
        tool({
          id: 'qq.get_private_info',
          applicable: false,
          unavailableReason: 'scope_kind_mismatch',
        }),
      ]),
    )

    expect(screen.getByRole('switch', { name: 'qq.group.read' })).toBeEnabled()
    expect(screen.getByText(/qq\.get_private_info（仅适用于群聊）/)).toBeInTheDocument()
    expect(screen.queryByText(/当前会话无法授权/)).toBeNull()
  })

  it('disables and explains a default-denied capability when every tool is inapplicable', () => {
    renderCatalog(
      catalogOf(
        [
          tool({
            id: 'qq.get_group_info',
            applicable: false,
            unavailableReason: 'scope_kind_mismatch',
          }),
          tool({
            id: 'qq.get_group_members',
            applicable: false,
            unavailableReason: 'scope_kind_mismatch',
          }),
        ],
        false,
      ),
    )

    const control = screen.getByRole('switch', { name: 'qq.group.read' })
    expect(control).not.toBeChecked()
    expect(control).toBeDisabled()
    // Both tools share one reason, so it must be listed once.
    expect(screen.getByText('该能力仅适用于群聊，当前会话无法授权。')).toBeInTheDocument()
    expect(control).toHaveAccessibleDescription(/当前会话无法授权/)
  })

  it('keeps a granted side-effecting capability disabled', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.send_message', sideEffect: true })], true))

    const control = screen.getByRole('switch', { name: 'qq.group.read' })
    expect(control).toBeChecked()
    expect(control).toBeDisabled()
    expect(screen.getByText(/包含有副作用的工具/)).toBeInTheDocument()
  })

  it('explains both reasons when a capability is blocked and inapplicable', () => {
    renderCatalog(
      catalogOf(
        [
          tool({
            id: 'qq.send_message',
            sideEffect: true,
            applicable: false,
            unavailableReason: 'scope_kind_mismatch',
          }),
        ],
        false,
      ),
    )

    const control = screen.getByRole('switch', { name: 'qq.group.read' })
    expect(control).toBeDisabled()
    expect(screen.getByText(/当前会话无法授权/)).toBeInTheDocument()
    expect(screen.getByText(/包含有副作用的工具/)).toBeInTheDocument()
    expect(control).toHaveAccessibleDescription(/当前会话无法授权.*包含有副作用的工具/)
  })

  it('disables a side-effecting capability and explains why even when unchecked', () => {
    renderCatalog(
      catalogOf([tool({ id: 'qq.send_message', sideEffect: true })], false),
    )

    const control = screen.getByRole('switch', { name: 'qq.group.read' })
    expect(control).not.toBeChecked()
    expect(control).toBeDisabled()
    expect(screen.getByText(/包含有副作用的工具/)).toBeInTheDocument()
  })

  it('keeps a previously granted switch enabled so it can be revoked', () => {
    renderCatalog(
      catalogOf([
        tool({ id: 'qq.get_group_info', applicable: false, unavailableReason: 'scope_kind_mismatch' }),
      ]),
    )

    const control = screen.getByRole('switch', { name: 'qq.group.read' })
    expect(control).toBeChecked()
    expect(control).toBeEnabled()
    expect(screen.getByText('该能力仅适用于群聊，当前会话无法授权。')).toBeInTheDocument()

    fireEvent.click(control)

    expect(control).not.toBeChecked()
    expect(control).toBeDisabled()
  })
})
