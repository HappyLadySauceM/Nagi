import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, ArrowLeftRight } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../../components/ui/dialog'
import { FeedbackState } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import { Textarea } from '../../../components/ui/textarea'
import {
  MEMORY_STATUS_LABELS,
  MEMORY_SUBJECT_LABELS,
  MEMORY_VISIBILITY_LABELS,
  type MemoryEntry,
} from '../../../lib/api/contracts'
import { ApiError } from '../../../lib/api/client'
import { describeError } from '../../../lib/errors'
import { formatDateTime } from '../../../lib/format'
import { useMemories, useMemoryManagement, useSupersedeMemory } from '../queries'

function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `supersede-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function subjectText(memory: MemoryEntry): string {
  if (!memory.subjectType) return '未记录'
  return `${MEMORY_SUBJECT_LABELS[memory.subjectType]}${memory.subjectId ? ` · ${memory.subjectId}` : ' · 未记录 ID'}`
}

function supersedeErrorText(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.message) {
      case 'subject_mismatch':
        return '旧事实的主体、可见性或归属与候选不一致，未执行替代。'
      case 'subject_ambiguous':
        return '候选或旧事实主体不明确，不能替代；请补充明确主体后重试。'
      case 'idempotency_conflict':
        return '该替代请求使用了不同的幂等键，可能已执行；请刷新后核对替代关系。'
      case 'memory_status_conflict':
        return '候选或旧事实状态已变化（可能已被审批、替代或删除），请刷新后重新选择。'
      case 'memory_not_found':
        return '候选或旧事实已不存在，未执行替代。'
      case 'replacement_invalid':
        return '替代请求参数无效，请重新选择旧事实。'
      default:
        break
    }
    if (error.kind === 'network' || error.kind === 'timeout') {
      return '请求结果不确定，操作可能已执行；请刷新并查看「替代关系」后再决定是否重试。'
    }
  }
  return describeError(error).description
}

function FactSummary({ title, memory }: { title: string; memory: MemoryEntry }) {
  return (
    <section className="space-y-2 rounded-lg border border-border bg-secondary/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-semibold">{title}</h3>
        <Badge variant={memory.status === 'candidate' ? 'warning' : 'success'}>
          {MEMORY_STATUS_LABELS[memory.status]}
        </Badge>
      </div>
      <p className="whitespace-pre-wrap text-sm leading-relaxed">{memory.content}</p>
      <dl className="grid gap-x-4 gap-y-1 text-meta sm:grid-cols-2">
        <div><dt className="text-muted-foreground">主体</dt><dd className="mt-0.5">{subjectText(memory)}</dd></div>
        <div><dt className="text-muted-foreground">可见性 / 归属</dt><dd className="mt-0.5">{MEMORY_VISIBILITY_LABELS[memory.visibility]} · {memory.ownerKey || '—'}</dd></div>
        <div><dt className="text-muted-foreground">来源</dt><dd className="mt-0.5 break-all font-mono">{memory.source || '未记录'}</dd></div>
        <div><dt className="text-muted-foreground">更新时间</dt><dd className="mt-0.5">{memory.updatedAt ? formatDateTime(memory.updatedAt) : '—'}</dd></div>
      </dl>
    </section>
  )
}

function SupersedeForm({
  candidate,
  onCancel,
  onSuperseded,
}: {
  candidate: MemoryEntry
  onCancel: () => void
  onSuperseded: (oldMemoryId: string, botId: number) => void
}) {
  const [selectedId, setSelectedId] = useState('')
  const [reason, setReason] = useState('')
  const keyRef = useRef(newIdempotencyKey())
  const supersede = useSupersedeMemory()
  const source = useMemories({
    search: '',
    page: 1,
    limit: 200,
    status: 'active',
    visibility: candidate.visibility,
    ownerKey: candidate.ownerKey,
    botId: candidate.botId,
  })
  const knownSubject = Boolean(
    candidate.subjectType &&
      candidate.subjectType !== 'unknown' &&
      candidate.subjectId?.trim(),
  )
  const eligible = (source.data?.items ?? []).filter(
    (memory) =>
      memory.id !== candidate.id &&
      memory.status === 'active' &&
      memory.botId === candidate.botId &&
      memory.visibility === candidate.visibility &&
      memory.ownerKey === candidate.ownerKey &&
      memory.subjectType === candidate.subjectType &&
      memory.subjectId === candidate.subjectId,
  )

  useEffect(() => {
    keyRef.current = newIdempotencyKey()
  }, [selectedId])

  const errorText = supersede.isError ? supersedeErrorText(supersede.error) : ''

  const submit = () => {
    if (!selectedId || !knownSubject || supersede.isPending) return
    supersede.mutate(
      {
        candidateId: candidate.id,
        oldMemoryId: selectedId,
        reason: reason.trim(),
        idempotencyKey: keyRef.current,
      },
      {
        onSuccess: (result) => {
          toast.success(result.idempotent ? '该替代关系已存在，未重复执行' : '记忆已替代', {
            description: result.idempotent
              ? '服务端返回已有替代关系。'
              : `旧事实已进入「已取代」，不再参与正常召回；替代关系可在「查看替代关系」中审计。`,
          })
          onSuperseded(result.oldMemoryId, candidate.botId)
        },
      },
    )
  }

  if (!knownSubject) {
    return (
      <div className="space-y-3">
        <FactSummary title="新事实（候选）" memory={candidate} />
        <div role="alert" className="rounded-lg border border-warning/30 bg-warning/10 p-3 text-sm text-warning">
          <p className="flex items-center gap-2 font-medium">
            <AlertTriangle className="size-4" aria-hidden />
            该候选主体未知或不完整，无法安全替代
          </p>
          <p className="mt-1 text-meta leading-relaxed">
            系统不会从来源消息、发送者或相似正文推断同一主体。请先补齐明确主体后重试。
          </p>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>关闭</Button>
        </DialogFooter>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <FactSummary title="新事实（候选）" memory={candidate} />
      <section className="space-y-2">
        <div>
          <h3 className="text-sm font-semibold">选择要替代的旧 active 事实</h3>
          <p className="mt-1 text-meta leading-relaxed text-muted-foreground">
            只显示同账号、同可见性、同归属、同主体的 active 事实。必须手动选择；系统不会按相似正文自动推断，也不会自动替代。
          </p>
        </div>
        {source.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
          </div>
        ) : source.isError ? (
          <FeedbackState variant="error" error={source.error} onRetry={() => void source.refetch()} compact />
        ) : eligible.length === 0 ? (
          <p className="rounded-lg border border-dashed border-border p-3 text-sm text-muted-foreground">
            没有主体一致的 active 事实可替代。请先确认旧事实已审批启用且主体与候选一致。
          </p>
        ) : (
          <div role="radiogroup" aria-label="选择要替代的旧事实" className="space-y-2">
            {eligible.map((memory) => (
              <label
                key={memory.id}
                className="flex cursor-pointer gap-3 rounded-lg border border-border p-3 hover:bg-secondary/30 has-[:checked]:border-primary has-[:checked]:bg-primary/5"
              >
                <input
                  type="radio"
                  name="supersede-old-memory"
                  value={memory.id}
                  checked={selectedId === memory.id}
                  onChange={() => setSelectedId(memory.id)}
                  className="mt-1 size-4 shrink-0 accent-primary"
                />
                <span className="min-w-0 space-y-1">
                  <span className="block whitespace-pre-wrap text-sm leading-relaxed">{memory.content}</span>
                  <span className="block text-meta text-muted-foreground">
                    {MEMORY_VISIBILITY_LABELS[memory.visibility]} · 归属 {memory.ownerKey || '—'} · 主体 {subjectText(memory)}
                  </span>
                  <span className="block break-all font-mono text-meta text-muted-foreground">
                    来源：{memory.source || '未记录'} · 更新：{memory.updatedAt ? formatDateTime(memory.updatedAt) : '—'}
                  </span>
                </span>
              </label>
            ))}
          </div>
        )}
      </section>

      <label className="block space-y-1 text-sm">
        <span>替代原因（可选）</span>
        <Textarea
          rows={2}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          placeholder="例如：用户补充了新的偏好事实"
          maxLength={500}
        />
      </label>

      <div className="rounded-lg border border-warning/30 bg-warning/10 p-3 text-meta leading-relaxed text-warning">
        <p className="font-medium">替代影响</p>
        <p className="mt-1">
          确认后，新候选会变为「已启用」并进入正常召回；旧事实会变为「已取代」，不再出现在正常召回和活跃列表。旧事实不会被物理删除；操作者、时间和原因会记录在替代关系中。
        </p>
      </div>

      {errorText ? <p role="alert" className="text-sm text-destructive">{errorText}</p> : null}

      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onCancel} disabled={supersede.isPending}>
          取消
        </Button>
        <Button
          type="button"
          onClick={submit}
          disabled={!selectedId || supersede.isPending}
          aria-label="确认替代"
        >
          <ArrowLeftRight aria-hidden />
          {supersede.isPending ? '替代中…' : '确认替代'}
        </Button>
      </DialogFooter>
    </div>
  )
}

export function MemorySupersedeDialog({
  candidate,
  onOpenChange,
  onSuperseded,
}: {
  candidate: MemoryEntry | null
  onOpenChange: (open: boolean) => void
  onSuperseded?: (oldMemoryId: string, botId: number) => void
}) {
  // The list row carries its own bot id; do not depend on the currently
  // selected account being resolved.
  // 列表行自带 bot id，不依赖当前账号是否已识别。
  const query = useMemoryManagement(candidate?.id ?? null, candidate?.botId)
  return (
    <Dialog open={candidate !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>用新事实替代旧事实</DialogTitle>
          <DialogDescription>
            显式选择一条同账号、同可见性/归属、同主体的 active 事实。不会自动覆盖，也不会按相似正文推断。
          </DialogDescription>
        </DialogHeader>
        {candidate === null ? null : query.isLoading ? (
          <div className="mt-4 space-y-2">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : query.isError ? (
          <div className="mt-4"><FeedbackState variant="error" error={query.error} onRetry={() => void query.refetch()} /></div>
        ) : query.data ? (
          query.data.memory.status === 'candidate' ? (
            <SupersedeForm
              candidate={query.data.memory}
              onCancel={() => onOpenChange(false)}
              onSuperseded={(oldMemoryId, botId) => {
                onOpenChange(false)
                onSuperseded?.(oldMemoryId, botId)
              }}
            />
          ) : (
            <div role="alert" className="mt-4 rounded-lg border border-warning/30 bg-warning/10 p-3 text-sm text-warning">
              该记忆已不是候选状态，不能执行替代。请刷新列表后重试。
            </div>
          )
        ) : null}
      </DialogContent>
    </Dialog>
  )
}