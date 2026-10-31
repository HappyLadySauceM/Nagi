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

  it('keeps authorization editable without runtime warnings or technical details', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info', blockedReasons: ['未启用生成模型', '未启用意图流水线'] })]), false, true)
    expect(screen.getByRole('checkbox', { name: '查询当前群信息' })).toBeChecked()
    expect(screen.queryByText('已授权，当前不可用')).toBeNull()
    expect(screen.queryByText('详情')).toBeNull()
    expect(screen.queryByText('未启用生成模型')).toBeNull()
    expect(screen.queryByText('未启用意图流水线')).toBeNull()
  })

  it('disables capability checkboxes while saving', () => {
    renderCatalog(catalogOf([tool({ id: 'qq.get_group_info' })]), true)
    expect(screen.getByRole('checkbox', { name: '查询当前群信息' })).toBeDisabled()
    expect(screen.queryByText(/当前策略尚未启用/)).toBeNull()
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
  expect(screen.getByText('高风险')).toBeVisible()
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


it('cross-conversation grants change only the scope permission and do not list other tools as granted', () => {
  const send = tool({ id: 'qq.send_group_msg', permission: 'qq.api.send_group_msg', readOnly: false, sideEffect: true })
  renderCatalog({ providers: ['builtin.qq'], tools: [send], permissions: [
    { id: send.permission, tools: [send.id], defaultGranted: false },
    { id: 'qq.cross_conversation', tools: [send.id], defaultGranted: false },
  ] })
  fireEvent.click(screen.getByRole('checkbox', { name: '跨会话操作' }))
  expect(screen.getByRole('checkbox', { name: '发送群消息' })).not.toBeChecked()
  expect(screen.getByLabelText('权限覆盖')).toHaveTextContent('{"qq.cross_conversation":true}')
  expect(screen.queryByText('详情')).toBeNull()
  expect(screen.getByRole('checkbox', { name: '跨会话操作' }).closest('article')).not.toHaveTextContent('基础')
})

it.each([
  ['qq.can_send_image', '检查图片发送能力', '基础'],
  ['qq.get_record', '转换语音格式', '基础'],
  ['qq.get_group_member_list', '查询群成员列表', '基础'],
  ['qq.set_group_admin', '设置群管理员', '群管'],
  ['qq.set_group_add_request', '审批加群申请', '群管'],
  ['qq.get_credentials', '获取登录凭证', '系统'],
  ['qq.clean_cache', '清理缓存', '系统'],
])('classifies %s without an extension label', (id, label, category) => {
  const entry = tool({ id, permission: `permission.${id}` })
  renderCatalog({ providers: ['builtin.qq'], tools: [entry], permissions: [
    { id: entry.permission, tools: [id], defaultGranted: false },
  ] })
  const card = screen.getByRole('checkbox', { name: label }).closest('article')
  expect(screen.queryByText(category, { exact: true })).toBeNull()
  expect(card?.querySelector('label > span')).toHaveClass(category === '群管' ? 'text-violet-700' : category === '基础' ? 'text-cyan-700' : 'text-blue-700')
  expect(card).toHaveTextContent('低风险')
  expect(screen.queryByText('扩展')).toBeNull()
})

it.each([['read', '低风险'], ['write', '中风险'], ['admin', '高风险']] as const)('shows %s risk without category labels or details', (riskLevel, riskLabel) => {
  const entry = tool({ id: 'qq.get_group_info', riskLevel })
  renderCatalog({ providers: ['builtin.qq'], tools: [entry], permissions: [
    { id: entry.permission, tools: [entry.id], defaultGranted: false, riskLevel },
  ] })
  expect(screen.getByText(riskLabel)).toBeVisible()
  expect(screen.queryByText('基础')).toBeNull()
  expect(screen.queryByText('详情')).toBeNull()
})
