import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from './ui/button'
import { Select } from './ui/select'
import { formatCount } from '../lib/format'
import { PAGE_SIZE_OPTIONS } from '../lib/search-params'
import { cn } from '../lib/utils'

interface PaginationProps {
  total: number
  page: number
  limit: number
  onPageChange: (page: number) => void
  onLimitChange: (limit: number) => void
  /** Server-side paging: values come from the request, never from the response echo. */
  pending?: boolean
  itemLabel?: string
  className?: string
}

export function Pagination({
  total,
  page,
  limit,
  onPageChange,
  onLimitChange,
  pending = false,
  itemLabel = '条',
  className,
}: PaginationProps) {
  const totalPages = Math.max(1, Math.ceil(total / Math.max(1, limit)))
  const canPrevious = page > 1 && !pending
  const canNext = page < totalPages && !pending

  return (
    <div
      className={cn(
        'flex flex-col gap-2 border-t border-border/60 pt-3 sm:flex-row sm:items-center sm:justify-between',
        className,
      )}
    >
      <p className="whitespace-nowrap text-[11px] text-muted-foreground tabular-nums">
        共 {formatCount(total)} {itemLabel} · 第 {page} / {totalPages} 页
      </p>
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1.5 whitespace-nowrap text-[11px] text-muted-foreground">
          每页
          <Select
            aria-label="每页条数"
            containerClassName="w-[68px]"
            className="h-8 pl-2 pr-6 text-[11px]"
            value={String(limit)}
            onChange={(event) => onLimitChange(Number(event.target.value))}
            disabled={pending}
          >
            {PAGE_SIZE_OPTIONS.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </Select>
        </label>
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => onPageChange(page - 1)}
          disabled={!canPrevious}
          aria-label="上一页"
        >
          <ChevronLeft className="size-3.5" aria-hidden />
        </Button>
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => onPageChange(page + 1)}
          disabled={!canNext}
          aria-label="下一页"
        >
          <ChevronRight className="size-3.5" aria-hidden />
        </Button>
      </div>
    </div>
  )
}
