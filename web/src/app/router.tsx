import type { QueryClient } from '@tanstack/react-query'
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  redirect,
} from '@tanstack/react-router'
import { DashboardPage } from '../features/dashboard/dashboard-page'
import {
  parseGroupDetailSearch,
  parsePageSearch,
} from '../lib/search-params'
import { AppShell } from './layout/app-shell'
import { NotFoundPage, RootErrorComponent } from './pages/route-fallbacks'
import { queryClient } from './query-client'

export interface RouterContext {
  queryClient: QueryClient
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
  component: lazyRouteComponent(
    () => import('../features/accounts/accounts-page'),
    'AccountsPage',
  ),
})

const contactsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/contacts',
  validateSearch: parsePageSearch,
  component: lazyRouteComponent(
    () => import('../features/directory/contacts-page'),
    'ContactsPage',
  ),
})

const groupsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/groups',
  validateSearch: parsePageSearch,
  component: lazyRouteComponent(
    () => import('../features/directory/groups-page'),
    'GroupsPage',
  ),
})

const groupDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/groups/$groupId',
  validateSearch: parseGroupDetailSearch,
  component: lazyRouteComponent(
    () => import('../features/directory/group-detail-page'),
    'GroupDetailPage',
  ),
})

const scopesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/scopes',
  component: lazyRouteComponent(() => import('../features/scopes/scopes-page'), 'ScopesPage'),
})

const scopeEditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/scopes/$type/$id',
  component: lazyRouteComponent(
    () => import('../features/scopes/scope-edit-page'),
    'ScopeEditPage',
  ),
})

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/dashboard' })
  },
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRoute,
  accountsRoute,
  contactsRoute,
  groupsRoute,
  groupDetailRoute,
  scopesRoute,
  scopeEditRoute,
])

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
