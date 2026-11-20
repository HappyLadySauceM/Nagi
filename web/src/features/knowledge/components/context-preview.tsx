import { useState } from 'react'
import { Eye, ScanSearch } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { Input } from '../../../components/ui/input'
import { Select } from '../../../components/ui/select'
import { ApiError } from '../../../lib/api/client'
import type { KnowledgePreview } from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { formatDateTime } from '../../../lib/format'
import { cn } from '../../../lib/utils'
import { PromptContentView } from '../../prompts/prompt-content-editor'
import { useKnowledgePreview } from '../queries'

const PROMPT_NAMES: Record<string, string> = {
  'chat.reply': '聊天回复',
  'intent.participation': '意图判断',
  'memory.extraction': '记忆抽取',
}

const SKIP_REASON_LABELS: Record<string, string> = {
  memory_not_authorized: '未授权读取记忆（allow_memory_read=false）',
  history_not_authorized: '未授权读取历史（allow_history_read=false）',
  scope_disabled: '作用域未启用或处于只观察模式',
  conversation_not_found: '会话不存在或不属于该目标',
  conversation_target_mismatch: '会话 ID 与目标不匹配',
  conversation_unavailable: '会话服务不可用，无法校验会话',
}

function reasonLabel(reason: string): string {
  if (!reason) {
    return '未提供原因'
  }
  return SKIP_REASON_LABELS[reason] ?? reason
}

function sourceLabel(source: string): string {
  switch (source) {
    case 'builtin':
      return '内置'
    case 'local':
      return '本地'
    case 'langfuse':
      return 'Langfuse'
    default:
      return source || '未知'
  }
}

function SkippedLayer({ label, reason }: { label: string; reason: string }) {
  return (
    <div
      role="status"
      className="rounded-lg border border-warning/30 bg-warning/10 px-3 py-2 text-xs leading-relaxed text-warning"
    >
      <span className="font-medium">{label}已跳过</span>
      <span className="text-warning/90">：{reasonLabel(reason)}</span>
    </div>
  )
}

/**
 * Read-only knowledge context preview for the role-and-prompt entry. Example
 * mode uses synthetic data; real mode shows every skipped layer instead of
 * treating an HTTP 200 as a complete preview.
 * 角色与提示词入口的只读知识上下文预览：示例模式用合成数据；真实模式显式展示
 * 每个被跳过的层，不把 HTTP 200 当作完整预览成功。
 */
