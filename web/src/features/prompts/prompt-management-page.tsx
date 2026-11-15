import { useEffect, useMemo, useState } from 'react'
import { BookOpen, FileDiff, Pencil, Plus, RefreshCw, RotateCcw, Send, ShieldCheck, Undo2 } from 'lucide-react'
import { toast } from 'sonner'
import { ConfirmDialog } from '../../components/confirm-dialog'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { Input } from '../../components/ui/input'
import { Select } from '../../components/ui/select'
import { Skeleton } from '../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import type {
  ManagedPromptKind,
  ManagedPromptProvider,
  ManagedPromptScope,
  ManagedPromptVersion,
} from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { formatDateTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccountContext } from '../accounts/account-context'
import { KnowledgePreviewCard } from '../knowledge/components/context-preview'
import {
  PROMPT_KIND_IDS,
  PROMPT_KIND_LABELS,
  PROMPT_PROVIDER_LABELS,
  PROMPT_SCOPE_HINTS,
  PROMPT_SCOPE_LABELS,
  PROMPT_SOURCE_LABELS,
  PROMPT_STATUS_LABELS,
} from './prompt-content'
import { PromptContentView } from './prompt-content-editor'
import {
  CreateDraftDialog,
  PromptDiffSheet,
  PromptEditorSheet,
  PromptPreviewDialog,
} from './prompt-dialogs'
import {
  usePromptCatalog,
  usePromptDetail,
  usePromptEffective,
  usePublishPromptDraft,
  useRestorePromptBuiltin,
  useRollbackPromptVersion,
  type PromptDiffParams,
} from './prompt-queries'

const KINDS: ManagedPromptKind[] = ['chat_reply', 'intent_participation', 'memory_extraction']
const SCOPES: ManagedPromptScope[] = ['account', 'group', 'private']

const STATUS_VARIANTS: Record<ManagedPromptVersion['status'], 'success' | 'warning' | 'secondary'> = {
  published: 'success',
  draft: 'warning',
  archived: 'secondary',
}

type Selection = { kind: 'builtin' } | { kind: 'empty' } | { kind: 'version'; id: number }

function scopeSummary(
  scope: ManagedPromptScope,
  active: { version: number; source: string } | null,
): string {
  if (active) {
    return `v${active.version} · ${PROMPT_SOURCE_LABELS[active.source as keyof typeof PROMPT_SOURCE_LABELS] ?? active.source}`
  }
  return scope === 'account' ? '使用内置默认' : '无补充'
}

/** Three-layer Prompt management: builtin, active and editable drafts per account/scope/provider.
 * 三层 Prompt 管理：按账号/层级/provider 展示内置、实际生效与可编辑草稿。
 */
