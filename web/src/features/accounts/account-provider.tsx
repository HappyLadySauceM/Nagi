import { useCallback, useMemo, useState, type ReactNode } from 'react'
import type { AccountSummary } from '../../lib/api/contracts'
import { useAccounts } from '../system/queries'
import { AccountContext, type AccountContextValue } from './account-context'

const STORAGE_KEY = 'nagi.selected-account'

function readStoredBotId(): number | undefined {
  if (typeof window === 'undefined') {
    return undefined
  }
  const raw = window.localStorage.getItem(STORAGE_KEY)
  if (!raw) {
    return undefined
  }
  const parsed = Number(raw)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

/**
 * Picks the account to show: the stored one when it still exists, otherwise the
 * first identified account, preferring a connected one. Never guesses an id for
 * an account that has not reported its identity yet.
 * 选择展示账号：已保存的优先；否则取第一个已识别账号，并优先已连接者。
 * 对尚未上报身份的账号不做任何猜测。
 */
function resolveSelection(accounts: AccountSummary[], stored?: number): number | undefined {
  if (stored !== undefined && accounts.some((account) => account.botId === stored)) {
    return stored
  }
  const identified = accounts.filter(
    (account): account is AccountSummary & { botId: number } => account.botId !== undefined,
  )
  const connected = identified.find((account) => account.connected)
  if (connected) {
    return connected.botId
  }
  return identified[0]?.botId
}

export function AccountProvider({ children }: { children: ReactNode }) {
  const accountsQuery = useAccounts()
  const [storedBotId, setStoredBotId] = useState<number | undefined>(readStoredBotId)

  const accounts = useMemo(() => accountsQuery.data?.accounts ?? [], [accountsQuery.data])
  const { data, isLoading, isFetching, isError, error, refetch } = accountsQuery

  const selectedBotId = useMemo(
    () => resolveSelection(accounts, storedBotId),
    [accounts, storedBotId],
  )

  const selectAccount = useCallback((botId?: number) => {
    if (!window.dispatchEvent(new Event('nagi:account-changing', { cancelable: true }))) return
    setStoredBotId(botId)
    if (typeof window === 'undefined') {
      return
    }
    if (botId === undefined) {
      window.localStorage.removeItem(STORAGE_KEY)
      return
    }
    window.localStorage.setItem(STORAGE_KEY, String(botId))
  }, [])

  const selectedAccount = useMemo(
    () => accounts.find((account) => account.botId === selectedBotId),
    [accounts, selectedBotId],
  )

  const value = useMemo<AccountContextValue>(
    () => ({
      accounts,
      selectedBotId,
      selectedAccount,
      selectAccount,
      dbConfigured: data?.dbConfigured ?? false,
      connected: data?.connected ?? false,
      isLoading,
      isFetching,
      isError,
      error,
      refetch: () => void refetch(),
    }),
    // Depend on concrete fields: the query result object itself is a new
    // reference on every render, which would defeat this memo entirely.
    // 依赖具体字段：query 结果对象每次渲染都是新引用，会让 memo 完全失效。
    [accounts, selectedBotId, selectedAccount, selectAccount, data, isLoading, isFetching, isError, error, refetch],
  )

  return <AccountContext.Provider value={value}>{children}</AccountContext.Provider>
}
