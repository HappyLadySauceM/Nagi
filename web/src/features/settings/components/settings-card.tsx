import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../../../components/ui/card'
import { cn } from '../../../lib/utils'

interface SettingsCardProps {
  id: string
  title: string
  description: string
  icon: LucideIcon
  tag?: string
  warning?: string
  children: ReactNode
}

/**
 * One settings group. The card is the scroll anchor used by the section nav, so
 * its id must match the nav item.
 * 单个配置分组。卡片是分区导航的滚动锚点，因此 id 必须与导航项一致。
 */
export function SettingsCard({
  id,
  title,
  description,
  icon: Icon,
  tag,
  warning,
  children,
}: SettingsCardProps) {
  return (
    <Card id={id} className="scroll-mt-24 overflow-hidden">
      <CardHeader className="border-b border-border/60 bg-surface/40">
        <div className="flex items-start gap-3">
          <span className={cn('mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg border', id === 'tools' ? 'border-cyan-500/20 bg-cyan-500/10 text-cyan-700 dark:text-cyan-400' : id === 'retention' ? 'border-border bg-secondary text-muted-foreground' : 'border-violet-500/20 bg-violet-500/10 text-violet-600 dark:text-violet-400')}>
            <Icon className="size-4" aria-hidden />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <CardTitle>{title}</CardTitle>
              {tag ? (
                <span className="rounded-full border border-border bg-secondary px-2 py-0.5 text-caption text-muted-foreground">
                  {tag}
                </span>
              ) : null}
            </div>
            <CardDescription className="mt-1">{description}</CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent className={cn('pt-2')}>
        {warning ? (
          <p className="mb-2 rounded-lg border border-warning/25 bg-warning/10 px-3 py-2 text-meta leading-relaxed text-warning">
            {warning}
          </p>
        ) : null}
        {children}
      </CardContent>
    </Card>
  )
}
