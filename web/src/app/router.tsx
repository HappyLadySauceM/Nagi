import type { QueryClient } from '@tanstack/react-query'
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  redirect,
  Link,
} from '@tanstack/react-router'
import { Compass } from 'lucide-react'
import { Button } from '../components/ui/button'
import { describeError } from '../components/feedback-state'
import { AccountsPage } from '../features/accounts/accounts-page'
import { DashboardPage } from '../features/dashboard/dashboard-page'
import { queryClient } from './query-client'
import { AppShell } from './layout/app-shell'

export interface RouterContext {
  queryClient: QueryClient
}

function NotFoundPage() {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-3 px-6 text-center">
      <span className="flex size-11 items-center justify-center rounded-full border border-border bg-card text-muted-foreground">
        <Compass className="size-5" aria-hidden />
      </span>
      <h1 className="text-xl font-semibold text-foreground">页面不存在</h1>
      <p className="max-w-sm text-xs leading-relaxed text-muted-foreground">
        该地址不匹配任何已实现的路由，不会触发该路由的数据请求。
      </p>
      <Button asChild variant="outline" size="sm" className="mt-1">
        <Link to="/dashboard">返回概览</Link>
      </Button>
    </div>
  )
}

function RootErrorComponent({ error, reset }: { error: unknown; reset: () => void }) {
  const presentation = describeError(error)
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-3 px-6 text-center">
      <h1 className="text-xl font-semibold text-foreground">{presentation.title}</h1>
      <p className="max-w-md text-xs leading-relaxed text-muted-foreground">
        {presentation.description}
      </p>
      <Button variant="outline" size="sm" onClick={reset} className="mt-1">
        重新加载
      </Button>
    </div>
  )
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: AppShell,
  notFoundComponent: NotFoundPage,
  errorComponent: RootErrorComponent,
})

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  component: DashboardPage,
})

const accountsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/accounts',
  component: AccountsPage,
})

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/dashboard' })
  },
})

const routeTree = rootRoute.addChildren([indexRoute, dashboardRoute, accountsRoute])

export const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
