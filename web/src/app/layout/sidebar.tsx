import { Link, useMatchRoute } from '@tanstack/react-router'
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { Button } from '../../components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '../../components/ui/tooltip'
import { cn } from '../../lib/utils'
import { NAV_ITEMS, type NavItem } from './nav-items'

export function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-2.5">
      <span
        className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-primary to-primary/60 text-[13px] font-semibold text-primary-foreground shadow-[0_6px_18px_-8px_hsl(var(--primary))]"
        aria-hidden
      >
        Q
      </span>
      {compact ? null : (
        <span className="min-w-0">
          <span className="block truncate text-[13px] font-semibold leading-tight text-sidebar-foreground">
            QQBot
          </span>
          <span className="block truncate text-[10px] leading-tight text-muted-foreground">
            Agent Control Center
          </span>
        </span>
      )}
    </div>
  )
}

function SidebarLink({
  item,
  collapsed,
}: {
  item: NavItem
  collapsed: boolean
}) {
  const matchRoute = useMatchRoute()
  const reduceMotion = useReducedMotion()
  const isActive = Boolean(matchRoute({ to: item.to, fuzzy: false }))
  const Icon = item.icon

  const link = (
    <Link
      to={item.to}
      aria-current={isActive ? 'page' : undefined}
      className={cn(
        'group relative flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] transition-colors duration-150',
        isActive
          ? 'bg-sidebar-accent text-sidebar-foreground'
          : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground',
        collapsed && 'justify-center px-0',
      )}
    >
      {isActive ? (
        <motion.span
          layoutId={reduceMotion ? undefined : 'sidebar-active-indicator'}
          className="absolute left-0 top-1/2 h-4 w-[2px] -translate-y-1/2 rounded-full bg-primary"
          aria-hidden
        />
      ) : null}
      <Icon
        className={cn('size-4 shrink-0', isActive ? 'text-primary' : 'text-muted-foreground')}
        aria-hidden
      />
      {collapsed ? null : <span className="truncate">{item.label}</span>}
    </Link>
  )

  if (!collapsed) {
    return link
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right">
        {item.label} · {item.hint}
      </TooltipContent>
    </Tooltip>
  )
}

interface SidebarProps {
  collapsed: boolean
  onToggle: () => void
}

export function Sidebar({ collapsed, onToggle }: SidebarProps) {
  return (
    <aside
      className={cn(
        'fixed inset-y-0 left-0 z-40 hidden flex-col border-r border-sidebar-border bg-sidebar transition-[width] duration-200 ease-quiet lg:flex',
        collapsed ? 'w-[72px]' : 'w-[248px]',
      )}
      aria-label="主导航"
    >
      <div
        className={cn(
          'flex h-[60px] items-center border-b border-sidebar-border',
          collapsed ? 'justify-center px-2' : 'px-4',
        )}
      >
        <Brand compact={collapsed} />
      </div>

      <nav className="flex-1 space-y-1 px-3 py-4">
        {NAV_ITEMS.map((item) => (
          <SidebarLink key={item.to} item={item} collapsed={collapsed} />
        ))}
      </nav>

      <div
        className={cn(
          'flex items-center gap-2 border-t border-sidebar-border p-3',
          collapsed ? 'flex-col' : 'justify-between',
        )}
      >
        {collapsed ? null : (
          <span className="font-mono text-[10px] text-muted-foreground">v{__APP_VERSION__}</span>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onToggle}
              aria-label={collapsed ? '展开侧边栏' : '折叠侧边栏'}
              className="text-muted-foreground hover:text-sidebar-foreground"
            >
              {collapsed ? (
                <PanelLeftOpen className="size-4" aria-hidden />
              ) : (
                <PanelLeftClose className="size-4" aria-hidden />
              )}
            </Button>
          </TooltipTrigger>
          <TooltipContent side="right">{collapsed ? '展开侧边栏' : '折叠侧边栏'}</TooltipContent>
        </Tooltip>
      </div>
    </aside>
  )
}
