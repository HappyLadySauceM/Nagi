import { BookOpen, ListChecks, ScrollText } from 'lucide-react'
import type { MemorySearch } from '../../../lib/search-params'
import { cn } from '../../../lib/utils'

type MemoryTab = MemorySearch['tab']

const TABS: { value: MemoryTab; label: string; icon: typeof ListChecks }[] = [
  { value: 'entries', label: '记忆条目', icon: ListChecks },
  { value: 'prompts', label: '角色与提示词', icon: BookOpen },
  { value: 'extraction', label: '抽取设置与任务', icon: ScrollText },
]

interface MemoryTabsProps {
  tab: MemoryTab
  onTabChange: (tab: MemoryTab) => void
}

/**
 * Three views of the knowledge-and-memory page share one URL parameter so a
 * reload or a shared link restores the exact entry.
 * 知识与记忆的三个视图共用一个 URL 参数，刷新或分享后可恢复同一入口。
 */
export function MemoryTabs({ tab, onTabChange }: MemoryTabsProps) {
  return (
    <div
      role="tablist"
      aria-label="知识与记忆入口"
      className="flex gap-1 overflow-x-auto rounded-xl border border-border/70 bg-secondary/40 p-1"
    >
      {TABS.map(({ value, label, icon: Icon }) => {
        const active = value === tab
        return (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onTabChange(value)}
            className={cn(
              'flex flex-1 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-lg px-3 py-2 text-body transition-colors duration-150',
              active
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            <Icon className="size-4" aria-hidden />
            {label}
          </button>
        )
      })}
    </div>
  )
}
