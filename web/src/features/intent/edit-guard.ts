import { useEffect } from 'react'
import { useRouter } from '@tanstack/react-router'
import { confirmAction } from '../../components/confirm-action'

export async function allowWorkspaceChange() {
  const confirmations: Promise<boolean>[] = []
  const allowed = window.dispatchEvent(
    new CustomEvent('nagi:account-changing', {
      cancelable: true,
      detail: { confirmations },
    }),
  )
  return allowed && (await Promise.all(confirmations)).every(Boolean)
}

export function useEditGuard(dirty: boolean, busy: boolean) {
  const router = useRouter({ warn: false })
  useEffect(() => {
    if (!router || (!dirty && !busy)) return
    return router.history.block({
      enableBeforeUnload: false,
      blockerFn: async () => busy || !(await confirmAction('放弃未保存的修改并切换页面？')),
    })
  }, [router, dirty, busy])
  useEffect(() => {
    const check = (event: Event) => {
      if (busy) {
        event.preventDefault()
        return
      }
      if (!dirty) return
      const detail = (
        event as CustomEvent<{ confirmations: Promise<boolean>[] }>
      ).detail
      if (detail && Array.isArray(detail.confirmations)) {
        detail.confirmations.push(
          confirmAction('放弃未保存的修改并切换页面或账号？'),
        )
      } else event.preventDefault()
    }
    window.addEventListener('nagi:account-changing', check)
    return () => window.removeEventListener('nagi:account-changing', check)
  }, [dirty, busy])
}
