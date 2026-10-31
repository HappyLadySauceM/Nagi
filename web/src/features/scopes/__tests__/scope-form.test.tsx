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
  }
}

function renderCatalog(catalog: ToolsCatalog, disabled = false, enabled = false) {
  function Harness() {
    const form = useForm<ScopeFormValues>({
      defaultValues: toScopeFormValues({ ...DEFAULT_SCOPE_FORM, enabled }),
    })
    return <><ScopeFormFields form={form} catalog={catalog} disabled={disabled} /><output aria-label="权限覆盖">{JSON.stringify(form.watch('toolPermissions'))}</output></>
  }
  render(<Harness />)
}

describe('ScopeFormFields capability checkboxes', () => {
  it('shows only tools applicable to the edited conversation', () => {
    renderCatalog(catalogOf([
      tool({ id: 'qq.get_group_info' }),
      tool({ id: 'qq.get_private_info', applicable: false, unavailableReason: 'scope_kind_mismatch' }),
    ]))
    expect(screen.getByRole('checkbox', { name: '查询当前群信息' })).toBeEnabled()
    expect(screen.queryByText(/qq.get_private_info/)).toBeNull()
  })

  it('hides an inapplicable permission even when previously granted', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info', applicable: false })], true))
    expect(screen.queryByRole('checkbox', { name: '查询当前群信息' })).toBeNull()
  })

  it.each([true, false])('grants a side-effect tool directly for grant %s', (granted) => {
    // There is no process-level side-effect switch any more: the conversation
    // grant is the only decision, so the switch stays operable.
    // 进程级副作用开关已被移除：会话授权是唯一判定，因此开关始终可操作。
    const entry = tool({ id: 'qq.send_message', sideEffect: true, readOnly: false, permission: 'qq.api.send_message' })
    renderCatalog({ providers: ['builtin.qq'], tools: [entry], permissions: [
      { id: 'qq.api.send_message', tools: [entry.id], defaultGranted: granted },
    ] })
    const control = screen.getByRole('checkbox', { name: 'qq.send_message' })
    expect(control).toBeEnabled()
    expect(control).toHaveProperty('checked', granted)
    expect(screen.queryByText(/包含有副作用的工具/)).toBeNull()
  })

  it('allows revoking an applicable read tool', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info' })], true))
    const control = screen.getByRole('checkbox', { name: '查询当前群信息' })
    expect(control).toBeChecked()
    fireEvent.click(control)
    expect(control).not.toBeChecked()
    expect(control).toBeEnabled()
  })

  it('allows dedicated replies without enabling group administration side effects', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.send_current', sideEffect: true, readOnly: false })], false))
    const control = screen.getByRole('checkbox', { name: '连续回复当前会话' })
    expect(control).toBeEnabled()
    fireEvent.click(control)
    expect(control).toBeChecked()
  })
  it('stores only explicit changes and removes an override when returning to the default', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info' })], true))
    const checkbox = screen.getByRole('checkbox', { name: '查询当前群信息' })
    fireEvent.click(checkbox)
    expect(screen.getByLabelText('权限覆盖')).toHaveTextContent('{"qq.group.read":false}')
    fireEvent.click(checkbox)
    expect(screen.getByLabelText('权限覆盖')).toHaveTextContent('{}')
  })

  it('separates authorization from blocked runtime status and keeps technical details collapsed', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info', blockedReasons: ['流水线未启用'] })]), false, true)
    expect(screen.getByRole('checkbox', { name: '查询当前群信息' })).toBeChecked()
    expect(screen.getByText('已授权，当前不可用')).toBeVisible()
    const details = screen.getByLabelText('查询当前群信息详情').closest('details')
    expect(details).not.toHaveAttribute('open')
    fireEvent.click(screen.getByLabelText('查询当前群信息详情'))
    expect(details).toHaveAttribute('open')
    expect(screen.getByText('流水线未启用')).toBeVisible()
  })

  it('disables capability checkboxes while saving', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info' })]), true)
    expect(screen.getByRole('checkbox', { name: '查询当前群信息' })).toBeDisabled()
    expect(screen.getByText(/当前策略尚未启用/)).toBeVisible()
  })

})

it('shows separate cross-conversation permissions with Chinese description and risk', () => {
  const entry = tool({ id: 'qq.send_group_msg', description: '发送群消息', permission: 'qq.api.send_group_msg', riskLevel: 'write' })
  renderCatalog({ providers: ['builtin.qq.public'], tools: [entry], permissions: [
    { id: 'qq.cross_conversation', tools: [entry.id], defaultGranted: false, description: '跨会话操作', riskLevel: 'admin' },
  ] })
  const checkbox = screen.getByRole('checkbox', { name: '跨会话操作' })
  expect(checkbox).not.toBeChecked()
  fireEvent.click(checkbox)
  expect(screen.getByLabelText('权限覆盖')).toHaveTextContent('{"qq.cross_conversation":true}')
  expect(screen.getByText('风险级别：管理。')).toBeInTheDocument()
})

it('names a multi-tool cross-conversation group by permission instead of its first mapped tool', () => {
  // qq.get_group_info has a tool-level presentation; a multi-tool permission
  // group must not borrow it, or two checkboxes end up with the same name.
  // qq.get_group_info 有工具级文案；多工具权限组不得借用它，否则两个复选框同名。
  const read = tool({ id: 'qq.get_group_info', permission: 'qq.group.read' })
  const send = tool({ id: 'qq.send_group_msg', description: '发送群消息', permission: 'qq.api.send_group_msg', riskLevel: 'write', readOnly: false, sideEffect: true })
  renderCatalog({
    providers: ['builtin.qq'],
    tools: [read, send],
    permissions: [
      { id: 'qq.group.read', tools: [read.id], defaultGranted: true },
      { id: 'qq.cross_conversation', tools: [read.id, send.id], defaultGranted: false, riskLevel: 'admin' },
    ],
  })
  expect(screen.getByRole('checkbox', { name: '查询当前群信息' })).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: '跨会话操作' })).toBeInTheDocument()
})
