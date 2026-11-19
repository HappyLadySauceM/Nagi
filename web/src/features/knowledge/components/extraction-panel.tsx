import { useState, type FormEvent } from 'react'
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
  type ExtractionSettings,
} from '../../../lib/api/contracts'
import { ApiError } from '../../../lib/api/client'
import { describeError } from '../../../lib/errors'
import { formatDateTime, formatRelativeTime } from '../../../lib/format'
import { useAccountContext } from '../../accounts/account-context'
import { useExtractionJobs, useExtractionSettings, useRetryExtractionJob, useSaveExtractionSettings } from '../queries'

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

const EXTRACTION_FIELDS = [
  ['concurrency', '并发数', 1, 16],
  ['batchMaxMessages', '单批消息上限', 1, 1000],
  ['batchMaxRunes', '单批字符上限', 1, 1000000],
  ['maxWaitMs', '最长等待（毫秒）', 0, 600000],
  ['pollIntervalMs', '轮询间隔（毫秒）', 1, 60000],
  ['leaseDurationMs', '租约时长（毫秒）', 1, 1800000],
  ['maxAttempts', '最大尝试次数', 1, 10],
] as const

function ExtractionSettingsForm({ settings }: { settings: ExtractionSettings }) {
  const save = useSaveExtractionSettings()
  const [error, setError] = useState('')
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError('')
    const fields = new FormData(event.currentTarget)
    const delays = String(fields.get('retryDelaysMs')).split(/[,，]/).map((value) => value.trim())
    const retryDelaysMs = delays.map(Number)
    if (delays.some((value) => !/^[0-9]+$/.test(value)) ||
        retryDelaysMs.some((value) => !Number.isSafeInteger(value) || value < 1)) {
      setError('请输入逗号分隔的正整数重试延迟。')
      return
    }
    const next = { ...settings, enabled: fields.has('enabled'), retryDelaysMs }
    for (const [field] of EXTRACTION_FIELDS) {
      next[field] = Number(fields.get(field))
    }
    save.mutate(next, {
      onSuccess: () => toast.success('抽取设置已保存'),
      onError: (cause) => {
        if (cause instanceof ApiError && cause.status === 409) {
          setError('抽取设置已被其他页面修改，请刷新后重新编辑。')
          return
        }
        setError(describeError(cause).description)
      },
    })
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <fieldset disabled={!settings.editable || save.isPending} className="space-y-3">
        <label className="inline-flex items-center gap-2 text-sm">
          <input type="checkbox" role="switch" name="enabled" defaultChecked={settings.enabled} />
          自动抽取
        </label>
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
          {EXTRACTION_FIELDS.map(([field, label, min, max]) => (
            <label key={field} className="space-y-1 text-xs text-muted-foreground">
              <span>{label}</span>
              <Input type="number" name={field} min={min} max={max} step={1} defaultValue={settings[field]} required />
            </label>
          ))}
          <label className="space-y-1 text-xs text-muted-foreground">
            <span>重试延迟（毫秒，逗号分隔）</span>
            <Input name="retryDelaysMs" defaultValue={settings.retryDelaysMs.join(', ')} required />
          </label>
        </div>
        {settings.editable ? <Button type="submit" size="sm">{save.isPending ? '保存中…' : '保存抽取设置'}</Button> : null}
      </fieldset>
      {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
    </form>
  )
}

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
        description={`抽取按同一授权代际分批执行。关闭后暂停新任务，重新开启后继续处理积压。当前账号：${selectedAccount?.nickname || selectedBotId || '未识别'}。`}
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
          <Badge variant="outline">{data?.editable ? "全局配置" : "全局只读"}</Badge>
          <Badge variant={data?.enabled ? 'success' : 'warning'}>
            {settings.isLoading ? '读取中' : data?.enabled ? '已开启' : data?.source === 'database' ? '已暂停' : '未开启（默认关闭）'}
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
            <p className="text-xs text-muted-foreground">
              作用域为全局，所有账号共用。来源 {data.source === 'database' ? '控制台配置' : '部署配置'} · 版本 {data.version}
            </p>
            <ExtractionSettingsForm key={data.version} settings={data} />
            <p className="text-xs text-muted-foreground">
              仅处理具有有效记忆写入授权的消息。人工审批：抽取结果先进入候选，审批后才进入生效记忆。
            </p>
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
