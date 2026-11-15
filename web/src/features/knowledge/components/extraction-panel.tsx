import { useState } from 'react'
import { RefreshCw, RotateCcw } from 'lucide-react'
import { toast } from 'sonner'
import { ConfirmDialog } from '../../../components/confirm-dialog'
import { FeedbackState } from '../../../components/feedback-state'
import { PageHeader } from '../../../components/page-header'
import { Pagination } from '../../../components/pagination'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { Input } from '../../../components/ui/input'
import { Select } from '../../../components/ui/select'
import { Skeleton } from '../../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../../components/ui/table'
import {
  EXTRACTION_JOB_STATUS_LABELS,
  type ExtractionJob,
  type ExtractionJobStatus,
} from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { formatCount, formatDateTime, formatRelativeTime } from '../../../lib/format'
import { useAccountContext } from '../../accounts/account-context'
import { useExtractionJobs, useExtractionSettings, useRetryExtractionJob } from '../queries'

const JOB_STATUS_VARIANTS: Record<
  ExtractionJobStatus,
  'secondary' | 'warning' | 'success' | 'destructive' | 'outline'
> = {
  pending: 'secondary',
  processing: 'warning',
  done: 'success',
  failed: 'destructive',
  cancelled: 'outline',
}

const STATUS_OPTIONS: ExtractionJobStatus[] = [
  'pending',
  'processing',
  'done',
  'failed',
  'cancelled',
]

function retryBlockedReason(job: ExtractionJob): string {
  if (job.status !== 'failed') {
    return '仅失败任务可以重试'
  }
  if (!job.sourceAvailable) {
    return '来源消息已不可用，不能重试'
  }
  if (job.manualRetries >= 1) {
    return '已达到最多 1 次手动重试'
  }
  return ''
}

function scopeLabel(job: ExtractionJob): string {
  const kind = job.scopeType === 'group' ? '群聊' : job.scopeType === 'private' ? '私聊' : job.scopeType
  return kind ? `${kind} ${job.scopeId}` : '—'
}

function delayLabel(values: number[]): string {
  if (values.length === 0) {
    return '—'
  }
  return values
    .map((value) => (value >= 1000 ? `${Math.round(value / 1000)}s` : `${value}ms`))
    .join(' / ')
}

/**
 * Read-only extraction settings and the existing job list. Retry is limited by
 * the backend to one manual round and is offered only for failed jobs whose
 * source message is still available.
 * 只读展示抽取设置与既有任务列表；重试仅对失败且来源仍可用的任务开放，
 * 后端限制最多一次手动重试。
 */
