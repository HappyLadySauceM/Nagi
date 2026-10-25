import type { DirectoryTab } from '../../../lib/search-params'
import { cn } from '../../../lib/utils'

interface DirectoryTabsProps {
  tab: DirectoryTab
  onTabChange: (tab: DirectoryTab) => void
  friendCount?: number
  groupCount?: number
}

const TAB_LABELS: Record<DirectoryTab, string> = {
  friends: '好友',
  groups: '群聊',
}

/**
 * Friends and groups are two views of one workspace, so a segmented control is
 * clearer than two top-level pages. Implemented with plain buttons to keep the
 * keyboard and screen-reader semantics explicit without another dependency.
 * 好友与群聊是同一工作区的两个视图，分段控件比两个顶级页面更清晰。
 * 使用原生按钮实现，保留明确的键盘与读屏语义，且不新增依赖。
 */
export function DirectoryTabs({ tab, onTabChange, friendCount, groupCount }: DirectoryTabsProps) {
  const tabs: DirectoryTab[] = ['friends', 'groups']

  return (
    <div
      role="tablist"
      aria-label="联系人类型"
      className="flex items-center gap-1 rounded-xl border border-border/70 bg-secondary/40 p-1"
    >
      {tabs.map((value) => {
        const active = value === tab
        const count = value === 'friends' ? friendCount : groupCount
        return (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onTabChange(value)}
            className={cn(
              'flex flex-1 cursor-pointer items-center justify-center gap-2 rounded-lg px-3 py-2 text-body transition-colors duration-150',
              active
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {TAB_LABELS[value]}
            {count !== undefined ? (
              <span
                className={cn(
                  'rounded-full px-1.5 py-0.5 font-mono text-caption tabular-nums',
                  active ? 'bg-primary/12 text-primary' : 'bg-secondary text-muted-foreground',
                )}
              >
                {count}
              </span>
            ) : null}
          </button>
        )
      })}
    </div>
  )
}
