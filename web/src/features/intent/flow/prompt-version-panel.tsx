import { useEffect, useState } from 'react'
import { BookOpen, RotateCcw, Upload } from 'lucide-react'
import { toast } from 'sonner'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { FeedbackState } from '../../../components/feedback-state'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { Skeleton } from '../../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/table'
import type { PromptSummary, PromptVersion } from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { cn } from '../../../lib/utils'
import {
  usePromptDetail,
  usePrompts,
  usePublishPromptVersion,
  useRollbackPromptVersion,
} from './queries'

const PROMPT_STATUS_LABELS: Record<PromptVersion['status'], string> = {
  draft: '草稿',
  published: '已发布',
  archived: '已归档',
}

const SOURCE_LABELS: Record<string, string> = {
  builtin: '内置',
  local: '本地',
  langfuse: 'Langfuse（可选）',
}

function contentText(content: unknown): string {
  if (typeof content === 'string') return content
  try {
    return JSON.stringify(content, null, 2)
  } catch {
    return String(content)
  }
}

/** Local Prompt library: view versions, publish drafts and roll back. No LF login required. */
export function PromptVersionPanel() {
  const prompts = usePrompts()
  const [selectedPromptId, setSelectedPromptId] = useState<string | null>(null)
  const detail = usePromptDetail(selectedPromptId)
  const publish = usePublishPromptVersion()
  const rollback = useRollbackPromptVersion(selectedPromptId ?? '')
  const [selectedVersionId, setSelectedVersionId] = useState<number | null>(null)
  const [pendingAction, setPendingAction] = useState<{ kind: 'publish' | 'rollback'; version: PromptVersion } | null>(null)

  useEffect(() => {
    if (selectedPromptId === null && prompts.data && prompts.data.length > 0) {
      setSelectedPromptId(prompts.data[0].promptId)
    }
  }, [prompts.data, selectedPromptId])

  useEffect(() => {
    if (selectedVersionId === null && detail.data && detail.data.versions.length > 0) {
      setSelectedVersionId(detail.data.versions[0].id)
    }
  }, [detail.data, selectedVersionId])

  if (prompts.isError) {
    return <FeedbackState variant="error" title="读取 Prompt 列表失败" error={prompts.error} onRetry={() => void prompts.refetch()} />
  }

  const runAction = () => {
    if (!pendingAction) return
    if (pendingAction.kind === 'publish') {
      publish.mutate(pendingAction.version.id, {
        onSuccess: () => {
          toast.success('Prompt 版本已发布')
          setPendingAction(null)
        },
        onError: (error) => toast.error(describeError(error).title),
      })
    } else {
      rollback.mutate(pendingAction.version.version, {
        onSuccess: () => {
          toast.success(`已回滚到 v${pendingAction.version.version}`)
          setPendingAction(null)
        },
        onError: (error) => toast.error(describeError(error).title),
      })
    }
  }

  const selectedVersion = detail.data?.versions.find((version) => version.id === selectedVersionId)

  return (
    <Card className="space-y-4 p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold"><BookOpen className="size-4" aria-hidden />本地 Prompt 版本</h2>
          <p className="mt-1 text-meta text-muted-foreground">
            查看、发布和回滚本地 Prompt；不依赖 Langfuse 登录或凭证。模型调用时会记录实际 Prompt 版本。
          </p>
        </div>
      </div>
      {prompts.isLoading ? <Skeleton className="h-24 w-full" /> : null}
      {prompts.data && prompts.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">暂无本地 Prompt，后端使用内置版本。</p>
      ) : null}
      {prompts.data && prompts.data.length > 0 ? (
        <div className="flex flex-wrap gap-2">
          {prompts.data.map((prompt) => (
            <Button
              key={prompt.promptId}
              type="button"
              size="sm"
              variant={selectedPromptId === prompt.promptId ? 'secondary' : 'outline'}
              onClick={() => {
                setSelectedPromptId(prompt.promptId)
                setSelectedVersionId(null)
              }}
            >
              {prompt.name || prompt.promptId}
              <Badge variant="outline" className="ml-1">{(SOURCE_LABELS[prompt.source] ?? prompt.source) || '本地'}</Badge>
            </Button>
          ))}
        </div>
      ) : null}
      {selectedPromptId ? (
        detail.isLoading ? <Skeleton className="h-32 w-full" /> : detail.isError ? (
          <FeedbackState variant="error" title="读取 Prompt 版本失败" error={detail.error} onRetry={() => void detail.refetch()} />
        ) : (
          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
            <div className="overflow-hidden rounded-lg border border-border">
              <Table label="Prompt 版本列表">
                <TableHeader>
                  <TableRow>
                    <TableHead>版本</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>来源</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.data?.versions.map((version) => (
                    <TableRow key={version.id} className={cn('cursor-pointer', selectedVersionId === version.id && 'bg-secondary/60')} onClick={() => setSelectedVersionId(version.id)}>
                      <TableCell className="font-mono">v{version.version}</TableCell>
                      <TableCell><Badge variant={version.status === 'published' ? 'success' : version.status === 'draft' ? 'warning' : 'secondary'}>{PROMPT_STATUS_LABELS[version.status]}</Badge></TableCell>
                      <TableCell className="text-meta text-muted-foreground">{(SOURCE_LABELS[version.source] ?? version.source) || '—'}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          {version.status === 'draft' ? (
                            <Button size="icon-sm" variant="ghost" aria-label={`发布 Prompt v${version.version}`} onClick={(event) => { event.stopPropagation(); setPendingAction({ kind: 'publish', version }) }}>
                              <Upload className="size-3.5" aria-hidden />
                            </Button>
                          ) : null}
                          {version.status === 'archived' ? (
                            <Button size="icon-sm" variant="ghost" aria-label={`回滚到 Prompt v${version.version}`} onClick={(event) => { event.stopPropagation(); setPendingAction({ kind: 'rollback', version }) }}>
                              <RotateCcw className="size-3.5" aria-hidden />
                            </Button>
                          ) : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="min-w-0 rounded-lg border border-border p-4">
              {selectedVersion ? (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold">{selectedVersion.name || selectedVersion.promptId} · v{selectedVersion.version}</h3>
                    <Badge variant={selectedVersion.status === 'published' ? 'success' : 'secondary'}>{PROMPT_STATUS_LABELS[selectedVersion.status]}</Badge>
                  </div>
                  <p className="mt-2 text-meta text-muted-foreground">
                    Provider：{selectedVersion.provider || '—'} · Hash：<span className="font-mono">{selectedVersion.hash || '—'}</span>
                    {selectedVersion.sourceRef ? ` · 来源：${selectedVersion.sourceRef}` : ''}
                  </p>
                  <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 text-xs leading-relaxed">{contentText(selectedVersion.content)}</pre>
                  {selectedVersion.note ? <p className="mt-2 text-xs text-muted-foreground">备注：{selectedVersion.note}</p> : null}
                </>
              ) : (
                <p className="text-sm text-muted-foreground">选择左侧一个版本查看内容。</p>
              )}
            </div>
          </div>
        )
      ) : null}
      <Sheet open={pendingAction !== null} onOpenChange={(open) => { if (!open) setPendingAction(null) }}>
        <SheetContent side="right" className="w-full p-6 sm:max-w-md">
          <SheetTitle>{pendingAction?.kind === 'publish' ? '发布 Prompt 版本？' : '回滚 Prompt 版本？'}</SheetTitle>
          <SheetDescription className="mt-2 leading-relaxed">
            {pendingAction?.kind === 'publish'
              ? '发布后该版本会用于新的模型调用；历史决策仍保留原 Prompt 版本记录。'
              : `回滚到 v${pendingAction?.version.version ?? ''}；新调用会使用该版本，历史记录不变。`}
          </SheetDescription>
          <div className="mt-6 flex justify-end gap-2">
            <Button variant="outline" onClick={() => setPendingAction(null)} disabled={publish.isPending || rollback.isPending}>取消</Button>
            <Button onClick={runAction} disabled={publish.isPending || rollback.isPending}>
              {publish.isPending || rollback.isPending ? '处理中…' : pendingAction?.kind === 'publish' ? '确认发布' : '确认回滚'}
            </Button>
          </div>
        </SheetContent>
      </Sheet>
    </Card>
  )
}

export type { PromptSummary }
