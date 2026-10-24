import type * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '../../lib/utils'

const Sheet = DialogPrimitive.Root
const SheetTrigger = DialogPrimitive.Trigger
const SheetClose = DialogPrimitive.Close

function SheetContent({
  className,
  children,
  side = 'left',
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  side?: 'left' | 'right'
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay
        className={cn(
          'fixed inset-0 z-50 bg-black/60 backdrop-blur-sm',
          'data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0',
        )}
      />
      <DialogPrimitive.Content
        data-slot="sheet-content"
        className={cn(
          'fixed inset-y-0 z-50 flex h-full w-[268px] flex-col border-border bg-sidebar text-sidebar-foreground shadow-float',
          'transition ease-quiet data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:duration-200 data-[state=open]:duration-300',
          side === 'left'
            ? 'left-0 border-r data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left'
            : 'right-0 border-l data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right',
          className,
        )}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          className="absolute right-3 top-4 cursor-pointer rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground"
          aria-label="关闭导航"
        >
          <X className="size-4" />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn('text-sm font-medium', className)} {...props} />
}

function SheetDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      className={cn('text-xs text-muted-foreground', className)}
      {...props}
    />
  )
}

export { Sheet, SheetTrigger, SheetClose, SheetContent, SheetTitle, SheetDescription }
