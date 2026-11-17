import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { apiRequest } from '../../lib/api/client'
import { toSettingsView } from '../../lib/api/adapters/settings'
import type {
  ManagedPromptKind,
  ManagedPromptScope,
  SettingsPromptVariable,
} from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { queryKeys } from '../../lib/query-keys'
import { useSystemSettings } from '../settings/queries'

const SETTINGS_ENDPOINT = '/api/v1/settings'
const NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/
// Reserved by the runtime, fixed templates or the built-in bot_name; custom names cannot reuse them.
// 由运行时、固定模板或内置 bot_name 占用；自定义变量不能重名。
const RESERVED_NAMES = [
  'bot_name',
  'scope_type',
  'target_id',
  'sender_id',
  'bot_id',
  'messages_json',
  'state',
  'question',
]

interface RuntimeVariable {
  name: string
  description: string
}

// Read-only variables injected from the live job context; never persisted.
// 由运行任务上下文注入的只读变量，不参与持久化。
const RUNTIME_VARIABLES: RuntimeVariable[] = [
  { name: 'scope_type', description: '运行时：会话类型（群聊 / 私聊）' },
  { name: 'target_id', description: '运行时：会话号（群号 / QQ）' },
  { name: 'sender_id', description: '运行时：发送者 QQ' },
  { name: 'bot_id', description: '运行时：机器人 QQ' },
]

type EditingTarget =
  | { kind: 'builtin' }
  | { kind: 'runtime'; name: string }
  | { kind: 'custom'; index: number }
  | { kind: 'new' }
  | null

function nameError(name: string, variables: SettingsPromptVariable[], selfIndex: number): string {
  const trimmed = name.trim()
  if (!trimmed) {
    return '变量名不能为空'
  }
  if (!NAME_PATTERN.test(trimmed)) {
    return '变量名需以字母或下划线开头，仅含字母、数字与下划线'
  }
  if (RESERVED_NAMES.includes(trimmed)) {
    return `{{${trimmed}}} 为内置变量名，不能重复定义`
  }
  if (variables.some((variable, index) => index !== selfIndex && variable.name.trim() === trimmed)) {
    return '变量名不能重复'
  }
  return ''
}

function chipClass(active: boolean): string {
  return `rounded-md border px-2 py-1 font-mono text-xs transition-colors ${
    active ? 'border-primary/50 bg-primary/10' : 'border-border bg-secondary/40 hover:bg-secondary'
  }`
}

/**
 * Variables panel above the Prompt editor: read-only runtime chips plus editable
 * built-in and custom variable chips.
 * Prompt 编辑器上方的变量面板：只读运行时 chip，以及可编辑的内置变量与自定义变量 chip。
 */
