import { useEffect } from 'react'

export function useEditGuard(dirty: boolean, busy: boolean) {
  useEffect(() => {
    const check = (event: Event) => {
      if (busy || (dirty && !window.confirm('放弃未保存的修改并切换页面或账号？'))) event.preventDefault()
    }
    window.addEventListener('nagi:account-changing', check)
    return () => window.removeEventListener('nagi:account-changing', check)
  }, [dirty, busy])
}