export function PromptManagementPage() {
  const { selectedBotId, selectedAccount } = useAccountContext()
  const [kind, setKind] = useState<ManagedPromptKind>('chat_reply')
  const [scope, setScope] = useState<ManagedPromptScope>('account')
  const [intentProvider, setIntentProvider] = useState<ManagedPromptProvider>('jev')
  const provider: ManagedPromptProvider = kind === 'intent_participation' ? intentProvider : 'openai'

  const catalog = usePromptCatalog(selectedBotId)
  const item = catalog.data?.find((entry) => entry.kind === kind && entry.provider === provider)
  const promptId = item?.promptId ?? PROMPT_KIND_IDS[kind]
  const detail = usePromptDetail({ botId: selectedBotId, promptId, scope, provider })

  const [selection, setSelection] = useState<Selection | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<ManagedPromptVersion | null>(null)
  const [diffParams, setDiffParams] = useState<PromptDiffParams | null>(null)
  const [preview, setPreview] = useState<{ versionId: number; label: string } | null>(null)
  const [effectiveTargetType, setEffectiveTargetType] = useState<'group' | 'private'>('group')
  const [effectiveTargetId, setEffectiveTargetId] = useState('')
  const [effectiveConversationId, setEffectiveConversationId] = useState('')
  const [effectiveRequested, setEffectiveRequested] = useState(false)
  const [confirm, setConfirm] = useState<
    { action: 'publish' | 'rollback' | 'restore'; version?: ManagedPromptVersion } | null
  >(null)

  const publish = usePublishPromptDraft()
  const rollback = useRollbackPromptVersion()
  const restore = useRestorePromptBuiltin()
  const effective = usePromptEffective({
    botId: selectedBotId,
    promptId,
    provider,
    targetType: effectiveTargetType,
    targetId: Number(effectiveTargetId) || undefined,
    conversationId: effectiveConversationId || undefined,
    enabled: effectiveRequested,
  })

  useEffect(() => {
    setEffectiveRequested(false)
  }, [promptId, provider, scope])

  useEffect(() => {
    if (!detail.data) {
      setSelection(null)
      return
    }
    setSelection(
      detail.data.active
        ? { kind: 'version', id: detail.data.active.id }
        : scope === 'account'
          ? { kind: 'builtin' }
          : { kind: 'empty' },
    )
  }, [detail.data, scope])

  const layers = useMemo(() => {
    const map = new Map(item?.layers.map((layer) => [layer.scopeType, layer]) ?? [])
    return SCOPES.map((value) => ({ scope: value, layer: map.get(value) }))
  }, [item])

  const activeVersion = detail.data?.active ?? null
  const drafts = detail.data?.versions.filter((version) => version.status === 'draft') ?? []
  const selectedVersion =
    selection?.kind === 'version'
      ? (detail.data?.versions.find((version) => version.id === selection.id) ??
        (activeVersion?.id === selection.id ? activeVersion : undefined))
      : undefined
  const displayedVersions = useMemo(() => {
    const versions = detail.data?.versions ?? []
    if (activeVersion && !versions.some((version) => version.id === activeVersion.id)) {
      return [activeVersion, ...versions]
    }
    return versions
  }, [detail.data?.versions, activeVersion])
  const pending = publish.isPending || rollback.isPending || restore.isPending

  if (selectedBotId === undefined) {
    return (
      <FeedbackState
        variant="empty"
        title="请选择已识别账号"
        description="Prompt 管理按账号隔离；请先在右上角选择已上报身份的账号。"
      />
    )
  }

  if (catalog.isError) {
    return (
      <FeedbackState
        variant="error"
        title="读取 Prompt 列表失败"
        error={catalog.error}
        onRetry={() => void catalog.refetch()}
      />
    )
  }

  const runConfirm = () => {
    if (!confirm) return
    if (confirm.action === 'publish' && confirm.version) {
      publish.mutate(confirm.version.id, {
        onSuccess: () => {
          toast.success(`已发布 Prompt v${confirm.version?.version}`)
          setConfirm(null)
        },
        onError: (error) => toast.error('发布失败', { description: describeError(error).description }),
      })
      return
    }
    if (confirm.action === 'rollback' && confirm.version) {
      rollback.mutate(
        { botId: selectedBotId, promptId, scope, provider, versionId: confirm.version.id },
        {
          onSuccess: () => {
            toast.success(`已回滚到 Prompt v${confirm.version?.version}`)
            setConfirm(null)
          },
          onError: (error) => toast.error('回滚失败', { description: describeError(error).description }),
        },
      )
      return
    }
    if (confirm.action === 'restore') {
      restore.mutate(
        { botId: selectedBotId, promptId, scope, provider },
        {
          onSuccess: () => {
            toast.success('已恢复内置 Prompt；本地历史版本保留')
            setConfirm(null)
          },
          onError: (error) => toast.error('恢复内置失败', { description: describeError(error).description }),
        },
      )
    }
  }

  const openDiff = (version: ManagedPromptVersion) => {
    setDiffParams({
      botId: selectedBotId,
      promptId,
      scope,
      provider,
      from: activeVersion ? 'active' : scope === 'account' ? 'builtin' : 'empty',
      to: 'version',
      toVersionId: version.id,
    })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="角色与提示词"
        description={`默认聊天 System 与默认抽取 System 可编辑，走 Prompt 版本管理；最终 Prompt 按层展示账号默认、群聊/私聊补充与服务端约束。当前账号：${selectedAccount?.nickname || selectedBotId}。不依赖 Langfuse；Langfuse 仅作为可选来源标签。`}
        actions={
          <Button variant="outline" size="sm" onClick={() => void catalog.refetch()}>
            <RefreshCw className={cn('size-3.5', catalog.isFetching && 'animate-spin')} aria-hidden />
            刷新
          </Button>
        }
      />

      <Card className="flex flex-row flex-wrap items-center gap-2 p-2">
        {KINDS.map((value) => (
          <Button
            key={value}
            type="button"
            size="sm"
            variant={kind === value ? 'secondary' : 'ghost'}
            onClick={() => {
              setKind(value)
              setScope('account')
              setEffectiveRequested(false)
            }}
          >
            <BookOpen className="size-3.5" aria-hidden />
            {PROMPT_KIND_LABELS[value]}
            <code className="ml-1 text-xs text-muted-foreground">{PROMPT_KIND_IDS[value]}</code>
          </Button>
        ))}
        {kind === 'intent_participation' ? (
          <label className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
            Provider
            <select
              aria-label="意图 Prompt Provider"
              className="h-8 rounded-md border border-border bg-surface px-2 text-xs"
              value={intentProvider}
              onChange={(event) => {
                setIntentProvider(event.target.value as ManagedPromptProvider)
                setEffectiveRequested(false)
              }}
            >
              <option value="jev">jev</option>
              <option value="openai">openai</option>
            </select>
          </label>
        ) : (
          <Badge variant="outline" className="ml-auto">{PROMPT_PROVIDER_LABELS.openai}</Badge>
        )}
      </Card>

      <div className="grid gap-2 sm:grid-cols-3">
        {layers.map(({ scope: layerScope, layer }) => (
          <button
            key={layerScope}
            type="button"
            onClick={() => {
              setScope(layerScope)
              setEffectiveRequested(false)
            }}
            className={cn(
              'rounded-lg border p-3 text-left transition-colors',
              scope === layerScope ? 'border-primary/40 bg-primary/5' : 'border-border hover:bg-secondary/40',
            )}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium">{PROMPT_SCOPE_LABELS[layerScope]}</span>
              {layer?.activeVersion ? <Badge variant="success">已发布</Badge> : <Badge variant="outline">{layerScope === 'account' ? '内置' : '空层'}</Badge>}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {scopeSummary(layerScope, layer?.activeVersion ? { version: layer.activeVersion, source: layer.activeSource } : null)}
            </p>
          </button>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">{PROMPT_SCOPE_HINTS[scope]} scope_id 固定为 0，不提供单群/单私聊独立 Prompt。</p>

      {detail.isLoading ? <Skeleton className="h-64 w-full" /> : null}
      {detail.isError ? (
        <FeedbackState
          variant="error"
          title="读取 Prompt 详情失败"
          error={detail.error}
          onRetry={() => void detail.refetch()}
        />
      ) : null}

      {detail.data ? (
        <>
          <div className="grid gap-2 sm:grid-cols-3">
            <Card className="p-3">
              <div className="text-xs text-muted-foreground">{scope === 'account' ? '内置默认' : '补充版本'}</div>
              <div className="mt-1 flex items-center gap-2 text-sm font-medium">
                {scope === 'account' ? (
                  <>
                    v{detail.data.builtin.version}
                    <Badge variant="secondary">内置</Badge>
                  </>
                ) : activeVersion ? (
                  <>
                    v{activeVersion.version}
                    <Badge variant="success">{PROMPT_SOURCE_LABELS[activeVersion.source]}</Badge>
                  </>
                ) : (
                  <span className="text-muted-foreground">无本地版本</span>
                )}
              </div>
            </Card>
            <Card className="p-3">
              <div className="text-xs text-muted-foreground">实际生效</div>
              <div className="mt-1 flex items-center gap-2 text-sm font-medium">
                {activeVersion ? (
                  <>
                    v{activeVersion.version}
                    <Badge variant="success">{PROMPT_SOURCE_LABELS[activeVersion.source]}</Badge>
                  </>
                ) : (
                  <span className="text-muted-foreground">{scope === 'account' ? '内置默认' : '无补充'}</span>
                )}
              </div>
            </Card>
            <Card className="p-3">
              <div className="text-xs text-muted-foreground">编辑草稿</div>
              <div className="mt-1 text-sm font-medium">
                {drafts.length > 0 ? `${drafts.length} 个草稿` : '暂无草稿'}
              </div>
            </Card>
          </div>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,380px)_minmax(0,1fr)]">
            <Card className="min-w-0 space-y-3 p-4">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-sm font-semibold">版本</h2>
                <Button size="sm" onClick={() => setCreateOpen(true)}>
                  <Plus className="size-3.5" aria-hidden />
                  新建草稿
                </Button>
              </div>
              <div className="overflow-hidden rounded-lg border border-border">
                <Table label="Prompt 版本列表">
                  <TableHeader>
                    <TableRow>
                      <TableHead>版本</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>来源</TableHead>
                      <TableHead>归档时间</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {scope === 'account' ? (
                      <TableRow
                        className={cn('cursor-pointer', selection?.kind === 'builtin' && 'bg-secondary/60')}
                        onClick={() => setSelection({ kind: 'builtin' })}
                      >
                        <TableCell className="font-mono">v{detail.data.builtin.version}</TableCell>
                        <TableCell><Badge variant="secondary">内置</Badge></TableCell>
                        <TableCell className="text-meta text-muted-foreground">内置</TableCell>
                        <TableCell className="text-meta text-muted-foreground">—</TableCell>
                      </TableRow>
                    ) : null}
                    {displayedVersions.map((version) => (
                      <TableRow
                        key={version.id}
                        className={cn('cursor-pointer', selection?.kind === 'version' && selection.id === version.id && 'bg-secondary/60')}
                        onClick={() => setSelection({ kind: 'version', id: version.id })}
                      >
                        <TableCell className="font-mono">
                          v{version.version}
                          {version.status === 'draft' ? <span className="ml-1 text-xs text-warning">草稿</span> : null}
                        </TableCell>
                        <TableCell>
                          <Badge variant={STATUS_VARIANTS[version.status]}>{PROMPT_STATUS_LABELS[version.status]}</Badge>
                        </TableCell>
                        <TableCell className="text-meta text-muted-foreground">{PROMPT_SOURCE_LABELS[version.source]}</TableCell>
                        <TableCell className="text-meta text-muted-foreground">
                          {version.status === 'archived'
                            ? version.archivedAt
                              ? formatDateTime(version.archivedAt)
                              : '未记录'
                            : '—'}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              {displayedVersions.length === 0 ? (
                <p className="text-xs text-muted-foreground">暂无本地版本，当前使用内置默认或空补充。可新建草稿后编辑。</p>
              ) : null}
            </Card>

            <Card className="min-w-0 space-y-4 p-4">
              {selection?.kind === 'builtin' ? (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold">内置默认 v{detail.data.builtin.version}</h2>
                    <Badge variant="secondary">只读</Badge>
                    <code className="max-w-full break-all text-xs text-muted-foreground">{detail.data.builtin.hash}</code>
                  </div>
                  <PromptContentView kind={detail.data.kind} provider={detail.data.provider} content={detail.data.builtin.content} />
                </>
              ) : selection?.kind === 'empty' ? (
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold">空补充</h2>
                    <Badge variant="outline">无本地版本</Badge>
                  </div>
                  <p className="text-sm text-muted-foreground">
                    该层暂无本地发布版本；运行时视为空补充，不向账号默认内容追加任何文本。可点击“新建草稿”创建空补充或指定版本。
                  </p>
                </div>
              ) : selectedVersion ? (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold">{selectedVersion.name || promptId} · v{selectedVersion.version}</h2>
                    <Badge variant={STATUS_VARIANTS[selectedVersion.status]}>{PROMPT_STATUS_LABELS[selectedVersion.status]}</Badge>
                    <Badge variant="outline">{PROMPT_SOURCE_LABELS[selectedVersion.source]}</Badge>
                    <code className="max-w-full break-all text-xs text-muted-foreground">{selectedVersion.hash}</code>
                  </div>
                  <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                    <span>创建：{formatDateTime(selectedVersion.createdAt)}</span>
                    <span>发布：{selectedVersion.publishedAt ? formatDateTime(selectedVersion.publishedAt) : '—'}</span>
                    <span>
                      归档：
                      {selectedVersion.status === 'archived'
                        ? selectedVersion.archivedAt
                          ? formatDateTime(selectedVersion.archivedAt)
                          : '未记录'
                        : '—'}
                    </span>
                  </div>
                  {selectedVersion.note ? <p className="text-xs text-muted-foreground">备注：{selectedVersion.note}</p> : null}
                  <div className="flex flex-wrap gap-2 border-b border-border pb-3">
                    {selectedVersion.status === 'draft' ? (
                      <>
                        <Button size="sm" variant="outline" onClick={() => setEditing(selectedVersion)}>
                          <Pencil className="size-3.5" aria-hidden />
                          编辑
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => openDiff(selectedVersion)}>
                          <FileDiff className="size-3.5" aria-hidden />
                          对比
                        </Button>
                        <Button size="sm" onClick={() => setConfirm({ action: 'publish', version: selectedVersion })}>
                          <Send className="size-3.5" aria-hidden />
                          发布
                        </Button>
                      </>
                    ) : null}
                    {selectedVersion.status === 'archived' && selectedVersion.publishedAt ? (
                      <Button size="sm" variant="outline" onClick={() => setConfirm({ action: 'rollback', version: selectedVersion })}>
                        <Undo2 className="size-3.5" aria-hidden />
                        回滚到此版本
                      </Button>
                    ) : null}
                    <Button size="sm" variant="ghost" onClick={() => setPreview({ versionId: selectedVersion.id, label: `v${selectedVersion.version}` })}>
                      预览
                    </Button>
                  </div>
                  <PromptContentView
                    kind={detail.data.kind}
                    provider={detail.data.provider}
                    content={selectedVersion.content}
                    scope={selectedVersion.scopeType}
                  />
                </>
              ) : (
                <p className="text-sm text-muted-foreground">选择一个版本查看内容。</p>
              )}
              {activeVersion ? (
                <div className="border-t border-border pt-3">
                  <Button size="sm" variant="ghost" onClick={() => setConfirm({ action: 'restore' })}>
                    <RotateCcw className="size-3.5" aria-hidden />
                    恢复内置默认
                  </Button>
                  <p className="mt-1 text-xs text-muted-foreground">归档当前本地发布版本，之后解析到内置版本；历史版本保留，不删除。</p>
                </div>
              ) : null}
            </Card>
          </div>

          <Card className="space-y-3 p-4">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-sm font-semibold">运行目标分层结果</h2>
              <Badge variant="outline">只读 · 不调用模型 · 不发送</Badge>
            </div>
            <p className="text-xs text-muted-foreground">
              选择真实群聊或私聊目标，查看账号默认、类型补充与服务端约束的最终 Prompt；不写入 Prompt 版本键，也不形成单会话覆盖。
            </p>
            <div className="grid gap-2 sm:grid-cols-4">
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">目标类型</span>
                <Select value={effectiveTargetType} onChange={(event) => setEffectiveTargetType(event.target.value as 'group' | 'private')} aria-label="生效目标类型">
                  <option value="group">群聊</option>
                  <option value="private">私聊</option>
                </Select>
              </label>
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">目标 ID（群号/QQ）</span>
                <Input value={effectiveTargetId} onChange={(event) => setEffectiveTargetId(event.target.value)} inputMode="numeric" aria-label="生效目标 ID" />
              </label>
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">会话 ID（可选）</span>
                <Input value={effectiveConversationId} onChange={(event) => setEffectiveConversationId(event.target.value)} aria-label="生效会话 ID" />
              </label>
              <div className="flex items-end">
                <Button size="sm" onClick={() => setEffectiveRequested(true)} disabled={!Number.isInteger(Number(effectiveTargetId)) || Number(effectiveTargetId) <= 0}>
                  查看分层结果
                </Button>
              </div>
            </div>
            {effective.isLoading ? <Skeleton className="h-24 w-full" /> : null}
            {effective.isError ? (
              <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                {describeError(effective.error).description}
              </p>
            ) : null}
            {effective.data ? (
              <div className="space-y-3 rounded-lg border border-border p-3">
                <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
                  <Badge variant="secondary">{effective.data.target.type === 'group' ? '群聊' : '私聊'} {effective.data.target.id}</Badge>
                  <span className="font-mono">Hash {effective.data.hash || '—'}</span>
                </div>
                <ul className="space-y-1 text-xs text-muted-foreground">
                  {effective.data.layers.map((layer) => (
                    <li key={`${layer.layer}-${layer.versionId ?? 0}`}>
                      {layer.layer}
                      {layer.version !== undefined ? ` · v${layer.version}` : ''}
                      {layer.source ? ` · ${layer.source}` : ''}
                      {layer.editable === false ? ' · 服务端固定' : ''}
                      {layer.summary ? ` · ${layer.summary}` : ''}
                    </li>
                  ))}
                </ul>
                <div>
                  <div className="text-xs text-muted-foreground">最终内容</div>
                  <div className="mt-2">
                    <PromptContentView kind={effective.data.kind} provider={effective.data.provider} content={effective.data.final.content} />
                  </div>
                </div>
              </div>
            ) : null}
          </Card>
        </>
      ) : null}

      <KnowledgePreviewCard botId={selectedBotId} />

      <CreateDraftDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        botId={selectedBotId}
        promptId={promptId}
        scope={scope}
        provider={provider}
        name={`${PROMPT_KIND_LABELS[kind]} · ${PROMPT_SCOPE_LABELS[scope]}`}
        hasActive={!!activeVersion}
        versions={detail.data?.versions ?? []}
        onCreated={(version) => {
          setSelection({ kind: 'version', id: version.id })
          setEditing(version)
        }}
      />
      <PromptEditorSheet
        open={editing !== null}
        onOpenChange={(open) => { if (!open) setEditing(null) }}
        version={editing}
        scope={scope}
      />
      <PromptDiffSheet
        open={diffParams !== null}
        onOpenChange={(open) => { if (!open) setDiffParams(null) }}
        params={diffParams}
      />
      <PromptPreviewDialog
        open={preview !== null}
        onOpenChange={(open) => { if (!open) setPreview(null) }}
        botId={selectedBotId}
        promptId={promptId}
        provider={provider}
        versionId={preview?.versionId ?? 0}
        versionLabel={preview?.label ?? ''}
      />
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => { if (!open) setConfirm(null) }}
        title={
          confirm?.action === 'publish'
            ? '发布 Prompt 版本？'
            : confirm?.action === 'rollback'
              ? '回滚 Prompt 版本？'
              : '恢复内置 Prompt？'
        }
        description={
          confirm?.action === 'publish'
            ? '发布后该版本在对应层生效，只影响之后的 Run；历史决策保留原 Prompt 快照。'
            : confirm?.action === 'rollback'
              ? '仅已归档且曾发布过的版本可回滚；当前发布版本会转为已归档。'
              : '将归档当前本地发布版本并恢复内置默认；历史版本保留，不删除。'
        }
        confirmLabel={confirm?.action === 'publish' ? '确认发布' : confirm?.action === 'rollback' ? '确认回滚' : '确认恢复'}
        pending={pending}
        onConfirm={runConfirm}
      />
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <ShieldCheck className="size-3.5" aria-hidden />
        Prompt 只影响实际使用方会消费的字段；工具权限、发送权限与 JSON 输出结构由服务端固定，不能由页面或 Prompt 覆盖。
      </p>
    </div>
  )
}
