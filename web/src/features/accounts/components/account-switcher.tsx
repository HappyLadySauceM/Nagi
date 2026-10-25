import { Link } from '@tanstack/react-router'
import { ChevronDown, Settings2 } from 'lucide-react'
import { Avatar } from '../../../components/avatar'
import { Button } from '../../../components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheck,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '../../../components/ui/dropdown-menu'
import type { AccountSummary } from '../../../lib/api/contracts'
import { cn } from '../../../lib/utils'
import { accountLabel, useAccountContext } from '../account-context'

function accountTone(account: AccountSummary): { dot: string; label: string } {
  if (account.rejected) {
    return { dot: 'bg-destructive', label: '已拒绝' }
  }
  if (account.connected) {
    return { dot: 'bg-success', label: '在线' }
  }
  return { dot: 'bg-warning', label: '离线' }
}

/**
 * Global account switcher. Every account-scoped query keys on the selected bot
 * id, so switching here swaps the whole console instead of mixing two accounts.
 * 全局账号切换器。所有账号级查询都以选中的 bot id 为键，切换后整个控制台随账号切换。
 */
export function AccountSwitcher() {
  const { accounts, selectedAccount, selectedBotId, selectAccount, isLoading } = useAccountContext()

  if (isLoading && accounts.length === 0) {
    return <span className="h-9 w-40 animate-pulse rounded-lg bg-secondary/60" aria-hidden />
  }

  if (accounts.length === 0) {
    return (
      <Button asChild variant="outline" size="sm">
        <Link to="/accounts">
          <Settings2 aria-hidden />
          配置账号
        </Link>
      </Button>
    )
  }

  const tone = selectedAccount ? accountTone(selectedAccount) : undefined

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-9 gap-2 pl-1.5 pr-2.5"
          aria-label="切换 QQ 账号"
        >
          <Avatar
            id={selectedAccount?.botId}
            name={accountLabel(selectedAccount)}
            size="sm"
            className="size-6 rounded-md"
          />
          <span className="hidden min-w-0 flex-col items-start leading-tight sm:flex">
            <span className="max-w-[120px] truncate text-meta font-medium">
              {accountLabel(selectedAccount)}
            </span>
            <span className="font-mono text-caption text-muted-foreground tabular-nums">
              {selectedBotId ?? '待识别'}
            </span>
          </span>
          {tone ? <span className={cn('size-1.5 rounded-full', tone.dot)} aria-hidden /> : null}
          <ChevronDown className="size-3.5 text-muted-foreground" aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="w-64" align="end">
        <DropdownMenuLabel>切换账号</DropdownMenuLabel>
        {accounts.map((account) => {
          const itemTone = accountTone(account)
          const active = account.botId !== undefined && account.botId === selectedBotId
          return (
            <DropdownMenuItem
              key={account.endpoint + String(account.botId ?? '')}
              disabled={account.botId === undefined}
              onSelect={() => {
                if (account.botId !== undefined) {
                  selectAccount(account.botId)
                }
              }}
              className="gap-2 py-2"
            >
              <Avatar
                id={account.botId}
                name={accountLabel(account)}
                size="sm"
                className="size-7 rounded-md"
              />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-body text-foreground">
                  {accountLabel(account)}
                </span>
                <span className="flex items-center gap-1.5 text-caption text-muted-foreground">
                  <span className={cn('size-1.5 rounded-full', itemTone.dot)} aria-hidden />
                  {account.botId ?? '等待登录信息'} · {itemTone.label}
                </span>
              </span>
              <DropdownMenuCheck active={active} />
            </DropdownMenuItem>
          )
        })}
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/accounts" className="gap-2 py-2 text-meta text-muted-foreground">
            <Settings2 className="size-3.5" aria-hidden />
            管理账号连接
          </Link>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
