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
  DEFAULT_DIRECTORY_SEARCH,
  DEFAULT_PAGE_LIMIT,
  parseDirectorySearch,
  parseGroupDetailSearch,
  parseMemorySearch,
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

const decisionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/decisions',
  component: lazyRouteComponent(
    () => import('../features/intent/intent-page'),
    'IntentPage',
  ),
})

const rulesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/rules',
  component: lazyRouteComponent(
    () => import('../features/intent/rules-page'),
    'RulesPage',
  ),
})

const directoryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/directory',
  validateSearch: parseDirectorySearch,
  component: lazyRouteComponent(
    () => import('../features/directory/directory-page'),
    'DirectoryPage',
  ),
})

const groupDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/directory/$groupId',
  validateSearch: parseGroupDetailSearch,
  component: lazyRouteComponent(
    () => import('../features/directory/group-detail-page'),
    'GroupDetailPage',
  ),
})

// Legacy paths stay reachable so bookmarks and shared links keep working.
// 旧路径保留重定向，书签与分享链接不会失效。
const contactsRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/contacts',
  beforeLoad: () => {
    throw redirect({
      to: '/directory',
      search: { ...DEFAULT_DIRECTORY_SEARCH, tab: 'friends' },
    })
  },
})

const groupsRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/groups',
  beforeLoad: () => {
    throw redirect({
      to: '/directory',
      search: { ...DEFAULT_DIRECTORY_SEARCH, tab: 'groups' },
    })
  },
})

const legacyGroupDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/groups/$groupId',
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: '/directory/$groupId',
      params: { groupId: params.groupId },
      search: {
        search: (search as { search?: string }).search ?? '',
        page: (search as { page?: number }).page ?? 1,
        limit: (search as { limit?: number }).limit ?? DEFAULT_PAGE_LIMIT,
        name: (search as { name?: string }).name ?? '',
      },
    })
  },
})

// Policy management lives on the contacts page now; the legacy paths redirect
// so bookmarks and shared links keep working.
// 策略配置已移入联系人页；旧路径保留重定向，书签与分享链接不会失效。
const scopesRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/scopes',
  beforeLoad: () => {
    throw redirect({
      to: '/directory',
      search: { ...DEFAULT_DIRECTORY_SEARCH, tab: 'groups' },
    })
  },
})

const scopeEditRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/scopes/$type/$id',
  beforeLoad: () => {
    throw redirect({
      to: '/directory',
      search: { ...DEFAULT_DIRECTORY_SEARCH, tab: 'groups' },
    })
  },
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: lazyRouteComponent(
    () => import('../features/settings/settings-page'),
    'SettingsPage',
  ),
})

const memoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/memories',
  validateSearch: parseMemorySearch,
  component: lazyRouteComponent(
    () => import('../features/memories/memories-page'),
    'MemoriesPage',
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
  decisionsRoute,
  rulesRoute,
  directoryRoute,
  groupDetailRoute,
  contactsRedirectRoute,
  groupsRedirectRoute,
  legacyGroupDetailRoute,
  scopesRedirectRoute,
  scopeEditRedirectRoute,
  memoriesRoute,
  settingsRoute,
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
