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
const RESERVED_NAMES = ['bot_name', 'scope_type', 'messages_json', 'state', 'question']

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

/**
 * Variables panel above the Prompt editor: built-in chips plus CRUD for custom
 * variables, persisted through the Role settings section.
 * Prompt 编辑器上方的变量面板：内置变量 chip 与自定义变量增删改，经 Role 设置分区持久化。
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
  const [error, setError] = useState('')

  const role = query.data?.settings.role

  useEffect(() => {
    if (!role) {
      return
    }
    setBotName(role.botName)
    setVariables(role.variables.map((variable) => ({ ...variable })))
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

  const addVariable = () => {
    const message = nameError(newName, variables, -1)
    if (message) {
      setError(message)
      return
    }
    setVariables([...variables, { name: newName.trim(), value: newValue }])
    setNewName('')
    setNewValue('')
    setError('')
  }

  const updateVariable = (index: number, patch: Partial<SettingsPromptVariable>) => {
    setVariables(variables.map((variable, current) => (current === index ? { ...variable, ...patch } : variable)))
  }

  const removeVariable = (index: number) => {
    setVariables(variables.filter((_, current) => current !== index))
    setError('')
  }

  const dirty =
    !!role &&
    (botName.trim() !== role.botName ||
      JSON.stringify(variables.map((variable) => ({ name: variable.name.trim(), value: variable.value }))) !==
        JSON.stringify(role.variables.map((variable) => ({ name: variable.name, value: variable.value }))))

  const showMessagesJson = kind === 'memory_extraction' && scope === 'account'

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
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <button
              type="button"
              aria-label="插入 {{bot_name}}"
              className="rounded-md border border-border bg-secondary/40 px-2 py-1 font-mono hover:bg-secondary"
              onClick={() => insert('{{bot_name}}')}
            >
              {'{{bot_name}}'}
            </button>
            <button
              type="button"
              aria-label="插入 {{scope_type}}"
              className="rounded-md border border-border bg-secondary/40 px-2 py-1 font-mono hover:bg-secondary"
              onClick={() => insert('{{scope_type}}')}
            >
              {'{{scope_type}}'}
            </button>
            {showMessagesJson ? (
              <button
                type="button"
                aria-label="插入 {{messages_json}}"
                className="rounded-md border border-border bg-secondary/40 px-2 py-1 font-mono hover:bg-secondary"
                onClick={() => insert('{{messages_json}}')}
              >
                {'{{messages_json}}'}
              </button>
            ) : null}
            <span className="text-muted-foreground">点击插入到当前聚焦的文本域</span>
          </div>

          <div className="flex flex-wrap items-end gap-2">
            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">机器人名称（bot_name 值）</span>
              <Input aria-label="机器人名称" value={botName} onChange={(event) => setBotName(event.target.value)} placeholder="例如 Nagi" />
            </label>
            <span className="pb-2 text-xs text-muted-foreground">{'{{scope_type}}'} 由运行时决定，只读。</span>
          </div>

          <div className="space-y-2">
            <div className="text-xs font-medium text-muted-foreground">自定义变量</div>
            {variables.length === 0 ? (
              <p className="text-xs text-muted-foreground">暂无自定义变量。</p>
            ) : (
              <ul className="space-y-2">
                {variables.map((variable, index) => (
                  <li key={index} className="flex flex-wrap items-end gap-2">
                    <Input
                      aria-label={`变量名称 ${index + 1}`}
                      className="w-40"
                      value={variable.name}
                      onChange={(event) => updateVariable(index, { name: event.target.value })}
                    />
                    <Input
                      aria-label={`变量值 ${index + 1}`}
                      className="min-w-40 flex-1"
                      value={variable.value}
                      onChange={(event) => updateVariable(index, { value: event.target.value })}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={`删除变量 ${variable.name}`}
                      onClick={() => removeVariable(index)}
                    >
                      <Trash2 className="size-3.5" aria-hidden />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
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
              <Button type="button" variant="outline" size="sm" onClick={addVariable}>
                <Plus className="size-3.5" aria-hidden />
                添加
              </Button>
            </div>
          </div>

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
