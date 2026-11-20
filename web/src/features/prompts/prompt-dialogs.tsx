import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog'
import { Input } from '../../components/ui/input'
import { Select } from '../../components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../components/ui/sheet'
import { Textarea } from '../../components/ui/textarea'
import type {
  ManagedPromptContent,
  ManagedPromptProvider,
  ManagedPromptScope,
  ManagedPromptVersion,
} from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { PromptContentView, PromptEditor } from './prompt-content-editor'
import {
  PROMPT_SCOPE_LABELS,
  normaliseContentForScope,
  promptContentToWire,
  validatePromptContent,
} from './prompt-content'
import {
  useCreatePromptDraft,
  usePromptDiff,
  usePromptPreview,
  useUpdatePromptDraft,
  type CreateDraftInput,
  type PromptDiffParams,
} from './prompt-queries'

function errorText(error: unknown): string {
  return describeError(error).description
}

interface CreateDraftDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  botId: number
  promptId: string
  scope: ManagedPromptScope
  provider: ManagedPromptProvider
  name: string
  hasActive: boolean
  versions: ManagedPromptVersion[]
  onCreated: (version: ManagedPromptVersion) => void
}

export function CreateDraftDialog({
  open,
  onOpenChange,
  botId,
  promptId,
  scope,
  provider,
  name,
  hasActive,
  versions,
  onCreated,
}: CreateDraftDialogProps) {
  const create = useCreatePromptDraft()
  const [base, setBase] = useState<CreateDraftInput['base']>('auto')
  const [baseVersionId, setBaseVersionId] = useState<number>(0)
  const [note, setNote] = useState('')

  useEffect(() => {
    if (open) {
      setBase('auto')
      setBaseVersionId(versions[0]?.id ?? 0)
      setNote('')
      create.reset()
    }
    // Reset only when the dialog opens; the mutation object is unstable.
    // 仅在对话框打开时重置；mutation 对象引用不稳定。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const submit = () => {
    create.mutate(
      {
        botId,
        promptId,
        scope,
        provider,
        name,
        base,
        baseVersionId: base === 'version' ? baseVersionId : undefined,
        note,
      },
      {
        onSuccess: (version) => {
          toast.success('已创建 Prompt 草稿')
          onCreated(version)
          onOpenChange(false)
        },
        onError: (error) => toast.error('创建草稿失败', { description: errorText(error) }),
      },
    )
  }

  const options: Array<{ value: CreateDraftInput['base']; label: string }> = [
    { value: 'auto', label: '自动（推荐）' },
  ]
  if (scope === 'account') options.push({ value: 'builtin', label: '从内置默认创建' })
  if (hasActive) options.push({ value: 'active', label: '从当前生效版本创建' })
  if (scope !== 'account') options.push({ value: 'empty', label: '创建空补充' })
  if (versions.length > 0) options.push({ value: 'version', label: '复制指定版本' })

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full space-y-4 overflow-y-auto p-6 sm:max-w-md">
        <SheetTitle>新建 Prompt 草稿</SheetTitle>
        <SheetDescription className="leading-relaxed">
          草稿不会影响新 Run；发布后才在该层生效。{PROMPT_SCOPE_LABELS[scope]}的编辑与运行目标分离，scope_id 固定为 0。
        </SheetDescription>
        <label className="block space-y-1 text-sm">
          <span className="font-medium">创建方式</span>
          <Select value={base} onChange={(event) => setBase(event.target.value as CreateDraftInput['base'])} aria-label="创建方式">
            {options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </Select>
        </label>
        {base === 'version' ? (
          <label className="block space-y-1 text-sm">
            <span className="font-medium">复制版本</span>
            <Select
              value={String(baseVersionId)}
              onChange={(event) => setBaseVersionId(Number(event.target.value))}
              aria-label="复制版本"
            >
              {versions.map((version) => (
                <option key={version.id} value={version.id}>
                  v{version.version} · {version.status} · {version.name || promptId}
                </option>
              ))}
            </Select>
          </label>
        ) : null}
        <label className="block space-y-1 text-sm">
          <span className="font-medium">名称</span>
          <Input value={name} readOnly aria-label="草稿名称" />
          <span className="block text-xs text-muted-foreground">名称由 Prompt 与层级自动生成，可在版本详情备注中说明修订。</span>
        </label>
        <label className="block space-y-1 text-sm">
          <span className="font-medium">修订说明（可选）</span>
          <Textarea value={note} onChange={(event) => setNote(event.target.value)} rows={3} aria-label="修订说明" />
        </label>
        {create.isError ? (
          <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {errorText(create.error)}
          </p>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={create.isPending}>
            取消
          </Button>
          <Button onClick={submit} disabled={create.isPending || (base === 'version' && baseVersionId === 0)}>
            {create.isPending ? '创建中…' : '创建草稿'}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}

interface PromptEditorSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  version: ManagedPromptVersion | null
  scope: ManagedPromptScope
}

export function PromptEditorSheet({ open, onOpenChange, version, scope }: PromptEditorSheetProps) {
  const update = useUpdatePromptDraft()
  const [content, setContent] = useState<ManagedPromptContent | null>(null)
  const [note, setNote] = useState('')
  const [errors, setErrors] = useState<string[]>([])

  useEffect(() => {
    if (open && version) {
      setContent(normaliseContentForScope(version.kind, version.provider, scope, version.content))
      setNote(version.note)
      setErrors([])
      update.reset()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, version?.id])

  if (!version || !content) {
    return null
  }

  const submit = () => {
    const clientErrors = validatePromptContent(version.kind, version.provider, scope, content)
    setErrors(clientErrors)
    if (clientErrors.length > 0) return
    update.mutate(
      { versionId: version.id, content: promptContentToWire(content, scope), note },
      {
        onSuccess: () => {
          toast.success('Prompt 草稿已保存')
          onOpenChange(false)
        },
        onError: (error) => toast.error('保存失败', { description: errorText(error) }),
      },
    )
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full space-y-4 overflow-y-auto p-6 sm:max-w-2xl">
        <SheetTitle>编辑 Prompt 草稿 · v{version.version}</SheetTitle>
        <SheetDescription className="leading-relaxed">
          仅草稿可编辑；发布或归档版本只读。补充层只能追加文本，不能覆盖账号默认内容。
        </SheetDescription>
        <PromptEditor
          kind={version.kind}
          provider={version.provider}
          scope={scope}
          content={content}
          onChange={setContent}
        />
        <label className="block space-y-1 text-sm">
          <span className="font-medium">修订说明（可选）</span>
          <Textarea value={note} onChange={(event) => setNote(event.target.value)} rows={3} aria-label="修订说明" />
        </label>
        {errors.length > 0 ? (
          <ul role="alert" className="space-y-1 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {errors.map((error) => (
              <li key={error}>{error}</li>
            ))}
          </ul>
        ) : null}
        {update.isError ? (
          <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {errorText(update.error)}
          </p>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={update.isPending}>
            取消
          </Button>
          <Button onClick={submit} disabled={update.isPending}>
            {update.isPending ? '保存中…' : '保存草稿'}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}

interface PromptDiffSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  params: PromptDiffParams | null
}

export function PromptDiffSheet({ open, onOpenChange, params }: PromptDiffSheetProps) {
  const diff = usePromptDiff(open ? params : null)
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full space-y-4 overflow-y-auto p-6 sm:max-w-3xl">
        <SheetTitle>版本对比</SheetTitle>
        <SheetDescription>
          对比基础版本与草稿；只展示文本差异，不调用模型、不写库、不发送。
        </SheetDescription>
        {diff.isLoading ? <p role="status">正在读取 diff…</p> : null}
        {diff.isError ? (
          <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {errorText(diff.error)}
          </p>
        ) : null}
        {diff.data ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <Badge variant="outline">{diff.data.from.source} v{diff.data.from.version}</Badge>
              <span>→</span>
              <Badge variant="outline">{diff.data.to.status} v{diff.data.to.version}</Badge>
              <span>{diff.data.changed ? '有变化' : '无变化'}</span>
            </div>
            {diff.data.lines.length > 0 ? (
              <div className="overflow-x-auto rounded-md border border-border font-mono text-xs">
                {diff.data.lines.map((line, index) => (
                  <div
                    key={`${index}-${line.op}`}
                    className={
                      line.op === '+'
                        ? 'bg-success/10 px-3 text-success'
                        : line.op === '-'
                          ? 'bg-destructive/10 px-3 text-destructive'
                          : 'px-3 text-muted-foreground'
                    }
                  >
                    <span className="inline-block w-4 select-none">{line.op}</span>
                    <span className="whitespace-pre-wrap break-words">{line.text}</span>
                  </div>
                ))}
              </div>
            ) : (
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 text-xs">
                {diff.data.unifiedDiff || '无差异'}
              </pre>
            )}
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

interface PromptPreviewDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  botId: number
  promptId: string
  provider: ManagedPromptProvider
  versionId: number
  versionLabel: string
}

export function PromptPreviewDialog({
  open,
  onOpenChange,
  botId,
  promptId,
  provider,
  versionId,
  versionLabel,
}: PromptPreviewDialogProps) {
  const preview = usePromptPreview()
  const [mode, setMode] = useState<'example' | 'real'>('example')
  const [targetType, setTargetType] = useState<'group' | 'private'>('group')
  const [targetId, setTargetId] = useState('')
  const [conversationId, setConversationId] = useState('')
  const [senderId, setSenderId] = useState('')

  useEffect(() => {
    if (open) {
      setMode('example')
      setTargetType('group')
      setTargetId('')
      setConversationId('')
      setSenderId('')
      preview.reset()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, versionId])

  const run = () => {
    preview.mutate({
      botId,
      promptId,
      provider,
      mode,
      versionId,
      ...(mode === 'real'
        ? {
            targetType,
            targetId: Number(targetId),
            senderId: targetType === 'group' ? Number(senderId) || undefined : undefined,
            conversationId: conversationId || undefined,
          }
        : {}),
    })
  }

  const targetNumber = Number(targetId)
  const realInvalid = mode === 'real' && (!Number.isInteger(targetNumber) || targetNumber <= 0)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] w-full max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Prompt 预览 · {versionLabel}</DialogTitle>
          <DialogDescription>
            预览不调用模型、不发送、不创建决策。示例预览使用合成数据；真实上下文预览按当前授权读取，未授权层会标注 Skipped。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3 text-sm">
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant={mode === 'example' ? 'secondary' : 'outline'}
              onClick={() => setMode('example')}
            >
              示例预览
            </Button>
            <Button
              type="button"
              size="sm"
              variant={mode === 'real' ? 'secondary' : 'outline'}
              onClick={() => setMode('real')}
            >
              真实上下文预览
            </Button>
            <Badge variant="outline">{mode === 'example' ? '合成示例，不读真实内容' : '按当前 scope 授权'}</Badge>
          </div>
          {mode === 'real' ? (
            <div className="grid gap-2 sm:grid-cols-3">
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">目标类型</span>
                <Select value={targetType} onChange={(event) => setTargetType(event.target.value as 'group' | 'private')} aria-label="目标类型">
                  <option value="group">群聊</option>
                  <option value="private">私聊</option>
                </Select>
              </label>
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">目标 ID（群号/QQ）</span>
                <Input value={targetId} onChange={(event) => setTargetId(event.target.value)} inputMode="numeric" aria-label="目标 ID" />
              </label>
              {targetType === 'group' ? (
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">发言人 QQ 号（可选）</span>
                  <Input value={senderId} onChange={(event) => setSenderId(event.target.value)} inputMode="numeric" aria-label="发言人 QQ 号" />
                </label>
              ) : null}
              <label className="space-y-1 text-xs">
                <span className="text-muted-foreground">会话 ID（可选）</span>
                <Input value={conversationId} onChange={(event) => setConversationId(event.target.value)} aria-label="会话 ID" />
              </label>
            </div>
          ) : null}
          <Button onClick={run} disabled={preview.isPending || realInvalid}>
            {preview.isPending ? '生成中…' : '生成预览'}
          </Button>
          {realInvalid ? <p className="text-xs text-destructive">真实上下文预览必须填写大于 0 的目标 ID。</p> : null}
          {preview.isError ? (
            <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {errorText(preview.error)}
            </p>
          ) : null}
          {preview.data ? (
            <div className="space-y-3 rounded-lg border border-border p-3">
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <Badge variant={preview.data.previewMode === 'real' ? 'warning' : 'secondary'}>
                  {preview.data.previewMode === 'real' ? '真实上下文预览' : '示例预览'}
                </Badge>
                <span>VersionID {preview.data.versionId}</span>
                <span className="font-mono">预览哈希 {preview.data.renderedHash || preview.data.previewHash || '—'}</span>
              </div>
              {preview.data.warnings.length > 0 ? (
                <ul className="list-disc space-y-1 pl-5 text-xs text-warning">
                  {preview.data.warnings.map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              ) : null}
              <div>
                <div className="text-xs text-muted-foreground">最终内容</div>
                <div className="mt-2">
                  <PromptContentView
                    kind={preview.data.kind}
                    provider={preview.data.provider}
                    content={preview.data.final.content}
                  />
                </div>
              </div>
              <div className="text-xs text-muted-foreground">
                变量：{Object.entries(preview.data.final.variables).map(([key, value]) => `${key}=${value}`).join(' · ') || '无'}
              </div>
              <div className="text-xs text-muted-foreground">
                分层：{preview.data.layers.map((layer) => layer.layer).join(' → ') || '—'}
              </div>
            </div>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
