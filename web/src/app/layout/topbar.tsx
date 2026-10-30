import { useQueryClient } from '@tanstack/react-query'
import { useRouterState } from '@tanstack/react-router'
import { ChevronRight, RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { StatusPill, type StatusTone } from '../../components/status-indicator'
import { ThemeToggle } from '../../components/theme-toggle'
import { ApiError } from '../../lib/api/client'
import { cn } from '../../lib/utils'
import { AccountSwitcher } from '../../features/accounts/components/account-switcher'
import { useAccountContext } from '../../features/accounts/account-context'
import { SYSTEM_QUERY_KEYS, useLivez } from '../../features/system/queries'
import { MobileNav } from './mobile-nav'

const PAGE_TITLES: Record<string, string> = {
  '/dashboard': '概览',
  '/directory': '联系人',
  '/accounts': '账号连接',
  '/memories': '知识与记忆',
  '/decisions': '意图识别',
  '/decisions/history': '决策记录',
  '/rules': '规则管理',
  '/settings': '系统配置',
}

function resolveTitle(pathname: string): string {
  const exact = PAGE_TITLES[pathname]
  if (exact) {
    return exact
  }
  if (pathname.startsWith('/directory/')) {
    return '群详情'
  }
  return '控制台'
}

function accountsTone(error: unknown): { tone: StatusTone; label: string } {
  if (error instanceof ApiError) {
    if (error.kind === 'unauthorized') {
      return { tone: 'error', label: '未授权' }
    }
    if (error.kind === 'not-found') {
      return { tone: 'unknown', label: '未启用' }
    }
    return { tone: 'error', label: '查询失败' }
  }
  return { tone: 'error', label: '查询失败' }
}

export function Topbar() {
  const queryClient = useQueryClient()
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const livez = useLivez()
  const {
    connected,
    dbConfigured,
    isLoading,
    isFetching,
    isError,
    error,
    accounts,
  } = useAccountContext()

  const title = resolveTitle(pathname)

  const processState: { tone: StatusTone; label: string } = livez.isSuccess
    ? { tone: 'ok', label: '进程在线' }
    : livez.isError
      ? { tone: 'error', label: '进程离线' }
      : { tone: 'unknown', label: '检测进程' }

  const accountState: { tone: StatusTone; label: string } = isError
    ? accountsTone(error)
    : isLoading
      ? { tone: 'unknown', label: '检测账号' }
      : connected
        ? { tone: 'ok', label: `${accounts.length} 个账号在线` }
        : { tone: 'warn', label: '账号未连接' }

  const databaseState: { tone: StatusTone; label: string } = dbConfigured
    ? { tone: 'ok', label: '数据库已配置' }
    : { tone: 'warn', label: '数据库未配置' }

  const isRefreshing = livez.isFetching || isFetching

  const refreshAll = () => {
    for (const queryKey of SYSTEM_QUERY_KEYS) {
      void queryClient.invalidateQueries({ queryKey })
    }
  }

  return (
    <header className="sticky top-0 z-30 border-b border-border bg-background">
      <div className="flex h-14 items-center gap-2 px-3 sm:px-5 lg:px-7">
        <MobileNav />

        <div className="hidden min-w-0 items-center gap-1.5 text-meta text-muted-foreground sm:flex">
          <span className="shrink-0">控制台</span>
          <ChevronRight className="size-3 shrink-0" aria-hidden />
          <span className="truncate font-medium text-foreground">{title}</span>
        </div>

        <div className="ml-auto flex items-center gap-1.5">
          <div className="hidden items-center gap-1.5 lg:flex" aria-label="全局运行状态">
            <StatusPill
              tone={processState.tone}
              label={processState.label}
              live
              description="/livez 探活：仅表示 Go 进程可达"
            />
            <StatusPill
              tone={accountState.tone}
              label={accountState.label}
              live
              description="来自 /api/v1/accounts 的连接快照"
            />
            <span className="hidden xl:inline-flex">
              <StatusPill
                tone={databaseState.tone}
                label={databaseState.label}
                live
                description="数据库未配置时管理路由不会注册"
              />
            </span>
          </div>

          <AccountSwitcher />

          <Button
            variant="ghost"
            size="icon"
            onClick={refreshAll}
            aria-label="刷新全部数据"
            title="刷新全部数据"
          >
            <RefreshCw className={cn('size-4', isRefreshing && 'animate-spin')} aria-hidden />
          </Button>
          <ThemeToggle />
        </div>
      </div>
    </header>
  )
}
