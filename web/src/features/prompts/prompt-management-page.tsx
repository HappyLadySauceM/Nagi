import { useEffect, useMemo, useRef, useState } from 'react'
import {
  BookOpen,
  History,
  Layers,
  Pencil,
  RefreshCw,
  RotateCcw,
  Save,
  Send,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'
import { ConfirmDialog } from '../../components/confirm-dialog'
import { FeedbackState } from '../../components/feedback-state'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { Input } from '../../components/ui/input'
import { Select } from '../../components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../components/ui/sheet'
import { Skeleton } from '../../components/ui/skeleton'
import type {
  ManagedPromptContent,
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
import { useSystemSettings } from '../settings/queries'
import {
  PROMPT_KIND_IDS,
  PROMPT_KIND_LABELS,
  PROMPT_SCOPE_LABELS,
  PROMPT_SOURCE_LABELS,
  normaliseContentForScope,
  promptContentToWire,
  validatePromptContent,
} from './prompt-content'
import { PromptContentView, PromptEditor, type InsertToken } from './prompt-content-editor'
import { PromptHistorySheet } from './prompt-history-sheet'
import { PromptVariablesPanel } from './prompt-variables-panel'
import { CreateDraftDialog, PromptPreviewDialog } from './prompt-dialogs'
import {
  useCreatePromptDraft,
  useDeletePromptVersion,
  useImportLangfusePrompt,
  usePromptCatalog,
  usePromptDetail,
  usePromptEffective,
  usePublishPromptDraft,
  useRestorePromptBuiltin,
  useRollbackPromptVersion,
  useUpdatePromptDraft,
} from './prompt-queries'

const KINDS: ManagedPromptKind[] = ['chat_reply', 'memory_extraction', 'memory_recall_rewrite']
const SCOPES: ManagedPromptScope[] = ['account', 'group', 'private']

function scopeSummary(
  scope: ManagedPromptScope,
  active: { version: number; source: string } | null,
): string {
  if (active) {
    return `v${active.version} · ${PROMPT_SOURCE_LABELS[active.source as keyof typeof PROMPT_SOURCE_LABELS] ?? active.source}`
  }
  return scope === 'account' ? '使用内置默认' : '无补充'
}

/**
 * Prompt management: left column picks the account/group/private layer, the
 * right column edits that layer's draft directly and opens history/effective/
 * knowledge preview in drawers.
 * Prompt 管理：左栏选择账号/群聊/私聊层，右栏直接编辑该层草稿，并以抽屉展示历史、
 * 分层结果与知识上下文预览。
 */
export function PromptManagementPage({
  kinds = KINDS,
  initialKind = kinds[0],
}: {
  kinds?: ManagedPromptKind[]
  initialKind?: ManagedPromptKind
} = {}) {
  const { selectedBotId } = useAccountContext()
  const [kind, setKind] = useState<ManagedPromptKind>(initialKind)
  const [scope, setScope] = useState<ManagedPromptScope>('account')
  const provider: ManagedPromptProvider = kind === 'intent_participation' ? 'jev' : 'openai'

  const catalog = usePromptCatalog(selectedBotId)
  const item = catalog.data?.find((entry) => entry.kind === kind && entry.provider === provider)
  const promptId = item?.promptId ?? PROMPT_KIND_IDS[kind]
  const detail = usePromptDetail({ botId: selectedBotId, promptId, scope, provider })
  const settingsQuery = useSystemSettings()
  const extraVariables = settingsQuery.data?.settings.role.variables.map((variable) => variable.name) ?? []

  const activeVersion = detail.data?.active ?? null
  const [selectedDraftId, setSelectedDraftId] = useState<number>()
  const draft = useMemo(() => {
    const versions = (detail.data?.versions ?? []).filter((version) => version.status === 'draft')
    return versions.find((version) => version.id === selectedDraftId) ?? versions.sort((left, right) => right.version - left.version)[0] ?? null
  }, [detail.data?.versions, selectedDraftId])

  const [draftEdit, setDraftEdit] = useState<{ id: number; content: ManagedPromptContent } | null>(null)
  const draftContent = draftEdit && draftEdit.id === draft?.id ? draftEdit.content : null
  const [errors, setErrors] = useState<string[]>([])
  const insertRef = useRef<InsertToken | null>(null)

  const [historyOpen, setHistoryOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [preview, setPreview] = useState<{ versionId: number; label: string } | null>(null)
  const [effectiveOpen, setEffectiveOpen] = useState(false)
  const [knowledgeOpen, setKnowledgeOpen] = useState(false)
  const [effectiveTargetType, setEffectiveTargetType] = useState<'group' | 'private'>('group')
  const [effectiveTargetId, setEffectiveTargetId] = useState('')
  const [effectiveConversationId, setEffectiveConversationId] = useState('')
  const [effectiveSenderId, setEffectiveSenderId] = useState('')
  const [importOpen, setImportOpen] = useState(false)
  const [remoteName, setRemoteName] = useState('')
  const [remoteVersion, setRemoteVersion] = useState('')
  const [effectiveRequested, setEffectiveRequested] = useState(false)
  const [confirm, setConfirm] = useState<
    { action: 'publish' | 'rollback' | 'restore' | 'delete'; version?: ManagedPromptVersion } | null
  >(null)

  const create = useCreatePromptDraft()
  const importPrompt = useImportLangfusePrompt()
  const update = useUpdatePromptDraft()
  const publish = usePublishPromptDraft()
  const rollback = useRollbackPromptVersion()
  const restore = useRestorePromptBuiltin()
  const remove = useDeletePromptVersion()
  const effective = usePromptEffective({
    botId: selectedBotId,
    promptId,
    provider,
    targetType: effectiveTargetType,
    targetId: Number(effectiveTargetId) || undefined,
    senderId: effectiveTargetType === 'group' ? Number(effectiveSenderId) || undefined : undefined,
    conversationId: effectiveConversationId || undefined,
    enabled: effectiveRequested,
  })

  const importVersion = () => {
    if (selectedBotId === undefined) return
    importPrompt.mutate({
      botId: selectedBotId, promptId, provider, scope,
      name: remoteName.trim(), version: Number(remoteVersion),
    }, {
      onSuccess: (record) => {
        setSelectedDraftId(record.id)
        toast.success(`已取得本地版本 v${record.version}`)
        setImportOpen(false)
      },
    })
  }

  useEffect(() => {
    setEffectiveRequested(false)
  }, [promptId, provider, scope])

  useEffect(() => {
    if (!draft) {
      setDraftEdit(null)
      setErrors([])
      return
    }
    setDraftEdit({ id: draft.id, content: normaliseContentForScope(kind, provider, scope, draft.content) })
    setErrors([])
    // Load a fresh draft only when its id, layer or kind changes.
    // 仅在草稿 id、作用域或 kind 变化时重新载入。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft?.id, scope, kind])

  const layers = useMemo(() => {
    const map = new Map(item?.layers.map((layer) => [layer.scopeType, layer]) ?? [])
    return SCOPES.map((value) => ({ scope: value, layer: map.get(value) }))
  }, [item])

  const pending =
    create.isPending || update.isPending || publish.isPending || rollback.isPending || restore.isPending || remove.isPending
  const intentOnly = kinds.length > 0 && kinds.every((value) => value === 'intent_participation')

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

  const startEdit = () => {
    const base = activeVersion ? 'active' : scope === 'account' ? 'builtin' : 'empty'
    create.mutate(
      {
        botId: selectedBotId,
        promptId,
        scope,
        provider,
        name: `${PROMPT_KIND_LABELS[kind]} · ${PROMPT_SCOPE_LABELS[scope]}`,
        base,
        note: '',
      },
      {
        onSuccess: (record) => {
          setSelectedDraftId(record.id)
          toast.success('已创建 Prompt 草稿')
        },
        onError: (error) => toast.error('创建草稿失败', { description: describeError(error).description }),
      },
    )
  }

  const saveDraft = () => {
    if (!draft || !draftContent) {
      return
    }
    const clientErrors = validatePromptContent(kind, provider, scope, draftContent, extraVariables)
    setErrors(clientErrors)
    if (clientErrors.length > 0) {
      return
    }
    update.mutate(
      { versionId: draft.id, content: promptContentToWire(draftContent, scope), note: draft.note },
      {
        onSuccess: () => toast.success('Prompt 草稿已保存'),
        onError: (error) => toast.error('保存失败', { description: describeError(error).description }),
      },
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
      return
    }
    if (confirm.action === 'delete' && confirm.version) {
      remove.mutate(
        { botId: selectedBotId, versionId: confirm.version.id },
        {
          onSuccess: () => {
            toast.success(`已删除 Prompt v${confirm.version?.version}`)
            setConfirm(null)
          },
          onError: (error) => toast.error('删除失败', { description: describeError(error).description }),
        },
      )
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1">
          {kinds.length > 1
            ? kinds.map((value) => (
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
                </Button>
              ))
            : null}
        </div>
        <Button variant="outline" size="sm" onClick={() => void catalog.refetch()}>
          <RefreshCw className={cn('size-3.5', catalog.isFetching && 'animate-spin')} aria-hidden />
          刷新
        </Button>
      </div>

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
        <div className="grid gap-3 md:grid-cols-[minmax(0,240px)_minmax(0,1fr)]">
          <div className="flex flex-col gap-2">
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
                  {layer?.activeVersion ? (
                    <Badge variant="success">已发布</Badge>
                  ) : (
                    <Badge variant="outline">{layerScope === 'account' ? '内置' : '空层'}</Badge>
                  )}
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {scopeSummary(layerScope, layer?.activeVersion ? { version: layer.activeVersion, source: layer.activeSource } : null)}
                </p>
              </button>
            ))}
          </div>

          <div className="min-w-0 space-y-3">
            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setHistoryOpen(true)}>
                <History className="size-3.5" aria-hidden />
                历史版本
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={() => setEffectiveOpen(true)}>
                <Layers className="size-3.5" aria-hidden />
                分层结果
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={() => {
                importPrompt.reset()
                setRemoteName(promptId)
                setRemoteVersion('')
                setImportOpen((open) => !open)
              }}>
                从 Langfuse 同步
              </Button>
              {intentOnly ? null : (
                <Button type="button" variant="outline" size="sm" onClick={() => setKnowledgeOpen(true)}>
                  知识上下文预览
                </Button>
              )}
            </div>

            {importOpen ? (
              <form className="space-y-2 rounded-lg border border-border p-3" onSubmit={(event) => {
                event.preventDefault()
                importVersion()
              }}>
                <p className="text-xs text-muted-foreground">同步到当前层级的草稿。发布后对话才会生效。</p>
                <div className="flex flex-wrap items-end gap-2">
                  <label className="space-y-1 text-xs">
                    <span>Langfuse 提示词名称</span>
                    <Input aria-label="Langfuse 提示词名称" value={remoteName} onChange={(event) => setRemoteName(event.target.value)} required />
                  </label>
                  <label className="space-y-1 text-xs">
                    <span>版本</span>
                    <Input aria-label="Langfuse 版本" type="number" min={1} step={1} value={remoteVersion} onChange={(event) => setRemoteVersion(event.target.value)} required />
                  </label>
                  <Button type="submit" size="sm" disabled={importPrompt.isPending || selectedBotId === undefined}>同步为草稿</Button>
                </div>
                {importPrompt.isError ? <p role="alert" className="text-xs text-destructive">{describeError(importPrompt.error).description}</p> : null}
              </form>
            ) : null}

            <PromptVariablesPanel
              kind={kind}
              scope={scope}
              onInsertToken={(token) => insertRef.current?.(token) ?? false}
            />

            <Card className="min-w-0 space-y-3 p-4">
              {draft && draftContent ? (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold">
                      {PROMPT_SCOPE_LABELS[scope]} · 草稿 v{draft.version}
                    </h2>
                    <Badge variant="warning">草稿</Badge>
                    <code className="max-w-full break-all text-xs text-muted-foreground">{draft.hash}</code>
                  </div>
                  <PromptEditor
                    kind={kind}
                    provider={provider}
                    scope={scope}
                    content={draftContent}
                    onChange={(content) => setDraftEdit({ id: draft.id, content })}
                    insertRef={insertRef}
                  />
                  {errors.length > 0 ? (
                    <ul role="alert" className="space-y-1 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                      {errors.map((error) => (
                        <li key={error}>{error}</li>
                      ))}
                    </ul>
                  ) : null}
                  <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                    <Button size="sm" onClick={saveDraft} disabled={update.isPending}>
                      <Save className="size-3.5" aria-hidden />
                      保存草稿
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => setConfirm({ action: 'publish', version: draft })}>
                      <Send className="size-3.5" aria-hidden />
                      发布
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setConfirm({ action: 'delete', version: draft })}
                    >
                      <Trash2 className="size-3.5" aria-hidden />
                      删除草稿
                    </Button>
                  </div>
                </>
              ) : (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold">{PROMPT_SCOPE_LABELS[scope]}</h2>
                    {activeVersion ? (
                      <>
                        <Badge variant="success">已发布</Badge>
                        <Badge variant="outline">{PROMPT_SOURCE_LABELS[activeVersion.source]}</Badge>
                        <span className="text-xs text-muted-foreground">v{activeVersion.version}</span>
                      </>
                    ) : scope === 'account' ? (
                      <Badge variant="secondary">内置默认 v{detail.data.builtin.version}</Badge>
                    ) : (
                      <Badge variant="outline">空补充</Badge>
                    )}
                  </div>

                  {activeVersion ? (
                    <PromptContentView kind={kind} provider={provider} content={activeVersion.content} scope={activeVersion.scopeType} />
                  ) : scope === 'account' ? (
                    <PromptContentView kind={kind} provider={provider} content={detail.data.builtin.content} />
                  ) : (
                    <p className="text-sm text-muted-foreground">
                      该层暂无本地发布版本；运行时视为空补充，不向账号默认内容追加任何文本。可点击“编辑”创建空补充草稿。
                    </p>
                  )}

                  {activeVersion ? (
                    <p className="text-xs text-muted-foreground">
                      创建：{formatDateTime(activeVersion.createdAt)} · 发布：
                      {activeVersion.publishedAt ? formatDateTime(activeVersion.publishedAt) : '—'}
                    </p>
                  ) : null}

                  <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                    <Button size="sm" variant="outline" onClick={startEdit} disabled={create.isPending}>
                      <Pencil className="size-3.5" aria-hidden />
                      {activeVersion ? '编辑' : '新建草稿'}
                    </Button>
                    {activeVersion ? (
                      <Button size="sm" variant="ghost" onClick={() => setConfirm({ action: 'restore' })}>
                        <RotateCcw className="size-3.5" aria-hidden />
                        恢复内置默认
                      </Button>
                    ) : null}
                  </div>
                </>
              )}
            </Card>
          </div>
        </div>
      ) : null}

      {detail.data ? (
        <PromptHistorySheet
          open={historyOpen}
          onOpenChange={setHistoryOpen}
          detail={detail.data}
          scope={scope}
          onDelete={(version) => setConfirm({ action: 'delete', version })}
          onRollback={(version) => setConfirm({ action: 'rollback', version })}
          onPreview={(version) => setPreview({ versionId: version.id, label: `v${version.version}` })}
          onCreateDraft={() => {
            setHistoryOpen(false)
            setCreateOpen(true)
          }}
        />
      ) : null}

      <Sheet open={effectiveOpen} onOpenChange={setEffectiveOpen}>
        <SheetContent side="right" className="w-full space-y-4 overflow-y-auto bg-background p-6 sm:max-w-3xl">
          <div className="border-b border-border pb-4 pr-8">
            <SheetTitle className="text-lg font-semibold">运行目标分层结果</SheetTitle>
            <SheetDescription className="mt-2 leading-relaxed">
              选择真实群聊或私聊目标，查看账号默认、类型补充与服务端约束的最终 Prompt；不写入 Prompt 版本键，也不形成单会话覆盖。
            </SheetDescription>
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
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
            {effectiveTargetType === 'group' ? (
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">发言人 QQ 号（可选）</span>
                <Input aria-label="生效发言人 QQ 号" inputMode="numeric" value={effectiveSenderId} onChange={(event) => setEffectiveSenderId(event.target.value)} />
              </label>
            ) : null}
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
                <span className="font-mono">模板哈希 {effective.data.hash || '—'}</span>
                {effective.data.renderedHash ? <span className="font-mono">渲染哈希 {effective.data.renderedHash}</span> : null}
              </div>
              {!effective.data.final.variables.sender_id && effective.data.final.content.openai?.systemPrompt?.includes('{{sender_id}}') ? (
                <p className="text-xs text-warning">填写发言人 QQ 号后可对照实际渲染哈希。</p>
              ) : null}
              <ul className="space-y-1 text-xs text-muted-foreground">
                {effective.data.layers.map((layer) => (
                  <li key={`${layer.layer}-${layer.versionId ?? 0}`}>
                    {layer.layer}
                    {layer.version !== undefined ? ` · v${layer.version}` : ''}
                    {layer.sourceRef ? ` · ${layer.sourceRef.replace('langfuse-verified:', 'Langfuse ')}` : layer.source ? ` · ${layer.source}` : ''}
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
        </SheetContent>
      </Sheet>

      <Sheet open={knowledgeOpen} onOpenChange={setKnowledgeOpen}>
        <SheetContent side="right" className="w-full space-y-4 overflow-y-auto bg-background p-6 sm:max-w-3xl">
          <div className="border-b border-border pb-4 pr-8">
            <SheetTitle className="text-lg font-semibold">知识上下文预览</SheetTitle>
            <SheetDescription className="mt-2 leading-relaxed">
              示例预览使用合成数据；真实上下文预览按当前账号、目标作用域与授权读取，并明确展示被跳过的层。
            </SheetDescription>
          </div>
          <KnowledgePreviewCard botId={selectedBotId} />
        </SheetContent>
      </Sheet>

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
        onCreated={(record) => {
          setSelectedDraftId(record.id)
          setCreateOpen(false)
        }}
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
              : confirm?.action === 'delete'
                ? '删除 Prompt 版本？'
                : '恢复内置 Prompt？'
        }
        description={
          confirm?.action === 'publish'
            ? '发布后该版本在对应层生效，只影响之后的 Run；历史决策保留原 Prompt 快照。'
            : confirm?.action === 'rollback'
              ? '仅已归档且曾发布过的版本可回滚；当前发布版本会转为已归档。'
              : confirm?.action === 'delete'
                ? '仅草稿或已归档版本可删除；删除后版本不再出现在管理列表，历史快照仍可按版本 ID 重建。'
                : '将归档当前本地发布版本并恢复内置默认；历史版本保留，不删除。'
        }
        confirmLabel={confirm?.action === 'publish' ? '确认发布' : confirm?.action === 'rollback' ? '确认回滚' : confirm?.action === 'delete' ? '确认删除' : '确认恢复'}
        pending={pending}
        onConfirm={runConfirm}
      />
    </div>
  )
}
