import { Bot, LayoutDashboard } from 'lucide-react'

/** Only routes that really exist are listed here; no placeholder entries. */
export const NAV_ITEMS = [
  {
    to: '/dashboard',
    label: '概览',
    hint: '连接状态与聚合指标',
    icon: LayoutDashboard,
  },
  {
    to: '/accounts',
    label: 'QQ 账号',
    hint: '账号、运行时与处理器',
    icon: Bot,
  },
] as const

export type NavItem = (typeof NAV_ITEMS)[number]
