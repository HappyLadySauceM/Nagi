import { cn } from '../lib/utils'

interface InfoRowProps {
  label: string
  value: string
  mono?: boolean
}

/**
 * Label/value row shared by the connection and account cards; the truncated
 * value keeps long ids readable through its title attribute.
 * 连接卡与账号卡共用的标签-数值行；过长内容截断并通过 title 提供完整值。
 */
export function InfoRow({ label, value, mono = false }: InfoRowProps) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-border/60 py-2 last:border-b-0">
      <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
      <span
        className={cn('truncate text-xs text-foreground', mono && 'font-mono tabular-nums')}
        title={value}
      >
        {value}
      </span>
    </div>
  )
}
