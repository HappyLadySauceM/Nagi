import { AnimatedNavIcon } from '../../../components/animated-nav-icon'
import type { LucideIcon } from 'lucide-react'
import { cn } from '../../../lib/utils'

export interface SectionNavItem {
  id: string
  label: string
  hint: string
  icon: LucideIcon
}

interface SectionNavProps {
  items: SectionNavItem[]
  activeId: string
  onSelect: (id: string) => void
}

/**
 * Desktop section navigation. It stays sticky next to the cards and collapses
 * into a horizontal segmented control on narrow screens.
 * 桌面端分区导航：在卡片旁吸附，窄屏折叠为横向分段控件。
 */
export function SectionNav({ items, activeId, onSelect }: SectionNavProps) {
  return (
    <>
      <nav
        aria-label="配置分区"
        className="sticky top-20 hidden w-56 shrink-0 flex-col gap-1 lg:flex"
      >
        {items.map((item) => {
          const active = item.id === activeId
          return (
            <button
              key={item.id}
              type="button"
              aria-current={active ? 'true' : undefined}
              onClick={() => onSelect(item.id)}
              className={cn(
                'group flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors duration-150',
                active
                  ? 'border-primary/35 bg-primary/10'
                  : 'border-transparent hover:border-border hover:bg-secondary/50',
              )}
            >
              <AnimatedNavIcon icon={item.icon}
                className={cn(
                  'mt-0.5 size-4 shrink-0',
                  active ? 'text-primary' : 'text-muted-foreground',
                )}
              />
              <span className="min-w-0">
                <span
                  className={cn(
                    'block text-xs font-medium',
                    active ? 'text-primary' : 'text-foreground',
                  )}
                >
                  {item.label}
                </span>
                <span className="mt-0.5 block text-caption leading-relaxed text-muted-foreground">
                  {item.hint}
                </span>
              </span>
            </button>
          )
        })}
      </nav>

      <div className="-mx-1 flex gap-1 overflow-x-auto pb-1 lg:hidden">
        {items.map((item) => {
          const active = item.id === activeId
          return (
            <button
              key={item.id}
              type="button"
              aria-current={active ? 'true' : undefined}
              onClick={() => onSelect(item.id)}
              className={cn(
                'flex shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1.5 text-meta transition-colors duration-150',
                active
                  ? 'border-primary/40 bg-primary/10 text-primary'
                  : 'border-border bg-card text-muted-foreground',
              )}
            >
              <AnimatedNavIcon icon={item.icon} className="size-3.5" />
              {item.label}
            </button>
          )
        })}
      </div>
    </>
  )
}
