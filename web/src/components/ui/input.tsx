import type * as React from 'react'
import { cn } from '../../lib/utils'

function Input({ className, type = 'text', ...props }: React.ComponentProps<'input'>) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        'flex h-9 w-full rounded-lg border border-border bg-surface/60 px-3 py-1 text-xs text-foreground shadow-sm transition-colors',
        'placeholder:text-muted-foreground/70',
        'focus-visible:border-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-[invalid=true]:border-destructive/60 aria-[invalid=true]:ring-destructive/20',
        className,
      )}
      {...props}
    />
  )
}

export { Input }
