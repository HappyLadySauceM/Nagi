import {
  Bot,
  BrainCircuit,
  LayoutDashboard,
  ScrollText,
  SlidersHorizontal,
  Users,
  type LucideIcon,
} from 'lucide-react'

export type NavPath = '/dashboard' | '/directory' | '/decisions' | '/rules' | '/accounts' | '/memories'

export interface NavItem {
  to: NavPath
  label: string
  hint: string
  icon: LucideIcon
}

export interface NavGroup {
  label: string
  items: NavItem[]
}

/**
 * Grouped navigation: run the bot, shape its knowledge, then configure it.
 * 分组导航：先看运行状态，再管知识与配置。
 */
export const NAV_GROUPS: NavGroup[] = [
  {
    label: '工作台',
    items: [
      { to: '/dashboard', label: '概览', hint: '账号状态与运行摘要', icon: LayoutDashboard },
      { to: '/directory', label: '联系人', hint: '好友与群聊统一入口', icon: Users },
    ],
  },
  {
    label: '智能',
    items: [
      { to: '/memories', label: '知识与记忆', hint: '候选、审批与可见性', icon: BrainCircuit },
      { to: '/decisions', label: '决策记录', hint: '为什么回复或沉默', icon: ScrollText },
      { to: '/rules', label: '意图规则', hint: '版本化规则与模拟', icon: SlidersHorizontal },
    ],
  },
  {
    label: '配置',
    items: [
      { to: '/accounts', label: '账号连接', hint: '端点、身份与运行时', icon: Bot },
    ],
  },
]
