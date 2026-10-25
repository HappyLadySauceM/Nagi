import type { ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { Button } from '../../../components/ui/button'
import { SearchField } from '../../../components/search-field'
import { formatCount } from '../../../lib/format'
import { cn } from '../../../lib/utils'

interface DirectoryToolbarProps {
  searchLabel: string
  searchPlaceholder: string
  searchValue: string
  onSearchChange: (value: string) => void
  total?: number
  totalLabel: string
  syncLabel?: string
  onSync?: () => void
  syncing?: boolean
  /** Extra filters rendered next to the search field. */
  children?: ReactNode
  className?: string
}

export function DirectoryToolbar({
  searchLabel,
  searchPlaceholder,
  searchValue,
  onSearchChange,
  total,
  totalLabel,
  syncLabel = '同步目录',
  onSync,
  syncing = false,
  children,
  className,
}: DirectoryToolbarProps) {
  return (
    <div
      className={cn(
        'flex flex-col gap-3 border-b border-border/60 px-5 py-4 lg:flex-row lg:items-center lg:justify-between',
        className,
      )}
    >
      <div className="flex w-full flex-col gap-2 sm:flex-row sm:items-center">
        <SearchField
          label={searchLabel}
          placeholder={searchPlaceholder}
          value={searchValue}
          onChange={onSearchChange}
          disabled={syncing}
        />
        {children}
      </div>
      <div className="flex items-center justify-between gap-3 lg:justify-end">
        <span className="whitespace-nowrap text-meta text-muted-foreground tabular-nums">
          {total === undefined ? '总数不可获取' : `共 ${formatCount(total)} ${totalLabel}`}
        </span>
        {onSync ? (
          <Button size="sm" onClick={onSync} disabled={syncing}>
            <RefreshCw className={cn('size-3.5', syncing && 'animate-spin')} aria-hidden />
            {syncing ? '同步中…' : syncLabel}
          </Button>
        ) : null}
      </div>
    </div>
  )
}
