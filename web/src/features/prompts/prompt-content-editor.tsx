import { useEffect, useRef, useState, type MutableRefObject } from 'react'
import { Trash2 } from 'lucide-react'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Select } from '../../components/ui/select'
import { Textarea } from '../../components/ui/textarea'
import type {
  ManagedPromptContent,
  ManagedPromptKind,
  ManagedPromptProvider,
  ManagedPromptQuestion,
  ManagedPromptRole,
  ManagedPromptScope,
} from '../../lib/api/contracts'
import {
  JEV_QUESTIONS,
  PROMPT_ROLE_LABELS,
  QUESTION_KEY_PATTERN,
  STANDARD_PROMPT_ROLES,
  inferRoleForKey,
} from './prompt-content'

/** Card title for one question; custom questions include their editable key.
 * 单个问题卡片的标题；自定义问题带上可编辑的 key。
 */
function questionTitle(key: string, question: ManagedPromptQuestion): string {
  const role = question.role ?? inferRoleForKey(key)
  return role === 'custom' ? `自定义问题（${key}）` : PROMPT_ROLE_LABELS[role]
}

/** Next unused custom key, e.g. custom_1, custom_2.
 * 下一个未使用的自定义 key，例如 custom_1、custom_2。
 */
function nextCustomKey(questions: Record<string, ManagedPromptQuestion>): string {
  let index = 1
  while (`custom_${index}` in questions) {
    index += 1
  }
  return `custom_${index}`
}

/** Builds a new question from the default template for a standard role.
 * 依据默认模板为标准 Role 构造新问题。
 */
function addedQuestion(role: ManagedPromptRole): ManagedPromptQuestion {
  const template = JEV_QUESTIONS.find((item) => item.role === role)
  return {
    type: template?.type ?? (role === 'intent' ? 'choice' : 'noul'),
    instructions: '',
    ...(template?.criteria ? { criteria: { ...template.criteria } } : {}),
    role,
    ...(template?.weight !== undefined ? { weight: template.weight } : {}),
  }
}

/** Read-only render of one Prompt content, grouped by each stored question's role.
 * 只读展示一份 Prompt 内容，按存储中每个问题的 Role 分组。
 */
export function PromptContentView({
  kind,
  provider,
  content,
  scope = 'account',
}: {
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  content: ManagedPromptContent
  scope?: ManagedPromptScope
}) {
  if (kind === 'intent_participation' && provider === 'jev') {
    const questions = content.systemOne?.questions ?? {}
    const entries = Object.entries(questions)
    if (entries.length === 0) {
      return <p className="text-sm text-muted-foreground">未记录</p>
    }
    return (
      <div className="space-y-3">
        {entries.map(([key, item]) => {
          const role = item.role ?? inferRoleForKey(key)
          const title = questionTitle(key, item)
          return (
            <div key={key} className="rounded-md border border-border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">{title}</span>
                {role === 'custom' ? null : (
                  <code className="text-xs text-muted-foreground">{key}</code>
                )}
                <Badge variant="outline">{item.type}</Badge>
              </div>
              <p className="mt-2 whitespace-pre-wrap break-words text-sm">
                {item.instructions || '未记录'}
              </p>
              {item.criteria && scope === 'account' ? (
                <dl className="mt-2 grid gap-1 text-xs text-muted-foreground sm:grid-cols-2">
                  {Object.entries(item.criteria).map(([criterion, label]) => (
                    <div key={criterion} className="flex gap-2">
                      <dt className="font-mono text-foreground/80">{criterion}</dt>
                      <dd>{label}</dd>
                    </div>
                  ))}
                </dl>
              ) : null}
            </div>
          )
        })}
      </div>
    )
  }
  const openai = content.openai
  return (
    <div className="space-y-3 text-sm">
      <div>
        <div className="text-xs text-muted-foreground">System Prompt</div>
        <pre className="mt-1 whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 text-xs leading-relaxed">
          {openai?.systemPrompt || '未记录'}
        </pre>
      </div>
      {openai?.replyStyle ? (
        <div>
          <div className="text-xs text-muted-foreground">回复风格</div>
          <p className="mt-1 whitespace-pre-wrap break-words text-xs">{openai.replyStyle}</p>
        </div>
      ) : null}
      {openai?.userTemplate !== undefined ? (
        <div>
          <div className="text-xs text-muted-foreground">UserTemplate</div>
          <pre className="mt-1 whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 text-xs leading-relaxed">
            {openai.userTemplate || '未记录'}
          </pre>
        </div>
      ) : null}
      {openai?.responseShape !== undefined ? (
        <div className="text-xs text-muted-foreground">
          ResponseShape：<span className="font-mono text-foreground">{openai.responseShape}</span>
        </div>
      ) : null}
    </div>
  )
}

