import { RefreshCw } from 'lucide-react'
import { FeedbackState } from '../../../components/feedback-state'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { Skeleton } from '../../../components/ui/skeleton'
import { formatCount } from '../../../lib/format'
import { cn } from '../../../lib/utils'
import { useKnowledgeSummary } from '../queries'

const PROMPT_NAMES = {
  'chat.reply': '聊天回复',
  'intent.participation': '意图判断',
  'memory.extraction': '记忆抽取',
} as const

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

/**
 * Count-only summary for the knowledge page. It never receives memory text, so
 * it is safe to render above the list for every account.
 * 知识页只读计数摘要；不接收记忆正文，可安全展示在列表上方。
 */
export function KnowledgeSummaryCards({ botId }: { botId?: number }) {
  const summary = useKnowledgeSummary(botId)

  if (summary.isLoading) {
    return (
      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <Skeleton key={index} className="h-20 w-full" />
        ))}
      </div>
    )
  }
  if (summary.isError) {
    return (
      <Card className="p-3">
        <FeedbackState
          compact
          variant="error"
          error={summary.error}
          onRetry={() => void summary.refetch()}
        />
      </Card>
    )
  }
  if (!summary.data) {
    return null
  }

  const data = summary.data
  const cards = [
    {
      label: '记忆总数',
      value: formatCount(data.memories.total),
      hint: `全局 ${formatCount(data.memories.byVisibility.bot_shared)} · 群级 ${formatCount(data.memories.byVisibility.group)} · 个人 ${formatCount(data.memories.byVisibility.private_user)} · 会话 ${formatCount(data.memories.byVisibility.conversation)}`,
    },
    { label: '生效', value: formatCount(data.memories.active), hint: '可被会话按可见性召回' },
    { label: '候选', value: formatCount(data.memories.candidate), hint: '需审批后才生效' },
    { label: '已替代', value: formatCount(data.memories.superseded), hint: '保留历史，不再召回' },
  ]

  return (
    <div className="space-y-2">
      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <Card key={card.label} className="p-3">
            <div className="text-xs text-muted-foreground">{card.label}</div>
            <div className="mt-1 text-xl font-semibold tabular-nums text-foreground">{card.value}</div>
            <div className="mt-1 line-clamp-2 text-meta text-muted-foreground">{card.hint}</div>
          </Card>
        ))}
      </div>
      <Card className="flex flex-col gap-2 px-3 py-2 text-meta text-muted-foreground sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-4">
        <span className="flex flex-wrap items-center gap-1.5">
          抽取：
          <Badge variant={data.extraction.enabled ? 'success' : 'warning'}>
            {data.extraction.enabled ? '已开启' : '未开启'}
          </Badge>
          <span className="tabular-nums">
            待处理 {formatCount(data.extraction.pendingJobs)} · 处理中 {formatCount(data.extraction.processingJobs)} · 失败 {formatCount(data.extraction.failedJobs)}
          </span>
          {data.extraction.lastErrorCode ? (
            <span className="font-mono text-warning">{data.extraction.lastErrorCode}</span>
          ) : null}
        </span>
        <span className="hidden h-3 w-px bg-border sm:block" />
        <span className="flex flex-wrap items-center gap-2">
          Prompt：
          {(Object.keys(PROMPT_NAMES) as (keyof typeof PROMPT_NAMES)[]).map((promptId) => {
            const item = data.prompts[promptId]
            return (
              <span key={promptId} className="flex items-center gap-1">
                {PROMPT_NAMES[promptId]}
                <span className={cn('font-mono tabular-nums', item.activeVersion > 0 ? 'text-foreground' : '')}>
                  v{item.activeVersion || '—'}
                </span>
                <span className="text-muted-foreground">· {sourceLabel(item.source)}</span>
              </span>
            )
          })}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2 text-meta sm:ml-auto"
          onClick={() => void summary.refetch()}
        >
          <RefreshCw className="size-3" aria-hidden />
          刷新摘要
        </Button>
      </Card>
    </div>
  )
}