export function PromptVariablesPanel({
  kind,
  scope,
  onInsertToken,
}: {
  kind: ManagedPromptKind
  scope: ManagedPromptScope
  onInsertToken?: (token: string) => boolean
}) {
  const query = useSystemSettings()
  const client = useQueryClient()
  const [open, setOpen] = useState(true)
  const [botName, setBotName] = useState('')
  const [variables, setVariables] = useState<SettingsPromptVariable[]>([])
  const [newName, setNewName] = useState('')
  const [newValue, setNewValue] = useState('')
  const [editing, setEditing] = useState<EditingTarget>(null)
  const [error, setError] = useState('')

  const role = query.data?.settings.role

  useEffect(() => {
    if (!role) {
      return
    }
    setBotName(role.botName)
    setVariables(role.variables.map((variable) => ({ ...variable })))
    setEditing(null)
    setError('')
    // Re-seed only when the stored settings revision changes.
    // 仅在已存储的配置修订变化时重置，避免覆盖正在编辑的变量。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query.data?.version])

  const save = useMutation({
    mutationFn: () => {
      if (!query.data) {
        return Promise.reject(new Error('角色设置尚未加载'))
      }
      return apiRequest(SETTINGS_ENDPOINT, {
        method: 'PATCH',
        body: {
          Version: query.data.version,
          Sections: {
            Role: {
              BotName: botName.trim(),
              Variables: variables.map((variable) => ({
                Name: variable.name.trim(),
                Value: variable.value,
              })),
            },
          },
        },
      }).then(toSettingsView)
    },
    onSuccess: (view) => {
      client.setQueryData(queryKeys.settings, view)
      toast.success('变量已保存')
    },
    onError: (mutationError) => {
      toast.error('保存变量失败', { description: describeError(mutationError).description })
    },
  })

  const insert = (token: string) => {
    if (onInsertToken?.(token)) {
      toast.success(`已插入 ${token}`)
      return
    }
    if (typeof navigator !== 'undefined' && navigator.clipboard) {
      navigator.clipboard.writeText(token).then(
        () => toast.success(`已复制 ${token}，请粘贴到 Prompt 中`),
        () => toast.info(`请手动输入 ${token}`),
      )
      return
    }
    toast.info(`请手动输入 ${token}`)
  }

  const toggle = (target: EditingTarget) => {
    setError('')
    setEditing((current) => (JSON.stringify(current) === JSON.stringify(target) ? null : target))
  }

  const addVariable = () => {
    const message = nameError(newName, variables, -1)
    if (message) {
      setError(message)
      return
    }
    setVariables([...variables, { name: newName.trim(), value: newValue }])
    setNewName('')
    setNewValue('')
    setEditing(null)
    setError('')
  }

  const updateVariable = (index: number, patch: Partial<SettingsPromptVariable>) => {
    setVariables(variables.map((variable, current) => (current === index ? { ...variable, ...patch } : variable)))
  }

  const removeVariable = (index: number) => {
    setVariables(variables.filter((_, current) => current !== index))
    setEditing(null)
    setError('')
  }

  const dirty =
    !!role &&
    (botName.trim() !== role.botName ||
      JSON.stringify(variables.map((variable) => ({ name: variable.name.trim(), value: variable.value }))) !==
        JSON.stringify(role.variables.map((variable) => ({ name: variable.name, value: variable.value }))))

  const showMessagesJson = kind === 'memory_extraction' && scope === 'account'

  const runtimeVariable = editing?.kind === 'runtime' ? RUNTIME_VARIABLES.find((item) => item.name === editing.name) : undefined
  const customVariable = editing?.kind === 'custom' ? variables[editing.index] : undefined

  const renderEditor = () => {
    if (editing?.kind === 'builtin') {
      return (
        <div className="space-y-2 rounded-md border border-border bg-secondary/20 p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="font-mono">{'{{bot_name}}'}</span>
            <span className="text-muted-foreground">内置变量，保存后写入角色名称（Role.BotName）。</span>
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">值</span>
              <Input
                aria-label="{{bot_name}} 的值"
                className="w-48"
                value={botName}
                onChange={(event) => setBotName(event.target.value)}
                placeholder="例如 Nagi"
              />
            </label>
            <Button type="button" size="sm" variant="outline" onClick={() => insert('{{bot_name}}')}>
              插入到 Prompt
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setEditing(null)}>
              关闭
            </Button>
          </div>
        </div>
      )
    }
    if (editing?.kind === 'runtime' && runtimeVariable) {
      return (
        <div className="space-y-2 rounded-md border border-border bg-secondary/20 p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="font-mono">{`{{${runtimeVariable.name}}}`}</span>
            <span className="text-muted-foreground">{runtimeVariable.description}</span>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              aria-label={`插入 {{${runtimeVariable.name}}}`}
              onClick={() => insert(`{{${runtimeVariable.name}}}`)}
            >
              插入到 Prompt
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setEditing(null)}>
              关闭
            </Button>
          </div>
        </div>
      )
    }
    if (editing?.kind === 'custom' && customVariable) {
      const index = editing.index
      return (
        <div className="space-y-2 rounded-md border border-border bg-secondary/20 p-3">
          <div className="flex flex-wrap items-end gap-2">
            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">变量名</span>
              <Input
                aria-label={`变量名称 ${index + 1}`}
                className="w-40"
                value={customVariable.name}
                onChange={(event) => updateVariable(index, { name: event.target.value })}
              />
            </label>
            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">变量值</span>
              <Input
                aria-label={`变量值 ${index + 1}`}
                className="min-w-40 flex-1"
                value={customVariable.value}
                onChange={(event) => updateVariable(index, { value: event.target.value })}
              />
            </label>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              aria-label={`插入 {{${customVariable.name}}}`}
              onClick={() => insert(`{{${customVariable.name}}}`)}
            >
              插入到 Prompt
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              aria-label={`删除变量 ${customVariable.name}`}
              onClick={() => removeVariable(index)}
            >
              <Trash2 className="size-3.5" aria-hidden />
              删除
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setEditing(null)}>
              关闭
            </Button>
          </div>
        </div>
      )
    }
    if (editing?.kind === 'new') {
      return (
        <div className="space-y-2 rounded-md border border-border bg-secondary/20 p-3">
          <div className="flex flex-wrap items-end gap-2">
            <Input
              aria-label="新变量名称"
              className="w-40"
              value={newName}
              onChange={(event) => setNewName(event.target.value)}
              placeholder="变量名"
            />
            <Input
              aria-label="新变量值"
              className="min-w-40 flex-1"
              value={newValue}
              onChange={(event) => setNewValue(event.target.value)}
              placeholder="变量值"
            />
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" size="sm" onClick={addVariable}>
              <Plus className="size-3.5" aria-hidden />
              添加
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => {
                setNewName('')
                setNewValue('')
                setEditing(null)
                setError('')
              }}
            >
              取消
            </Button>
          </div>
        </div>
      )
    }
    return null
  }

  return (
    <section className="rounded-lg border border-border p-3" aria-label="Prompt 变量">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">变量</h3>
        <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(!open)}>
          {open ? '收起' : '展开'}
        </Button>
      </div>
      {open ? (
        <div className="mt-3 space-y-3">
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs font-medium text-muted-foreground">运行时变量</span>
              <span className="text-xs text-muted-foreground">只读，由运行时提供</span>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {RUNTIME_VARIABLES.map((variable) => (
                <button
                  key={variable.name}
                  type="button"
                  aria-label={`变量 {{${variable.name}}}`}
                  title={variable.description}
                  className={chipClass(editing?.kind === 'runtime' && editing.name === variable.name)}
                  onClick={() => toggle({ kind: 'runtime', name: variable.name })}
                >
                  {`{{${variable.name}}}`}
                </button>
              ))}
            </div>
          </div>

          <div className="space-y-2">
            <div className="text-xs font-medium text-muted-foreground">自定义变量</div>
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                aria-label="变量 {{bot_name}}"
                title="{{bot_name}}（内置，写入 Role.BotName）"
                className={chipClass(editing?.kind === 'builtin')}
                onClick={() => toggle({ kind: 'builtin' })}
              >
                {'{{bot_name}}'}
              </button>
              {variables.map((variable, index) => (
                <button
                  key={index}
                  type="button"
                  aria-label={`变量 {{${variable.name}}}`}
                  className={chipClass(editing?.kind === 'custom' && editing.index === index)}
                  onClick={() => toggle({ kind: 'custom', index })}
                >
                  {`{{${variable.name}}}`}
                </button>
              ))}
              <Button type="button" variant="outline" size="sm" onClick={() => toggle({ kind: 'new' })}>
                <Plus className="size-3.5" aria-hidden />
                新增变量
              </Button>
            </div>
            {variables.length === 0 ? (
              <p className="text-xs text-muted-foreground">暂无自定义变量。</p>
            ) : null}
            {showMessagesJson ? (
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="text-muted-foreground">模板变量</span>
                <button
                  type="button"
                  aria-label="插入 {{messages_json}}"
                  className={chipClass(false)}
                  onClick={() => insert('{{messages_json}}')}
                >
                  {'{{messages_json}}'}
                </button>
              </div>
            ) : null}
          </div>

          {renderEditor()}

          {error ? (
            <p role="alert" className="text-xs text-destructive">
              {error}
            </p>
          ) : null}

          <div className="flex items-center justify-between gap-2">
            <span className="text-xs text-muted-foreground" aria-live="polite">
              {dirty ? '有未保存的变量修改' : '变量已保存'}
            </span>
            <Button type="button" size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
              {save.isPending ? '保存中…' : '保存变量'}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  )
}
