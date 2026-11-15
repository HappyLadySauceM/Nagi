import { Badge } from '../../components/ui/badge'
import { Input } from '../../components/ui/input'
import { Textarea } from '../../components/ui/textarea'
import type {
  ManagedPromptContent,
  ManagedPromptKind,
  ManagedPromptProvider,
  ManagedPromptQuestion,
  ManagedPromptScope,
} from '../../lib/api/contracts'
import { JEV_QUESTIONS } from './prompt-content'

/** Read-only render of one Prompt content, grouped by the fixed server schema.
 * 只读展示一份 Prompt 内容，按服务端固定 schema 分组。
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
    return (
      <div className="space-y-3">
        {JEV_QUESTIONS.map((question) => {
          const item = questions[question.key]
          return (
            <div key={question.key} className="rounded-md border border-border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">{question.label}</span>
                <code className="text-xs text-muted-foreground">{question.key}</code>
                <Badge variant="outline">{question.type === 'choice' ? 'choice' : 'noul'}</Badge>
              </div>
              <p className="mt-2 whitespace-pre-wrap break-words text-sm">
                {item?.instructions || '未记录'}
              </p>
              {question.criteria && scope === 'account' ? (
                <dl className="mt-2 grid gap-1 text-xs text-muted-foreground sm:grid-cols-2">
                  {Object.entries(question.criteria).map(([key, label]) => (
                    <div key={key} className="flex gap-2">
                      <dt className="font-mono text-foreground/80">{key}</dt>
                      <dd>{item?.criteria?.[key] || label}</dd>
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

interface PromptEditorProps {
  kind: ManagedPromptKind
  provider: ManagedPromptProvider
  scope: ManagedPromptScope
  content: ManagedPromptContent
  onChange: (content: ManagedPromptContent) => void
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
export function PromptEditor({ kind, provider, scope, content, onChange }: PromptEditorProps) {
  const account = scope === 'account'
  if (kind === 'intent_participation' && provider === 'jev') {
    return (
      <div className="space-y-4">
        <p className="text-xs text-muted-foreground">
          题目 key、Type 与 Criteria 的输出 key 由服务端固定；页面只编辑 Instructions 与描述。补充层只能追加说明，不能新增题目或修改 Type/Criteria。
        </p>
        {JEV_QUESTIONS.map((question) => {
          const item = content.systemOne?.questions?.[question.key]
          return (
            <div key={question.key} className="space-y-2 rounded-md border border-border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">{question.label}</span>
                <code className="text-xs text-muted-foreground">{question.key}</code>
                <Badge variant="outline">{question.type}</Badge>
              </div>
              <Textarea
                aria-label={`${question.label} Instructions`}
                value={item?.instructions ?? ''}
                onChange={(event) =>
                  onChange(updateQuestion(content, question.key, { instructions: event.target.value }))
                }
                placeholder={account ? '编辑题目描述' : '补充说明（追加到基础描述后）'}
              />
              {account && question.criteria ? (
                <div className="grid gap-2 sm:grid-cols-2">
                  {Object.entries(question.criteria).map(([key, label]) => (
                    <label key={key} className="space-y-1 text-xs text-muted-foreground">
                      <span>
                        <code className="font-mono text-foreground/80">{key}</code> 描述
                      </span>
                      <Input
                        aria-label={`${question.label} ${key} 描述`}
                        value={item?.criteria?.[key] ?? label}
                        onChange={(event) =>
                          onChange(updateCriterion(content, question.key, key, event.target.value))
                        }
                      />
                    </label>
                  ))}
                </div>
              ) : null}
            </div>
          )
        })}
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
          onChange={(event) => onChange(updateOpenAI(content, { systemPrompt: event.target.value }))}
          rows={8}
        />
        {!account ? (
          <span className="block text-xs text-muted-foreground">补充层只追加文本，不覆盖账号默认内容。</span>
        ) : null}
      </label>
      {kind === 'intent_participation' && provider === 'openai' && account ? (
        <label className="block space-y-1">
          <span className="text-sm font-medium">UserTemplate</span>
          <Input
            aria-label="UserTemplate"
            value={openai.userTemplate ?? ''}
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