export function ExtractionPanel() {
  const { selectedBotId, selectedAccount } = useAccountContext()
  const [status, setStatus] = useState<ExtractionJobStatus | ''>('')
  const [conversationId, setConversationId] = useState('')
  const [page, setPage] = useState(1)
  const [limit, setLimit] = useState(20)
  const [pendingRetry, setPendingRetry] = useState<ExtractionJob | null>(null)

  const settings = useExtractionSettings()
  const jobs = useExtractionJobs(selectedBotId, { status, conversationId, page, limit })
  const retry = useRetryExtractionJob()

  const changeStatus = (next: ExtractionJobStatus | '') => {
    setStatus(next)
    setPage(1)
  }
  const changeConversation = (next: string) => {
    setConversationId(next)
    setPage(1)
  }

  const runRetry = () => {
    if (!pendingRetry) {
      return
    }
    retry.mutate(pendingRetry.id, {
      onSuccess: () => {
        toast.success('抽取任务已重新排队', {
          description: '本次操作计入 1 次手动重试；失败的来源消息仍需保持可用。',
        })
        setPendingRetry(null)
      },
      onError: (error) => {
        toast.error('重试失败', { description: describeError(error).description })
      },
    })
  }

  const data = settings.data
  const rows = jobs.data?.items ?? []

  return (
    <div className="space-y-4">
      <PageHeader
        title="抽取设置与任务"
        description={`抽取默认关闭，仅处理新增消息，并按同一授权代际的小批次执行；抽取结果先进入候选，需人工审批后才生效。当前账号：${selectedAccount?.nickname || selectedBotId || '未识别'}。`}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void settings.refetch()
              void jobs.refetch()
            }}
          >
            <RefreshCw
              className={settings.isFetching || jobs.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'}
              aria-hidden
            />
            刷新
          </Button>
        }
      />

      <Card className="space-y-4 p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-sm font-semibold">抽取设置</h2>
          <Badge variant="outline">全局只读</Badge>
          <Badge variant={data?.enabled ? 'success' : 'warning'}>
            {settings.isLoading ? '读取中' : data?.enabled ? '已开启' : '未开启（默认关闭）'}
          </Badge>
        </div>
        {settings.isLoading ? <Skeleton className="h-24 w-full" /> : null}
        {settings.isError ? (
          <FeedbackState
            compact
            variant="error"
            error={settings.error}
            onRetry={() => void settings.refetch()}
          />
        ) : null}
        {data ? (
          <>
            <p className="text-xs leading-relaxed text-muted-foreground">
              作用域为 <code className="text-foreground">global</code>：参数来自进程配置{' '}
              <code className="text-foreground">{data.source || 'config'}</code>，所有账号共用；本页只读，
              不支持在线开关、写入或按账号覆盖。{data.enabled ? '当前已开启。' : '当前未开启，不会自动开始抽取。'}
            </p>
            <dl className="grid gap-2 text-xs sm:grid-cols-2 lg:grid-cols-4">
              {[
                ['并发数', formatCount(data.concurrency)],
                ['单批消息上限', formatCount(data.batchMaxMessages)],
                ['单批字符上限', formatCount(data.batchMaxRunes)],
                ['最长等待', `${formatCount(data.maxWaitMs)} ms`],
                ['轮询间隔', `${formatCount(data.pollIntervalMs)} ms`],
                ['租约时长', `${formatCount(data.leaseDurationMs)} ms`],
                ['最大尝试', formatCount(data.maxAttempts)],
                ['自动重试延迟', delayLabel(data.retryDelaysMs)],
              ].map(([label, value]) => (
                <div key={label} className="rounded-lg border border-border/70 bg-secondary/20 px-3 py-2">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="mt-1 font-mono tabular-nums text-foreground">{value}</dd>
                </div>
              ))}
            </dl>
            <div className="grid gap-2 text-xs text-muted-foreground sm:grid-cols-2 lg:grid-cols-4">
              <p>默认关闭：打开需修改进程配置，不在页面在线切换。</p>
              <p>仅新增消息：不回扫历史消息，来源被留存清理后不可重试。</p>
              <p>同代小批次：同一授权代际内的新增消息合并为一次抽取。</p>
              <p>人工审批：抽取结果先进入候选，审批后才进入生效记忆。</p>
            </div>
          </>
        ) : null}
      </Card>

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-border/60 px-5 py-3">
          <h2 className="text-sm font-semibold">抽取任务</h2>
          <Badge variant="outline">最多手动重试 1 次</Badge>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <Select
              aria-label="按任务状态筛选"
              containerClassName="w-[132px]"
              className="h-8 text-xs"
              value={status}
              onChange={(event) => changeStatus(event.target.value as ExtractionJobStatus | '')}
            >
              <option value="">全部状态</option>
              {STATUS_OPTIONS.map((value) => (
                <option key={value} value={value}>
                  {EXTRACTION_JOB_STATUS_LABELS[value]}
                </option>
              ))}
            </Select>
            <Input
              aria-label="按会话 ID 筛选"
              className="h-8 w-[190px] text-xs"
              placeholder="会话 ID"
              value={conversationId}
              onChange={(event) => changeConversation(event.target.value)}
            />
          </div>
        </div>

        {jobs.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 5 }).map((_, index) => (
              <Skeleton key={index} className="h-12 w-full" />
            ))}
          </div>
        ) : jobs.isError ? (
          <FeedbackState
            variant="error"
            error={jobs.error}
            onRetry={() => void jobs.refetch()}
          />
        ) : rows.length === 0 ? (
          <FeedbackState
            variant="empty"
            title={status || conversationId ? '没有匹配的抽取任务' : '还没有抽取任务'}
            description={
              status || conversationId
                ? '当前筛选条件下没有任务，可调整状态或清空会话 ID。'
                : '抽取开启后，新增消息会按授权代际进入任务队列。'
            }
          />
        ) : (
          <Table label="记忆抽取任务列表">
            <TableHeader>
              <TableRow>
                <TableHead>任务</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>作用域</TableHead>
                <TableHead>会话</TableHead>
                <TableHead>消息</TableHead>
                <TableHead className="text-right">尝试</TableHead>
                <TableHead>失败码</TableHead>
                <TableHead>来源</TableHead>
                <TableHead>下次运行</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((job) => {
                const blocked = retryBlockedReason(job)
                return (
                  <TableRow key={job.id}>
                    <TableCell className="font-mono text-meta">
                      <span title={job.id}>{job.id.slice(0, 8)}</span>
                      <div className="mt-0.5 text-muted-foreground">
                        {job.createdAt ? formatRelativeTime(job.createdAt) : '—'}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant={JOB_STATUS_VARIANTS[job.status]}>
                        {EXTRACTION_JOB_STATUS_LABELS[job.status]}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-meta text-muted-foreground">{scopeLabel(job)}</TableCell>
                    <TableCell className="max-w-[160px] truncate font-mono text-meta" title={job.conversationId}>
                      {job.conversationId || '—'}
                    </TableCell>
                    <TableCell className="font-mono text-meta tabular-nums">{job.messageId || '已清理'}</TableCell>
                    <TableCell className="text-right font-mono text-meta tabular-nums">
                      {job.attempts}
                      {job.roundAttempts > 0 ? <span className="text-muted-foreground"> / {job.roundAttempts}</span> : null}
                    </TableCell>
                    <TableCell className="font-mono text-meta">
                      {job.lastErrorCode ? <span className="text-warning">{job.lastErrorCode}</span> : '—'}
                    </TableCell>
                    <TableCell>
                      <Badge variant={job.sourceAvailable ? 'success' : 'warning'}>
                        {job.sourceAvailable ? '可用' : '已失效'}
                      </Badge>
                      {job.manualRetries > 0 ? (
                        <div className="mt-1 text-meta text-muted-foreground">手动 {job.manualRetries} 次</div>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-meta text-muted-foreground" title={job.nextRunAt ? formatDateTime(job.nextRunAt) : undefined}>
                      {job.nextRunAt ? formatRelativeTime(job.nextRunAt) : '—'}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end">
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-8 px-2 text-meta"
                          disabled={blocked !== '' || retry.isPending}
                          title={blocked || '重新排队该失败任务'}
                          onClick={() => setPendingRetry(job)}
                        >
                          <RotateCcw className="size-3" aria-hidden />
                          重试
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}

        {rows.length > 0 ? (
          <div className="px-5 pb-4">
            <Pagination
              total={jobs.data?.total ?? 0}
              page={page}
              limit={limit}
              pending={jobs.isFetching}
              itemLabel="个任务"
              onPageChange={setPage}
              onLimitChange={(next) => {
                setLimit(next)
                setPage(1)
              }}
            />
          </div>
        ) : null}
      </Card>

      <ConfirmDialog
        open={pendingRetry !== null}
        onOpenChange={(open) => !open && setPendingRetry(null)}
        title="重新排队该抽取任务？"
        description="仅失败且来源消息仍可用的任务可重试；每个任务最多手动重试 1 次，重复失败不会自动恢复。"
        details={
          pendingRetry
            ? [
                `任务：${pendingRetry.id}`,
                `失败码：${pendingRetry.lastErrorCode || '—'}`,
                `来源：${pendingRetry.sourceAvailable ? '可用' : '已失效'}`,
              ]
            : undefined
        }
        confirmLabel="确认重试"
        pending={retry.isPending}
        onConfirm={runRetry}
      />
    </div>
  )
}
