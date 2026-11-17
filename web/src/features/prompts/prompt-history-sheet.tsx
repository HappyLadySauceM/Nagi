import { Plus, RotateCcw, Trash2 } from 'lucide-react'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../components/ui/sheet'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import type { ManagedPromptDetail, ManagedPromptScope, ManagedPromptVersion } from '../../lib/api/contracts'
import { formatDateTime } from '../../lib/format'
import { PROMPT_SOURCE_LABELS, PROMPT_STATUS_LABELS } from './prompt-content'

const STATUS_VARIANTS: Record<ManagedPromptVersion['status'], 'success' | 'warning' | 'secondary'> = {
  published: 'success',
  draft: 'warning',
  archived: 'secondary',
}

/**
 * Version history drawer: the old version table moved off the page, keeping
 * delete/rollback/preview plus a shortcut to create a new draft.
 * 版本历史抽屉：原版本表格移出页面，保留删除/回滚/预览，并提供新建草稿入口。
 */
export function PromptHistorySheet({
  open,
  onOpenChange,
  detail,
  scope,
  onDelete,
  onRollback,
  onPreview,
  onCreateDraft,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  detail: ManagedPromptDetail
  scope: ManagedPromptScope
  onDelete: (version: ManagedPromptVersion) => void
  onRollback: (version: ManagedPromptVersion) => void
  onPreview: (version: ManagedPromptVersion) => void
  onCreateDraft: () => void
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full space-y-4 overflow-y-auto bg-background p-6 sm:max-w-3xl">
        <div className="flex items-start justify-between gap-3 border-b border-border pb-4 pr-8">
          <div>
            <SheetTitle className="text-lg font-semibold">历史版本</SheetTitle>
            <SheetDescription className="mt-2 leading-relaxed">
              查看、预览与清理历史版本；仅草稿或已归档版本可删除，仅已归档且发布过的版本可回滚。
            </SheetDescription>
          </div>
          <Button size="sm" onClick={onCreateDraft}>
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
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {scope === 'account' ? (
                <TableRow>
                  <TableCell className="font-mono">v{detail.builtin.version}</TableCell>
                  <TableCell>
                    <Badge variant="secondary">内置</Badge>
                  </TableCell>
                  <TableCell className="text-meta text-muted-foreground">内置</TableCell>
                  <TableCell className="text-meta text-muted-foreground">—</TableCell>
                  <TableCell />
                </TableRow>
              ) : null}
              {detail.versions.map((version) => (
                <TableRow key={version.id}>
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
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button type="button" variant="ghost" size="sm" onClick={() => onPreview(version)}>
                        预览
                      </Button>
                      {version.status === 'archived' && version.publishedAt ? (
                        <Button type="button" variant="ghost" size="sm" onClick={() => onRollback(version)}>
                          <RotateCcw className="size-3.5" aria-hidden />
                          回滚
                        </Button>
                      ) : null}
                      {version.status === 'draft' || version.status === 'archived' ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={`删除 Prompt v${version.version}`}
                          onClick={() => onDelete(version)}
                        >
                          <Trash2 className="size-3.5" aria-hidden />
                          删除
                        </Button>
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        {detail.versions.length === 0 ? (
          <p className="text-xs text-muted-foreground">暂无本地版本，当前使用内置默认或空补充。可新建草稿后编辑。</p>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