/** Callback that inserts a token into whichever Prompt textarea last held focus.
 * 把 token 插入到最后聚焦的 Prompt 文本域的回调。
 */
export type InsertToken = (token: string) => boolean

interface PromptEditorProps {
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  scope: ManagedPromptScope
  content: ManagedPromptContent
  onChange: (content: ManagedPromptContent) => void
  /** Optional: receives an insert callback so a variables panel can inject {{name}}.
   * 可选：接收插入回调，供变量面板注入 {{name}}。
   */
  insertRef?: MutableRefObject<InsertToken | null>
}

function updateOpenAI(
  content: ManagedPromptContent,
  patch: Partial<NonNullable<ManagedPromptContent['openai']>>,
): ManagedPromptContent {
  return { ...content, openai: { ...(content.openai ?? {}), ...patch } }
}

function updateQuestion(
  content: ManagedPromptContent,
  key: string,
  patch: Partial<ManagedPromptQuestion>,
): ManagedPromptContent {
  const questions = { ...(content.systemOne?.questions ?? {}) }
  const current = questions[key] ?? { type: 'noul', instructions: '' }
  questions[key] = { ...current, ...patch }
  return { ...content, systemOne: { ...(content.systemOne ?? {}), questions } }
}

function updateCriterion(
  content: ManagedPromptContent,
  key: string,
  criterion: string,
  value: string,
): ManagedPromptContent {
  const current = content.systemOne?.questions?.[key]
  const criteria = { ...(current?.criteria ?? {}), [criterion]: value }
  return updateQuestion(content, key, { criteria })
}

/** Editable Prompt content. Supplement layers only expose append-only fields.
 * 可编辑的 Prompt 内容；补充层只暴露追加字段。
 */
