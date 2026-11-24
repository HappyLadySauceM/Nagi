import { useState } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { ArrowLeftRight, Check, History, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { ConfirmDialog } from '../../components/confirm-dialog'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Pagination } from '../../components/pagination'
import { Skeleton } from '../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import {
  MEMORY_STATUS_LABELS,
  MEMORY_VISIBILITY_LABELS,
  type MemoryEntry,
  type MemoryStatus,
} from '../../lib/api/contracts'
import { ApiError } from '../../lib/api/client'
import { describeError } from '../../lib/errors'
import type { MemorySearch } from '../../lib/search-params'
import { formatDateTime, formatRelativeTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccountContext } from '../accounts/account-context'
import { MemoryCreateDialog } from './components/memory-create-dialog'
import { MemoryEditSheet } from './components/memory-edit-sheet'
import { MemoryFilters } from './components/memory-filters'
import { MemoryManagementSheet } from './components/memory-management-sheet'
import { MemorySupersedeDialog } from './components/memory-supersede-dialog'
import { KnowledgeSummaryCards } from '../knowledge/components/knowledge-summary'
import { MemoryTabs } from '../knowledge/components/memory-tabs'
import { ExtractionPanel } from '../knowledge/components/extraction-panel'
import { PromptManagementPage } from '../prompts/prompt-management-page'
import { useApproveMemory, useDeleteMemory, useMemories } from './queries'

const STATUS_VARIANTS: Record<MemoryStatus, 'success' | 'warning' | 'secondary' | 'destructive'> = {
  candidate: 'warning',
  active: 'success',
  superseded: 'secondary',
  deleted: 'destructive',
}

export function MemoriesPage() {
  const search = useSearch({ from: '/memories' })
  const navigate = useNavigate({ from: '/memories' })

  const changeTab = (tab: MemorySearch['tab']) => {
    void navigate({ search: (prev) => ({ ...prev, tab, page: 1 }), replace: true })
  }

  return (
    <div className="space-y-4">
      <MemoryTabs tab={search.tab} onTabChange={changeTab} />
      {search.tab === 'prompts' ? (
        <PromptManagementPage />
      ) : search.tab === 'extraction' ? (
        <ExtractionPanel />
      ) : (
        <MemoriesEntriesPage />
      )}
    </div>
  )
}

