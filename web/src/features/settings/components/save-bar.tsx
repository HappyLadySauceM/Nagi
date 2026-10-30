import { AlertTriangle, CheckCircle2, Loader2, RotateCcw, Undo2 } from 'lucide-react'
import { Button } from '../../../components/ui/button'
import { cn } from '../../../lib/utils'

interface SaveBarProps {
  dirtyCount: number
  errorCount: number
  saving: boolean
  resetting: boolean
  message: { tone: 'success' | 'warning'; text: string } | null
  onSave: () => void
  onDiscard: () => void
  onReset: () => void
}

/**
 * Sticky action bar: the change count and error count stay visible while the
 * operator scrolls, so a half-finished edit can never be mistaken for saved.
 * 吸附式操作条：变更数与错误数始终可见，
 * 半途而废的编辑不会被误认为已保存。
 */
export function SaveBar({
  dirtyCount,
  errorCount,
  saving,
  resetting,
  message,
  onSave,
  onDiscard,
  onReset,
}: SaveBarProps) {
  const busy = saving || resetting
  return (
    <div className="sticky bottom-3 z-20 mt-4">
      <div className="flex flex-col gap-3 rounded-xl border border-border/80 bg-card/92 p-3 shadow-lg shadow-black/5 backdrop-blur md:flex-row md:items-center md:justify-between">
        <div className="min-w-0 px-1" aria-live="polite">
          <div className="flex flex-wrap items-center gap-2">
            {dirtyCount > 0 ? (
              <span className="inline-flex items-center gap-1.5 rounded-full border border-warning/30 bg-warning/10 px-2.5 py-1 text-caption text-warning">
                <span className="size-1.5 animate-pulse rounded-full bg-warning" aria-hidden />
                有 {dirtyCount} 项未保存
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-secondary px-2.5 py-1 text-caption text-muted-foreground">
                已是最新配置
              </span>
            )}
            {errorCount > 0 ? (
              <span className="inline-flex items-center gap-1.5 rounded-full border border-destructive/30 bg-destructive/10 px-2.5 py-1 text-caption text-destructive">
                <AlertTriangle className="size-3" aria-hidden />
                {errorCount} 项需要修正
              </span>
            ) : null}
            {message ? (
              <span
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-caption',
                  message.tone === 'success'
                    ? 'border border-success/30 bg-success/10 text-success'
                    : 'border border-warning/30 bg-warning/10 text-warning',
                )}
              >
                {message.tone === 'success' ? (
                  <CheckCircle2 className="size-3" aria-hidden />
                ) : (
                  <AlertTriangle className="size-3" aria-hidden />
                )}
                {message.text}
              </span>
            ) : null}
          </div>
          <p className="mt-1 text-caption text-muted-foreground">
            保存后对后续会话生效，进行中的运行不受影响。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="ghost" size="sm" onClick={onReset} disabled={busy}>
            <RotateCcw className="size-3.5" aria-hidden />
            恢复默认
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onDiscard}
            disabled={busy || dirtyCount === 0}
          >
            <Undo2 className="size-3.5" aria-hidden />
            放弃修改
          </Button>
          <Button size="sm" onClick={onSave} disabled={busy || dirtyCount === 0 || errorCount > 0}>
            {saving ? <Loader2 className="size-3.5 animate-spin" aria-hidden /> : null}
            {saving ? '保存中' : '保存配置'}
          </Button>
        </div>
      </div>
    </div>
  )
}
