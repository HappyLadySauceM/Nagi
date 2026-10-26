import { useEffect, useState } from 'react'
import { Outlet, useRouterState } from '@tanstack/react-router'
import { motion, useReducedMotion } from 'motion/react'
import { cn } from '../../lib/utils'
import { Sidebar } from './sidebar'
import { Topbar } from './topbar'

const SIDEBAR_STORAGE_KEY = 'nagi-sidebar-collapsed'

export function AppShell() {
  const [collapsed, setCollapsed] = useState(
    () => window.localStorage.getItem(SIDEBAR_STORAGE_KEY) === 'true',
  )
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const reduceMotion = useReducedMotion()

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_STORAGE_KEY, String(collapsed))
  }, [collapsed])

  return (
    <div className="min-h-screen">
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed((value) => !value)} />

      <div
        className={cn(
          'flex min-h-screen flex-col transition-[padding-left] duration-200 ease-quiet',
          collapsed ? 'lg:pl-[72px]' : 'lg:pl-[248px]',
        )}
      >
        <Topbar />

        <main className="flex-1 px-4 pb-16 pt-6 sm:px-6 lg:px-8">
          <div className="mx-auto w-full max-w-[1680px]">
            {reduceMotion ? (
              <Outlet />
            ) : (
              <motion.div
                key={pathname}
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.18, ease: 'easeOut' }}
              >
                <Outlet />
              </motion.div>
            )}
          </div>
        </main>
      </div>
    </div>
  )
}
