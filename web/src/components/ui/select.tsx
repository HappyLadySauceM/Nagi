import type * as React from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '../../lib/utils'

interface SelectProps extends React.ComponentProps<'select'> {
  /** Width belongs to the wrapper so the chevron stays next to the control. */
  containerClassName?: string
}

/**
 * Native select in a styled wrapper: keyboard behaviour and mobile pickers come
 * for free, and no extra Radix primitive is needed.
 * 原生 select 加样式容器：键盘行为与移动端选择器直接可用，无需额外 Radix 组件。
 */
function Select({ className, containerClassName, children, ...props }: SelectProps) {
  return (
    <div className={cn('relative', containerClassName ?? 'w-full')}>
      <select
        data-slot="select"
        className={cn(
          'h-9 w-full cursor-pointer appearance-none rounded-lg border border-border bg-surface pl-3 pr-8 text-sm text-foreground transition-colors',
          'focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
          'disabled:cursor-not-allowed disabled:opacity-50',
          className,
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown
        className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
        aria-hidden
      />
    </div>
  )
}

export { Select }
