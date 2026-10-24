import { useQueryClient } from '@tanstack/react-query'
import { useRouterState } from '@tanstack/react-router'
import { ChevronRight, RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { StatusPill, type StatusTone } from '../../components/status-indicator'
import { ThemeToggle } from '../../components/theme-toggle'
import { ApiError } from '../../lib/api/client'
import { cn } from '../../lib/utils'
import { SYSTEM_QUERY_KEYS, useAccounts, useLivez } from '../../features/system/queries'
import { MobileNav } from './mobile-nav'

const PAGE_TITLES: Record<string, string> = {
  '/dashboard': '概览',
  '/accounts': 'QQ 账号',
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
  const accounts = useAccounts()

  const title = PAGE_TITLES[pathname] ?? '控制台'

  const processState: { tone: StatusTone; label: string } = livez.isSuccess
    ? { tone: 'ok', label: '进程在线' }
    : livez.isError
      ? { tone: 'error', label: '进程离线' }
      : { tone: 'unknown', label: '检测进程' }

  const accountState = accounts.data
    ? accounts.data.connected
      ? { tone: 'ok' as StatusTone, label: 'OneBot 已连接' }
      : { tone: 'warn' as StatusTone, label: 'OneBot 未连接' }
    : accounts.isError
      ? accountsTone(accounts.error)
      : { tone: 'unknown' as StatusTone, label: '检测 OneBot' }

  const databaseState: { tone: StatusTone; label: string } = accounts.data
    ? accounts.data.dbConfigured
      ? { tone: 'ok', label: '数据库已配置' }
      : { tone: 'warn', label: '数据库未配置' }
    : accounts.isError
      ? accountsTone(accounts.error)
      : { tone: 'unknown', label: '检测数据库' }

  // Narrow screens show one summarised pill instead of three, still with text.
  const summaryState: { tone: StatusTone; label: string } = livez.isError
    ? { tone: 'error', label: '服务离线' }
    : accounts.isError
      ? accountsTone(accounts.error)
      : !accounts.data
        ? { tone: 'unknown', label: '状态检测中' }
        : !accounts.data.connected
          ? { tone: 'warn', label: 'OneBot 未连接' }
          : !accounts.data.dbConfigured
            ? { tone: 'warn', label: '数据库未配置' }
            : { tone: 'ok', label: '运行正常' }

  const isRefreshing = livez.isFetching || accounts.isFetching

  const refreshAll = () => {
    for (const queryKey of SYSTEM_QUERY_KEYS) {
      void queryClient.invalidateQueries({ queryKey })
    }
  }

  return (
    <header className="sticky top-0 z-30 border-b border-border/80 bg-background/85 backdrop-blur-md">
      <div className="flex h-[60px] items-center gap-2 px-3 sm:px-5 lg:px-7">
        <MobileNav />

        <div className="hidden min-w-0 items-center gap-1.5 text-xs text-muted-foreground sm:flex">
          <span className="shrink-0">控制台</span>
          <ChevronRight className="size-3 shrink-0" aria-hidden />
          <span className="truncate font-medium text-foreground">{title}</span>
        </div>

        <div className="ml-auto flex items-center gap-1.5">
          <span className="sm:hidden">
            <StatusPill tone={summaryState.tone} label={summaryState.label} live />
          </span>
          <div className="hidden items-center gap-1.5 sm:flex" aria-label="全局运行状态">
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
