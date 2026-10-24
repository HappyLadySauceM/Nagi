import {
  Bot,
  BrainCircuit,
  LayoutDashboard,
  ShieldCheck,
  Users,
  UsersRound,
} from 'lucide-react'

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
  {
    to: '/contacts',
    label: '联系人',
    hint: '好友目录与策略入口',
    icon: Users,
  },
  {
    to: '/groups',
    label: '群聊',
    hint: '群列表、成员与策略入口',
    icon: UsersRound,
  },
  {
    to: '/scopes',
    label: '作用域策略',
    hint: '已保存的授权配置',
    icon: ShieldCheck,
  },
  {
    to: '/memories',
    label: '长期记忆',
    hint: '候选、审批与可见性',
    icon: BrainCircuit,
  },
] as const

export type NavItem = (typeof NAV_ITEMS)[number]