export function KnowledgePreviewCard({ botId }: { botId?: number }) {
  const [targetType, setTargetType] = useState<'group' | 'private'>('group')
  const [targetId, setTargetId] = useState('')
  const [speakerId, setSpeakerId] = useState('')
  const [conversationId, setConversationId] = useState('')
  const [query, setQuery] = useState('喜欢喝什么')
  const [subjectType, setSubjectType] = useState<'unknown' | 'user' | 'group'>('unknown')
  const [subjectId, setSubjectId] = useState('')
  const [validation, setValidation] = useState('')
  const preview = useKnowledgePreview()

  const run = (mode: 'example' | 'real', allowRewrite = false) => {
    setValidation('')
    if (botId === undefined) {
      setValidation('机器人账号尚未识别，无法发起预览。')
      return
    }
    const numericTargetId = Number(targetId)
    if (mode === 'real' && (!Number.isInteger(numericTargetId) || numericTargetId <= 0)) {
      setValidation('真实预览必须选择群聊或私聊，并填写大于 0 的目标 ID（群号或 QQ）。')
      return
    }
    const selectedSubject = mode === 'real' ? subjectType : 'unknown'
    const selectedId = selectedSubject === 'group' ? String(numericTargetId) : subjectId.trim()
    if (selectedSubject === 'user' && !/^[1-9][0-9]*$/.test(selectedId)) {
      setValidation('请填写个人主体的 QQ 号。')
      return
    }
    preview.mutate({
      botId,
      mode,
      targetType,
      targetId: mode === 'real' ? numericTargetId : undefined,
      conversationId: mode === 'real' ? conversationId.trim() : '',
      query: query.trim(),
      speakerId: mode === 'real' && targetType === 'group' ? speakerId.trim() : undefined,
      allowRewrite,
      subjectType: selectedSubject,
      subjectId: selectedSubject === 'unknown' ? undefined : selectedId,
    })
  }

  const data = preview.isError ? undefined : preview.data

  return (
    <Card className="space-y-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Eye className="size-4 text-muted-foreground" aria-hidden />
        <h2 className="text-sm font-semibold">知识上下文预览</h2>
        <Badge variant="outline">只读 · 不发送</Badge>
      </div>
      <p className="text-xs leading-relaxed text-muted-foreground">
        示例预览只使用合成消息与记忆，不读取真实内容；真实上下文预览按当前账号、目标作用域与授权读取。
        接口返回 200 时仍可能跳过记忆或历史层，页面会明确展示跳过原因，不能视为完整预览成功。
        默认聊天 System / 默认抽取 System 可在上方「聊天回复」与「记忆抽取」Prompt 管理中编辑；角色提示词属于行为配置，不写入共享记忆。
      </p>

      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        <label className="space-y-1 text-xs">
          <span className="text-muted-foreground">目标类型</span>
          <Select
            aria-label="预览目标类型"
            value={targetType}
            onChange={(event) => {
              const kind = event.target.value as 'group' | 'private'
              setTargetType(kind)
              if (kind === 'private' && subjectType === 'group') setSubjectType('unknown')
            }}
          >
            <option value="group">群聊</option>
            <option value="private">私聊</option>
          </Select>
        </label>
        <label className="space-y-1 text-xs">
          <span className="text-muted-foreground">目标 ID（群号 / QQ）</span>
          <Input
            aria-label="预览目标 ID"
            inputMode="numeric"
            value={targetId}
            onChange={(event) => setTargetId(event.target.value)}
            placeholder="真实预览必填"
          />
        </label>
        <label className="space-y-1 text-xs">
          <span className="text-muted-foreground">会话 ID（可选）</span>
          <Input
            aria-label="预览会话 ID"
            value={conversationId}
            onChange={(event) => setConversationId(event.target.value)}
            placeholder="留空由目标解析"
          />
        </label>
        <label className="space-y-1 text-xs">
          <span className="text-muted-foreground">记忆检索关键词</span>
          <Input
            aria-label="预览记忆关键词"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="例如：喜欢喝什么"
          />
        </label>
      </div>

      <div className="grid gap-2 sm:grid-cols-2">
        {targetType === 'group' ? (
          <label className="space-y-1 text-xs">
            <span className="text-muted-foreground">发言人 QQ 号（可选）</span>
            <Input aria-label="发言人 QQ 号" inputMode="numeric" value={speakerId} onChange={(event) => setSpeakerId(event.target.value)} />
          </label>
        ) : null}
        <label className="space-y-1 text-xs">
          <span className="text-muted-foreground">记忆主体</span>
          <Select aria-label="记忆主体" value={subjectType} onChange={(event) => setSubjectType(event.target.value as typeof subjectType)}>
            <option value="unknown">未指定</option>
            <option value="user">个人</option>
            {targetType === 'group' ? <option value="group">当前群</option> : null}
          </Select>
        </label>
        {subjectType === 'user' ? (
          <label className="space-y-1 text-xs">
            <span className="text-muted-foreground">主体 QQ 号</span>
            <Input aria-label="主体 QQ 号" inputMode="numeric" value={subjectId} onChange={(event) => setSubjectId(event.target.value)} />
          </label>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" onClick={() => run('example')} disabled={preview.isPending}>
          <ScanSearch className="size-3.5" aria-hidden />
          示例预览
        </Button>
        <Button size="sm" onClick={() => run('real')} disabled={preview.isPending}>
          <Eye className="size-3.5" aria-hidden />
          真实上下文预览
        </Button>
        <Button variant="outline" size="sm" onClick={() => run('real', true)} disabled={preview.isPending} title="原文无命中时调用当前回复模型">
          同义召回预览
        </Button>
        {preview.isPending ? <span className="text-xs text-muted-foreground">读取中…</span> : null}
      </div>
      {validation ? (
        <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {validation}
        </p>
      ) : null}
      {preview.isError ? (
        <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <div className="font-medium">
            {preview.error instanceof ApiError && preview.error.status === 400
              ? '真实预览请求被拒绝（HTTP 400）'
              : '预览失败'}
          </div>
          <div className="mt-1">
            {preview.error instanceof ApiError && preview.error.status === 400
              ? `必须提供 bot_id 与有效的 target_type / target_id。${preview.error.message}`
              : describeError(preview.error).description}
          </div>
        </div>
      ) : null}

      {data ? <PreviewResult data={data} /> : null}
    </Card>
  )
}

function PreviewResult({ data }: { data: KnowledgePreview }) {
  const skipped = [
    data.tools.skipped ? `工具约束（${reasonLabel(data.tools.reason)}）` : '',
    data.memory.skipped ? `记忆（${reasonLabel(data.memory.reason)}）` : '',
    data.history.skipped ? `历史（${reasonLabel(data.history.reason)}）` : '',
  ].filter(Boolean)
  const incomplete = data.previewMode === 'real' && skipped.length > 0
  const emptyConversation =
    data.previewMode === 'real' && data.target.conversationId === '' && (data.memory.skipped || data.history.skipped)

  return (
    <div className="space-y-3 rounded-lg border border-border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={data.previewMode === 'example' ? 'secondary' : 'success'}>
          {data.previewMode === 'example' ? '示例预览（合成数据）' : '真实上下文预览'}
        </Badge>
        <Badge variant="outline">
          目标：{data.target.type === 'group' ? '群聊' : '私聊'} {data.target.id}
        </Badge>
        <Badge variant={data.target.conversationId ? 'secondary' : 'warning'}>
          会话：{data.target.conversationId || '未绑定'}
        </Badge>
      </div>

      {data.previewMode === 'example' ? (
        <p className="rounded-md border border-border bg-secondary/30 px-3 py-2 text-xs text-muted-foreground">
          示例预览使用合成消息与合成记忆，不读取真实内容，不返回真实来源 ID。
        </p>
      ) : incomplete ? (
        <div role="status" className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs leading-relaxed text-warning">
          <div className="font-medium">接口返回 200，但这不是完整预览</div>
          <div className="mt-1">以下层被跳过：{skipped.join('；')}。</div>
          {emptyConversation ? (
            <div className="mt-1">目标会话未校验通过，Target.ConversationID 为空，记忆与历史均被跳过。</div>
          ) : null}
        </div>
      ) : (
        <p className="rounded-md border border-success/30 bg-success/10 px-3 py-2 text-xs text-success">
          真实上下文预览：工具约束、记忆与历史均按当前授权读取并通过。
        </p>
      )}

      <div className="grid gap-3 lg:grid-cols-2">
        <section className="space-y-2">
          <h3 className="text-xs font-semibold text-foreground">默认聊天 System</h3>
          <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-lg border border-border/70 bg-secondary/20 p-3 font-mono text-xs leading-relaxed text-muted-foreground">
            {data.defaults.chatSystem || '—'}
          </pre>
        </section>
        <section className="space-y-2">
          <h3 className="text-xs font-semibold text-foreground">默认抽取 System</h3>
          <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-lg border border-border/70 bg-secondary/20 p-3 font-mono text-xs leading-relaxed text-muted-foreground">
            {data.defaults.extractionSystem || '—'}
          </pre>
        </section>
      </div>

      <section className="space-y-2">
        <h3 className="text-xs font-semibold text-foreground">最终 Prompt 分层</h3>
        <div className="space-y-2">
          {Object.values(data.prompts).map((prompt) => (
            <div key={prompt.promptId} className="rounded-lg border border-border/70 p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">{PROMPT_NAMES[prompt.promptId] ?? prompt.name}</span>
                <Badge variant="secondary">{prompt.kind}</Badge>
                <Badge variant="success">v{prompt.version}</Badge>
                <Badge variant="outline">{sourceLabel(prompt.source)}</Badge>
              </div>
              <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
                {prompt.layers.map((layer, index) => (
                  <li key={`${layer.layer}-${layer.versionId ?? index}`}>
                    <span className="font-mono text-foreground">{layer.layer}</span>
                    {layer.version !== undefined ? ` · v${layer.version}` : ''}
                    {layer.sourceRef ? ` · ${layer.sourceRef}` : layer.source ? ` · ${sourceLabel(layer.source)}` : ''}
                    {layer.layer === 'server_constraints' ? ' · 服务端固定' : ''}
                    {layer.summary && layer.layer !== 'server_constraints' ? ` · ${layer.summary}` : ''}
                  </li>
                ))}
              </ul>
              <p className="mt-1 break-all font-mono text-meta text-muted-foreground">
                模板哈希 {prompt.finalHash || prompt.hash || '—'}
              </p>
              {prompt.kind === 'intent_participation' && prompt.provider === 'jev' ? (
                <details className="mt-2 text-xs">
                  <summary className="cursor-pointer">渲染后的 SystemOne 问题</summary>
                  <PromptContentView kind={prompt.kind} provider={prompt.provider} content={prompt.content} />
                  {prompt.renderedHash ? (
                    <p className="mt-1 break-all font-mono text-meta text-muted-foreground">渲染哈希 {prompt.renderedHash}</p>
                  ) : null}
                  {!prompt.variables.sender_id && Object.values(prompt.content.systemOne?.questions ?? {}).some((question) => question.instructions.includes('{{sender_id}}')) ? (
                    <p className="mt-1 text-meta text-warning">填写发言人 QQ 号后可对照实际渲染哈希。</p>
                  ) : null}
                </details>
              ) : null}
              {prompt.kind === 'chat_reply' && prompt.content.openai ? (
                <details className="mt-2 text-xs">
                  <summary className="cursor-pointer">渲染后的系统提示词</summary>
                  <pre className="mt-2 max-h-56 overflow-auto whitespace-pre-wrap rounded-lg border border-border p-3 font-mono text-muted-foreground">
                    {prompt.content.openai.systemPrompt}
                  </pre>
                  <p className="mt-1 break-all font-mono text-meta text-muted-foreground">
                    预览哈希 {prompt.renderedHash || '—'}
                  </p>
                  <p className="mt-1 text-meta text-muted-foreground">实际回复还会附加本轮召回的记忆。</p>
                  {!prompt.variables.sender_id && prompt.content.openai.systemPrompt?.includes('{{sender_id}}') ? (
                    <p className="mt-1 text-meta text-warning">填写发言人 QQ 号后可对照实际渲染哈希。</p>
                  ) : null}
                </details>
              ) : null}
            </div>
          ))}
        </div>
      </section>

      <section className="space-y-2">
        <h3 className="text-xs font-semibold text-foreground">工具约束</h3>
        {data.tools.skipped ? (
          <SkippedLayer label="工具约束" reason={data.tools.reason} />
        ) : (
          <div className="space-y-2 rounded-lg border border-border/70 p-3 text-xs">
            <div className="flex flex-wrap gap-2">
              <Badge variant={data.tools.policy.enabled ? 'success' : 'warning'}>
                作用域{data.tools.policy.enabled ? '已启用' : '未启用'}
              </Badge>
              <Badge variant="outline">回复模式 {data.tools.policy.replyMode || '—'}</Badge>
              <Badge variant={data.tools.policy.storeMessages ? 'secondary' : 'outline'}>
                消息存储{data.tools.policy.storeMessages ? '开' : '关'}
              </Badge>
              <Badge variant={data.tools.policy.allowMemoryRead ? 'secondary' : 'outline'}>
                记忆读取{data.tools.policy.allowMemoryRead ? '开' : '关'}
              </Badge>
              <Badge variant={data.tools.policy.allowHistoryRead ? 'secondary' : 'outline'}>
                历史读取{data.tools.policy.allowHistoryRead ? '开' : '关'}
              </Badge>
            </div>
            <div className="text-muted-foreground">
              实际权限：
              {data.tools.effectivePermissions.length > 0 ? (
                <span className="ml-1 inline-flex flex-wrap gap-1">
                  {data.tools.effectivePermissions.map((permission) => (
                    <code key={permission} className="rounded bg-secondary px-1.5 py-0.5 text-foreground">
                      {permission}
                    </code>
                  ))}
                </span>
              ) : (
                ' 无'
              )}
            </div>
            {Object.keys(data.tools.explicitOverrides).length > 0 ? (
              <div className="text-muted-foreground">
                显式覆盖：
                {Object.entries(data.tools.explicitOverrides)
                  .map(([permission, granted]) => `${permission}=${granted ? '允许' : '拒绝'}`)
                  .join('，')}
              </div>
            ) : null}
          </div>
        )}
      </section>

      <section className="space-y-2">
        <h3 className="text-xs font-semibold text-foreground">授权记忆</h3>
        {data.memory.source ? (
          <Badge variant="outline">{data.memory.source === 'rewrite' ? '改写命中' : '原文命中'}</Badge>
        ) : null}
        {data.memory.skipped ? (
          <SkippedLayer label="记忆" reason={data.memory.reason} />
        ) : data.memory.items.length === 0 ? (
          <p className="text-xs text-muted-foreground">当前条件下没有可读取的记忆。</p>
        ) : (
          <ul className="space-y-2">
            {data.memory.items.map((memory) => (
              <li key={memory.id} className="rounded-lg border border-border/70 p-3 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="secondary">{memory.visibility}</Badge>
                  {memory.subjectType ? (
                    <Badge variant="outline">
                      主体 {memory.subjectType}
                      {memory.subjectId ? `:${memory.subjectId}` : ''}
                    </Badge>
                  ) : null}
                  {memory.status ? <Badge variant="outline">{memory.status}</Badge> : null}
                </div>
                <p className="mt-1 leading-relaxed text-foreground">{memory.content}</p>
                {memory.updatedAt ? (
                  <p className="mt-1 text-meta text-muted-foreground">{formatDateTime(memory.updatedAt)}</p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-2">
        <h3 className="text-xs font-semibold text-foreground">授权历史</h3>
        {data.history.skipped ? (
          <SkippedLayer label="历史" reason={data.history.reason} />
        ) : data.history.messages.length === 0 ? (
          <p className="text-xs text-muted-foreground">当前条件下没有可读取的历史消息。</p>
        ) : (
          <ul className="space-y-1 rounded-lg border border-border/70 p-3 text-xs">
            {data.history.messages.map((message, index) => (
              <li key={`${message.id}-${message.messageId}-${index}`} className={cn('flex gap-2', message.isBot && 'text-primary')}>
                <span className="shrink-0 font-mono text-meta text-muted-foreground">
                  {message.isBot ? 'BOT' : message.senderId}
                </span>
                <span className="min-w-0 break-words text-foreground">{message.text}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
