import { Badge } from '../../../components/ui/badge'
import { FeedbackState } from '../../../components/feedback-state'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { Skeleton } from '../../../components/ui/skeleton'
import {
  MEMORY_STATUS_LABELS,
  MEMORY_SUBJECT_LABELS,
  MEMORY_VISIBILITY_LABELS,
  type MemoryEntry,
  type MemoryReplacement,
  type MemoryStatus,
} from '../../../lib/api/contracts'
import { formatDateTime } from '../../../lib/format'
import { useMemoryManagement } from '../queries'

const STATUS_VARIANTS: Record<MemoryStatus, 'success' | 'warning' | 'secondary' | 'destructive'> = {
  candidate: 'warning',
  active: 'success',
  superseded: 'secondary',
  deleted: 'destructive',
}

function statusLabel(status: MemoryStatus | '') {
  return status ? MEMORY_STATUS_LABELS[status] : '—'
}

function MemorySummary({ memory }: { memory: MemoryEntry }) {
  const subject = memory.subjectType
    ? `${MEMORY_SUBJECT_LABELS[memory.subjectType]}${memory.subjectId ? ` · ${memory.subjectId}` : ' · 未记录 ID'}`
    : '未记录'
  return (
    <section className="space-y-3 rounded-lg border border-border bg-secondary/20 p-4">
      <p className="whitespace-pre-wrap text-sm leading-relaxed">{memory.content}</p>
      <dl className="grid gap-2 text-meta sm:grid-cols-2">
        <div><dt className="text-muted-foreground">状态</dt><dd className="mt-0.5"><Badge variant={STATUS_VARIANTS[memory.status]}>{MEMORY_STATUS_LABELS[memory.status]}</Badge></dd></div>
        <div><dt className="text-muted-foreground">可见性</dt><dd className="mt-0.5">{MEMORY_VISIBILITY_LABELS[memory.visibility]}</dd></div>
        <div><dt className="text-muted-foreground">归属</dt><dd className="mt-0.5 font-mono">{memory.ownerKey || '—'}</dd></div>
        <div><dt className="text-muted-foreground">主体</dt><dd className="mt-0.5">{subject}</dd></div>
        <div><dt className="text-muted-foreground">来源</dt><dd className="mt-0.5 break-all font-mono">{memory.source || '未记录'}</dd></div>
        <div><dt className="text-muted-foreground">来源消息 / 发送者</dt><dd className="mt-0.5 font-mono">{memory.sourceMessageId ?? '—'} / {memory.sourceSenderId ?? '—'}</dd></div>
        <div><dt className="text-muted-foreground">操作者</dt><dd className="mt-0.5">{memory.approvedBy || '未记录'}</dd></div>
        <div><dt className="text-muted-foreground">更新时间</dt><dd className="mt-0.5">{memory.updatedAt ? formatDateTime(memory.updatedAt) : '—'}</dd></div>
      </dl>
    </section>
  )
}

function ReplacementCard({ relation }: { relation: MemoryReplacement }) {
  const asNew = relation.direction === 'as_new'
  const counterpart = relation.counterpart
  return (
    <article className="space-y-2 rounded-lg border border-border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={asNew ? 'success' : 'secondary'}>
          {asNew ? '本记忆替代了旧事实' : '本记忆被新事实替代'}
        </Badge>
        <span className="text-meta text-muted-foreground">
          {relation.createdAt ? formatDateTime(relation.createdAt) : '—'}
        </span>
      </div>
      <p className="text-meta text-muted-foreground">
        新事实 <span className="font-mono text-foreground">{relation.newMemoryId}</span>
        {' · '}
        {statusLabel(relation.newStatus)}
      </p>
      <p className="text-meta text-muted-foreground">
        旧事实 <span className="font-mono text-foreground">{relation.oldMemoryId}</span>
        {' · '}
        {statusLabel(relation.oldStatus)}
      </p>
      <p className="text-meta text-muted-foreground">
        对方 {counterpart.memoryId || '—'} · {counterpart.missing ? '已缺失' : statusLabel(counterpart.status)}
      </p>
      <p className="text-meta text-muted-foreground">
        操作者：{relation.actor || '未记录'} · 原因：{relation.reason || '未填写'}
      </p>
    </article>
  )
}

export function MemoryManagementSheet({
  memoryId,
  botId,
  onOpenChange,
}: {
  memoryId: string | null
  botId?: number
  onOpenChange: (open: boolean) => void
}) {
  const query = useMemoryManagement(memoryId, botId)
  return (
    <Sheet open={memoryId !== null} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:w-[560px]">
        <div className="flex h-full flex-col">
          <div className="border-b border-border px-5 py-4 pr-14">
            <SheetTitle>记忆详情</SheetTitle>
            <SheetDescription className="mt-1">
              查看记忆来源和替代关系。
            </SheetDescription>
          </div>
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
            {query.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-24 w-full" />
                <Skeleton className="h-24 w-full" />
              </div>
            ) : query.isError ? (
              <FeedbackState variant="error" error={query.error} onRetry={() => void query.refetch()} />
            ) : query.data ? (
              <>
                <MemorySummary memory={query.data.memory} />
                {query.data.factSources.length > 0 && (
                  <section className="space-y-2">
                    <h3 className="text-sm font-semibold">有效事实来源</h3>
                    <ul className="space-y-1 text-meta">
                      {query.data.factSources.map((source) => (
                        <li key={source.messageId} className="font-mono">
                          消息 {source.messageId} · 发送者 {source.senderId}
                        </li>
                      ))}
                    </ul>
                  </section>
                )}
                <section className="space-y-2">
                  <h3 className="text-sm font-semibold">替代关系</h3>
                  {query.data.replacements.length === 0 ? (
                    <p className="text-sm text-muted-foreground">暂无替代关系。</p>
                  ) : (
                    query.data.replacements.map((relation) => (
                      <ReplacementCard key={relation.replacementId} relation={relation} />
                    ))
                  )}
                </section>
              </>
            ) : null}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}