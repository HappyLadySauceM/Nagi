import { AnimatedNavIcon } from '../../components/animated-nav-icon'
import { useState } from 'react'
import { Link, useMatchRoute } from '@tanstack/react-router'
import { Menu } from 'lucide-react'
import { Button } from '../../components/ui/button'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from '../../components/ui/sheet'
import { cn } from '../../lib/utils'
import { NAV_GROUPS } from './nav-items'
import { Brand } from './sidebar'

export function MobileNav() {
  const [open, setOpen] = useState(false)
  const matchRoute = useMatchRoute()

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="lg:hidden"
          aria-label="打开导航菜单"
        >
          <Menu className="size-4" />
        </Button>
      </SheetTrigger>
      <SheetContent side="left">
        <div className="flex h-14 items-center border-b border-sidebar-border px-4">
          <Brand />
        </div>
        <SheetTitle className="sr-only">主导航</SheetTitle>
        <SheetDescription className="sr-only">在概览、联系人、知识与配置之间切换</SheetDescription>
        <nav className="flex-1 space-y-5 overflow-y-auto px-3 py-4">
          {NAV_GROUPS.map((group) => (
            <div key={group.label}>
              <p className="px-2.5 pb-1.5 text-caption font-medium tracking-wide text-muted-foreground/70">
                {group.label}
              </p>
              <div className="space-y-1">
                {group.items.map((item) => {
                  const isActive = Boolean(matchRoute({ to: item.to, fuzzy: item.to === '/decisions' }))
                  const Icon = item.icon
                  return (
                    <SheetClose asChild key={item.to}>
                      <Link
                        to={item.to}
                        aria-current={isActive ? 'page' : undefined}
                        className={cn(
                          'flex items-center gap-2.5 rounded-lg px-2.5 py-2.5 text-body transition-colors',
                          isActive
                            ? 'bg-sidebar-accent text-sidebar-foreground'
                            : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground',
                        )}
                      >
                        <AnimatedNavIcon icon={Icon}
                          className={cn(
                            'size-4 shrink-0',
                            isActive ? 'text-primary' : 'text-muted-foreground',
                          )}
                        />
                        <span className="min-w-0 flex-1 truncate">{item.label}</span>
                        <span className="truncate text-caption text-muted-foreground">{item.hint}</span>
                      </Link>
                    </SheetClose>
                  )
                })}
              </div>
            </div>
          ))}
        </nav>
        <div className="border-t border-sidebar-border p-4">
          <p className="font-mono text-caption text-muted-foreground">v{__APP_VERSION__}</p>
        </div>
      </SheetContent>
    </Sheet>
  )
}