export function PromptEditor({ kind, provider, scope, content, onChange, insertRef }: PromptEditorProps) {
  const account = scope === 'account'
  const isJev = kind === 'intent_participation' && provider === 'jev'
  const jevQuestions = content.systemOne?.questions ?? {}
  const firstJevKey = Object.keys(jevQuestions)[0] ?? JEV_QUESTIONS[0].key
  const defaultField = isJev ? `q:${firstJevKey}` : 'systemPrompt'
  const [addRole, setAddRole] = useState<ManagedPromptRole | ''>('')
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)

  // Keep the latest props reachable from the stable insert callback.
  // 让稳定的插入回调始终能读到最新的 props。
  const latest = useRef({ content, onChange, defaultField })
  latest.current = { content, onChange, defaultField }
  const focusedField = useRef<string | null>(null)
  const elements = useRef(new Map<string, HTMLInputElement | HTMLTextAreaElement>())

  useEffect(() => {
    if (!insertRef) {
      return
    }
    const insert: InsertToken = (token) => {
      // Template-only variables belong to UserTemplate even when System Prompt held focus.
      // 模板专用变量必须写入 UserTemplate，即使当前聚焦的是 System Prompt。
      const templateOnly = ['{{messages_json}}', '{{state}}', '{{question}}']
      let field = focusedField.current ?? latest.current.defaultField
      if (templateOnly.includes(token) && elements.current.has('userTemplate')) {
        field = 'userTemplate'
      }
      const element = elements.current.get(field)
      const current = latest.current.content
      const value = element?.value ?? ''
      const start = element?.selectionStart ?? value.length
      const end = element?.selectionEnd ?? start
      const nextValue = value.slice(0, start) + token + value.slice(end)
      const nextContent = field.startsWith('q:')
        ? updateQuestion(current, field.slice(2), { instructions: nextValue })
        : updateOpenAI(current, { [field]: nextValue })
      latest.current.onChange(nextContent)
      if (element) {
        element.focus()
        const caret = start + token.length
        window.setTimeout(() => {
          element.selectionStart = caret
          element.selectionEnd = caret
        }, 0)
      }
      return true
    }
    insertRef.current = insert
    return () => {
      insertRef.current = null
    }
  }, [insertRef])

  const fieldProps = (field: string) => ({
    ref: (element: HTMLInputElement | HTMLTextAreaElement | null) => {
      if (element) {
        elements.current.set(field, element)
      } else {
        elements.current.delete(field)
      }
    },
    onFocus: () => {
      focusedField.current = field
    },
  })

  const addQuestion = () => {
    if (!addRole) {
      return
    }
    const questions = { ...(content.systemOne?.questions ?? {}) }
    const key = addRole === 'custom' ? nextCustomKey(questions) : addRole
    if (key in questions) {
      return
    }
    questions[key] =
      addRole === 'custom'
        ? { type: 'noul', instructions: '', role: 'custom', weight: 1 }
        : addedQuestion(addRole)
    onChange({ ...content, systemOne: { ...(content.systemOne ?? {}), questions } })
    setAddRole('')
  }

  const renameQuestion = (oldKey: string, rawKey: string) => {
    const nextKey = rawKey.replace(/[^A-Za-z0-9_]/g, '')
    if (nextKey === '' || nextKey === oldKey || !QUESTION_KEY_PATTERN.test(nextKey)) {
      return
    }
    const questions = { ...(content.systemOne?.questions ?? {}) }
    if (nextKey in questions) {
      return
    }
    const entries = Object.entries(questions).map(
      ([key, item]) => [key === oldKey ? nextKey : key, item] as const,
    )
    onChange({ ...content, systemOne: { ...(content.systemOne ?? {}), questions: Object.fromEntries(entries) } })
  }

  const removeQuestion = (key: string) => {
    const questions = { ...(content.systemOne?.questions ?? {}) }
    delete questions[key]
    onChange({ ...content, systemOne: { ...(content.systemOne ?? {}), questions } })
    setPendingDelete(null)
  }

  if (isJev) {
    const entries = Object.entries(jevQuestions)
    const usedRoles = new Set(entries.map(([key, item]) => item.role ?? inferRoleForKey(key)))
    const availableStandardRoles = STANDARD_PROMPT_ROLES.filter((role) => !usedRoles.has(role))
    return (
      <div className="space-y-4">
        <p className="text-xs text-muted-foreground">
          {account
            ? '问题可增删；Role 决定对应的内置规则，删除标准 Role 后对应内置规则不再生效。'
            : '补充层只能追加说明，不能新增题目或修改 Role/Type/Criteria。'}
        </p>
        {account ? (
          <div className="flex flex-wrap items-end gap-2">
            <label className="space-y-1 text-xs text-muted-foreground">
              <span>添加问题</span>
              <Select
                aria-label="添加问题类型"
                value={addRole}
                onChange={(event) => setAddRole(event.target.value as ManagedPromptRole | '')}
                containerClassName="w-44"
              >
                <option value="">选择…</option>
                {availableStandardRoles.map((role) => (
                  <option key={role} value={role}>
                    {PROMPT_ROLE_LABELS[role]}
                  </option>
                ))}
                <option value="custom">自定义问题</option>
              </Select>
            </label>
            <Button type="button" size="sm" variant="outline" onClick={addQuestion} disabled={!addRole}>
              添加
            </Button>
          </div>
        ) : null}
        {entries.map(([key, item]) => {
          const role = item.role ?? inferRoleForKey(key)
          const title = questionTitle(key, item)
          return (
            <div key={key} className="space-y-2 rounded-md border border-border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="outline">{PROMPT_ROLE_LABELS[role]}</Badge>
                {role === 'custom' && account ? (
                  <Input
                    aria-label="自定义问题 key"
                    value={key}
                    className="w-36"
                    onChange={(event) => renameQuestion(key, event.target.value)}
                  />
                ) : (
                  <code className="text-xs text-muted-foreground">{key}</code>
                )}
                <Badge variant="secondary">{item.type}</Badge>
                {account ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="ml-auto"
                    aria-label={`删除 ${title}`}
                    onClick={() => (role === 'custom' ? removeQuestion(key) : setPendingDelete(key))}
                  >
                    <Trash2 aria-hidden />
                    删除
                  </Button>
                ) : null}
              </div>
              <Textarea
                aria-label={`${title} Instructions`}
                value={item.instructions ?? ''}
                {...fieldProps(`q:${key}`)}
                onChange={(event) =>
                  onChange(updateQuestion(content, key, { instructions: event.target.value }))
                }
                placeholder={account ? '编辑题目描述' : '补充说明（追加到基础描述后）'}
              />
              {account && item.type === 'noul' ? (
                <label className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span>Weight（-10 ~ 10）</span>
                  <Input
                    type="number"
                    min={-10}
                    max={10}
                    step={1}
                    aria-label={`${title} Weight`}
                    className="w-24"
                    value={item.weight ?? ''}
                    onChange={(event) =>
                      onChange(
                        updateQuestion(content, key, {
                          weight: event.target.value === '' ? undefined : Number(event.target.value),
                        }),
                      )
                    }
                  />
                </label>
              ) : null}
              {account && item.type === 'choice' && item.criteria ? (
                <div className="grid gap-2 sm:grid-cols-2">
                  {Object.entries(item.criteria).map(([criterion, text]) => (
                    <label key={criterion} className="space-y-1 text-xs text-muted-foreground">
                      <span>
                        <code className="font-mono text-foreground/80">{criterion}</code> 描述
                      </span>
                      <Input
                        aria-label={`${title} ${criterion} 描述`}
                        value={text}
                        onChange={(event) =>
                          onChange(updateCriterion(content, key, criterion, event.target.value))
                        }
                      />
                    </label>
                  ))}
                </div>
              ) : null}
              {account && pendingDelete === key ? (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  <p>删除「{title}」后对应内置规则不再生效。</p>
                  <div className="mt-2 flex gap-2">
                    <Button type="button" size="sm" variant="outline" onClick={() => setPendingDelete(null)}>
                      取消
                    </Button>
                    <Button type="button" size="sm" variant="destructive" onClick={() => removeQuestion(key)}>
                      确认删除
                    </Button>
                  </div>
                </div>
              ) : null}
            </div>
          )
        })}
        {entries.length === 0 ? <p className="text-sm text-muted-foreground">至少需要 1 个问题。</p> : null}
      </div>
    )
  }

  const openai = content.openai ?? {}
  return (
    <div className="space-y-4">
      <label className="block space-y-1">
        <span className="text-sm font-medium">{account ? 'System Prompt' : 'System Prompt 补充文本'}</span>
        <Textarea
          aria-label="System Prompt"
          value={openai.systemPrompt ?? ''}
          {...fieldProps('systemPrompt')}
          onChange={(event) => onChange(updateOpenAI(content, { systemPrompt: event.target.value }))}
          rows={8}
        />
        {!account ? (
          <span className="block text-xs text-muted-foreground">补充层只追加文本，不覆盖账号默认内容。</span>
        ) : null}
      </label>
      {kind === 'chat_reply' && account ? (
        <label className="block space-y-1">
          <span className="text-sm font-medium">回复风格</span>
          <Textarea
            aria-label="回复风格"
            rows={2}
            placeholder="用简体中文、自然口语、简洁回复"
            value={openai.replyStyle ?? ''}
            {...fieldProps('replyStyle')}
            onChange={(event) => onChange(updateOpenAI(content, { replyStyle: event.target.value }))}
          />
        </label>
      ) : null}
      {kind === 'intent_participation' && provider === 'openai' && account ? (
        <label className="block space-y-1">
          <span className="text-sm font-medium">UserTemplate</span>
          <Input
            aria-label="UserTemplate"
            value={openai.userTemplate ?? ''}
            {...fieldProps('userTemplate')}
            onChange={(event) => onChange(updateOpenAI(content, { userTemplate: event.target.value }))}
          />
          <span className="block text-xs text-muted-foreground">
            必须且只能包含一次 {'{{state}}'} 与一次 {'{{question}}'}。
          </span>
        </label>
      ) : null}
      {kind === 'intent_participation' && provider === 'openai' ? (
        <p className="text-xs text-muted-foreground">
          ResponseShape 固定为 <code className="font-mono">single-letter</code>，不可修改。
        </p>
      ) : null}
      {kind === 'memory_extraction' && account ? (
        <label className="block space-y-1">
          <span className="text-sm font-medium">UserTemplate</span>
          <Input
            aria-label="UserTemplate"
            value={openai.userTemplate ?? ''}
            {...fieldProps('userTemplate')}
            onChange={(event) => onChange(updateOpenAI(content, { userTemplate: event.target.value }))}
          />
          <span className="block text-xs text-muted-foreground">
            必须且只能包含一次 {'{{messages_json}}'}。
          </span>
        </label>
      ) : null}
    </div>
  )
}