function MemoriesEntriesPage() {
  const search = useSearch({ from: '/memories' })
  const navigate = useNavigate({ from: '/memories' })
  const { selectedBotId: botId, selectedAccount } = useAccountContext()

  const memories = useMemories({
    search: search.search,
    page: search.page,
    limit: search.limit,
    status: search.status,
    visibility: search.visibility,
    ownerKey: search.ownerKey,
    botId,
  })
  const approve = useApproveMemory()
  const remove = useDeleteMemory()

  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<MemoryEntry | null>(null)
  const [approving, setApproving] = useState<MemoryEntry | null>(null)
  const [deleting, setDeleting] = useState<MemoryEntry | null>(null)
  const [superseding, setSuperseding] = useState<MemoryEntry | null>(null)
  const [managing, setManaging] = useState<{ id: string; botId: number } | null>(null)

  const rows = memories.data?.items ?? []
  const hasFilters =
    search.search !== '' ||
    search.ownerKey !== '' ||
    search.status !== '' ||
    search.visibility !== ''

  const changeFilters = (patch: Partial<typeof search>) => {
    void navigate({
      search: (prev) => ({ ...prev, ...patch, page: 1 }),
      replace: true,
    })
  }

  const runApprove = () => {
    if (!approving) {
      return
    }
    approve.mutate(approving.id, {
      onSuccess: () => {
        toast.success('记忆已审批并启用')
        setApproving(null)
      },
      onError: (error) => {
        const existingId = error instanceof ApiError ? error.existingMemoryId : undefined
        const account = approving.botId
        const feedback = describeError(error)
        if (existingId) {
          setApproving(null)
          toast.error(feedback.title, {
            description: feedback.description,
            action: { label: '查看已有记忆', onClick: () => setManaging({ id: existingId, botId: account }) },
          })
        } else {
          toast.error('审批失败', { description: feedback.description })
        }
      },
    })
  }

  const runDelete = () => {
    if (!deleting) {
      return
    }
    remove.mutate(deleting.id, {
      onSuccess: () => {
        toast.success('记忆已永久删除', {
          description: '该记忆已从数据库中删除，无法恢复。',
        })
        setDeleting(null)
      },
      onError: (error) =>
        toast.error('永久删除失败', { description: describeError(error).description }),
    })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="记忆条目"
        description={`候选记忆需要审批后才会被会话读取。当前账号：${selectedAccount?.nickname || botId || '未识别'}；历史共享记忆只在可核验的原会话内可用。`}
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void memories.refetch()}>
              <RefreshCw
                className={cn('size-3.5', memories.isFetching && 'animate-spin')}
                aria-hidden
              />
              刷新
            </Button>
            <Button
              size="sm"
              onClick={() => setCreateOpen(true)}
              disabled={botId === undefined}
              title={botId === undefined ? '机器人账号尚未识别，无法绑定记忆' : undefined}
            >
              <Plus aria-hidden />
              新建记忆
            </Button>
          </div>
        }
      />

      <KnowledgeSummaryCards botId={botId} />

      <Card className="overflow-hidden">
        <MemoryFilters value={search} onChange={changeFilters} disabled={memories.isFetching} />

        {memories.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 6 }).map((_, index) => (
              <Skeleton key={index} className="h-12 w-full" />
            ))}
          </div>
        ) : memories.isError ? (
          <FeedbackState
            variant="error"
            error={memories.error}
            onRetry={() => void memories.refetch()}
          />
        ) : rows.length === 0 ? (
          <FeedbackState
            variant="empty"
            title={hasFilters ? '没有匹配的记忆' : '还没有长期记忆'}
            description={
              hasFilters
                ? '当前筛选条件下没有记录，可调整关键词或清除筛选。'
                : '创建一条候选记忆，或由机器人在允许写入的作用域中自动产生。'
            }
            action={
              hasFilters ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    changeFilters({ search: '', ownerKey: '', status: '', visibility: '' })
                  }
                >
                  清除筛选
                </Button>
              ) : (
                <Button size="sm" onClick={() => setCreateOpen(true)} disabled={botId === undefined}>
                  新建记忆
                </Button>
              )
            }
          />
        ) : (
          <Table label="长期记忆列表">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>内容</TableHead>
                <TableHead>可见性</TableHead>
                <TableHead>归属</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">置信度</TableHead>
                <TableHead className="text-right">更新时间</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((memory) => (
                <TableRow key={memory.id}>
                  <TableCell className="max-w-[380px]">
                    <p className="line-clamp-2 text-xs leading-relaxed text-foreground">
                      {memory.content}
                    </p>
                    {memory.source ? (
                      <p className="mt-1 truncate font-mono text-meta text-muted-foreground">
                        {memory.source}
                      </p>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={memory.visibility === 'bot_shared' ? 'accent' : 'secondary'}
                      title={
                        memory.visibility === 'bot_shared'
                          ? '来源可核验时仅原会话可读'
                          : undefined
                      }
                    >
                      {MEMORY_VISIBILITY_LABELS[memory.visibility]}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-meta text-muted-foreground tabular-nums">
                    {memory.ownerKey || '—'}
                  </TableCell>
                  <TableCell>
                    <Badge variant={STATUS_VARIANTS[memory.status]}>
                      {MEMORY_STATUS_LABELS[memory.status]}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right font-mono text-muted-foreground tabular-nums">
                    {memory.confidence.toFixed(2)}
                  </TableCell>
                  <TableCell
                    className="text-right text-muted-foreground tabular-nums"
                    title={memory.updatedAt ? formatDateTime(memory.updatedAt) : undefined}
                  >
                    {memory.updatedAt ? formatRelativeTime(memory.updatedAt) : '—'}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      {memory.status === 'candidate' && memory.visibility !== 'bot_shared' ? (
                        <>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label="用新事实替代旧事实"
                            title="用新事实替代旧事实"
                            onClick={() => setSuperseding(memory)}
                          >
                            <ArrowLeftRight className="size-3.5" aria-hidden />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label="审批并启用"
                            title="审批并启用"
                            onClick={() => setApproving(memory)}
                          >
                            <Check className="size-3.5" aria-hidden />
                          </Button>
                        </>
                      ) : null}
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="查看记忆详情"
                        title="查看记忆详情"
                        onClick={() => setManaging({ id: memory.id, botId: memory.botId })}
                      >
                        <History className="size-3.5" aria-hidden />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={memory.visibility === 'bot_shared' ? '查看历史共享记忆' : '编辑记忆'}
                        title={memory.visibility === 'bot_shared' ? '查看' : '编辑'}
                        onClick={() => setEditing(memory)}
                        disabled={memory.status === 'deleted'}
                      >
                        <Pencil className="size-3.5" aria-hidden />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="永久删除记忆"
                        title="永久删除"
                        className="text-muted-foreground hover:text-destructive"
                        onClick={() => setDeleting(memory)}
                        disabled={memory.status === 'deleted'}
                      >
                        <Trash2 className="size-3.5" aria-hidden />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        {rows.length > 0 ? (
          <div className="px-5 pb-4">
            <Pagination
              total={memories.data?.total ?? 0}
              page={search.page}
              limit={search.limit}
              pending={memories.isFetching}
              itemLabel="条记忆"
              onPageChange={(next) =>
                void navigate({ search: (prev) => ({ ...prev, page: next }) })
              }
              onLimitChange={(next) =>
                void navigate({ search: (prev) => ({ ...prev, limit: next, page: 1 }) })
              }
            />
          </div>
        ) : null}
      </Card>

      <MemoryCreateDialog open={createOpen} onOpenChange={setCreateOpen} botId={botId} />

      <MemoryEditSheet memory={editing} onOpenChange={(open) => !open && setEditing(null)} />

      <MemorySupersedeDialog
        candidate={superseding}
        onOpenChange={(open) => !open && setSuperseding(null)}
        onSuperseded={(oldMemoryId, oldMemoryBotId) => {
          setSuperseding(null)
          setManaging({ id: oldMemoryId, botId: oldMemoryBotId })
        }}
      />
      <MemoryManagementSheet
        memoryId={managing && (botId === undefined || managing.botId === botId) ? managing.id : null}
        botId={managing?.botId}
        onOpenChange={(open) => !open && setManaging(null)}
      />

      <ConfirmDialog
        open={approving !== null}
        onOpenChange={(open) => !open && setApproving(null)}
        title="审批并启用该记忆？"
        description="审批后，符合可见性范围（群 / 个人 / 会话）的对话会读取这条记忆。"
        confirmLabel="审批并启用"
        pending={approve.isPending}
        onConfirm={runApprove}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="永久删除该记忆？"
        description="该记忆将从数据库中永久删除，无法恢复。"
        confirmLabel="永久删除"
        destructive
        pending={remove.isPending}
        onConfirm={runDelete}
      />
    </div>
  )
}
