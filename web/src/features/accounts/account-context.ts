import { createContext, useContext } from 'react'
import type { AccountSummary } from '../../lib/api/contracts'

export interface AccountContextValue {
  accounts: AccountSummary[]
  /** Undefined until an account reports its QQ number. */
  selectedBotId?: number
  selectedAccount?: AccountSummary
  selectAccount: (botId?: number) => void
  dbConfigured: boolean
  /** True when at least one endpoint is connected. */
  connected: boolean
  isLoading: boolean
  isFetching: boolean
  isError: boolean
  error: unknown
  refetch: () => void
}

export const AccountContext = createContext<AccountContextValue | null>(null)

export function useAccountContext(): AccountContextValue {
  const context = useContext(AccountContext)
  if (!context) {
    throw new Error('useAccountContext 必须在 AccountProvider 内使用')
  }
  return context
}

/** Display name of one account; falls back to the QQ number or endpoint. */
export function accountLabel(account?: AccountSummary): string {
  if (!account) {
    return '未选择账号'
  }
  if (account.nickname) {
    return account.nickname
  }
  if (account.botId !== undefined) {
    return String(account.botId)
  }
  return account.endpoint || '未识别账号'
}
