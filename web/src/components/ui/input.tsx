import type * as React from 'react'
import { cn } from '../../lib/utils'

function Input({ className, type = 'text', ...props }: React.ComponentProps<'input'>) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        'flex h-10 w-full rounded-lg border border-border bg-surface px-3 py-1 text-sm text-foreground transition-colors',
        'placeholder:text-muted-foreground/70',
        'focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-[invalid=true]:border-destructive/60 aria-[invalid=true]:ring-destructive/20',
        className,
      )}
      {...props}
    />
  )
}

export { Input }
