import { useEffect, useRef } from 'react'
import { cn } from '../lib/utils'
import { useReducedMotion } from 'motion/react'
import { Archive, Bot, BrainCircuit, Database, LayoutDashboard, MessageCircle, ScrollText, Send, Settings2, ShieldCheck, Users, Wrench, type LucideIcon } from 'lucide-react'
import { UsersIcon } from './animated-icons/users'
import { BrainIcon } from './animated-icons/brain'
import { BotIcon } from './animated-icons/bot'
import { SettingsIcon } from './animated-icons/settings'
import { MessageCircleIcon } from './animated-icons/message-circle'
import { ShieldCheckIcon } from './animated-icons/shield-check'
import { SendIcon } from './animated-icons/send'
import { DatabaseBackupIcon } from './animated-icons/database-backup'
import { WrenchIcon } from './animated-icons/wrench'
import { ArchiveIcon } from './animated-icons/archive'
import { CircleGaugeIcon } from './animated-icons/circle-gauge'
import { FileTextIcon } from './animated-icons/file-text'

const icons = new Map<LucideIcon, typeof UsersIcon>([
  [Users, UsersIcon], [BrainCircuit, BrainIcon], [Bot, BotIcon], [Settings2, SettingsIcon],
  [MessageCircle, MessageCircleIcon], [ShieldCheck, ShieldCheckIcon], [Send, SendIcon],
  [Database, DatabaseBackupIcon], [Wrench, WrenchIcon], [Archive, ArchiveIcon],
  [LayoutDashboard, CircleGaugeIcon], [ScrollText, FileTextIcon],
])

export function AnimatedNavIcon({ icon: Icon, className, active = false }: { icon: LucideIcon; className?: string; active?: boolean }) {
  const host = useRef<HTMLSpanElement>(null)
  const animation = useRef<{ startAnimation: () => void; stopAnimation: () => void }>(null)
  const reduced = useReducedMotion()
  const Animated = icons.get(Icon)
  useEffect(() => {
    const parent = host.current?.closest('button, a')
    if (!parent || reduced) return
    let sequence = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const stop = () => {
      sequence++
      clearTimeout(timer)
      void animation.current?.stopAnimation()
    }
    const play = async () => {
      if (document.hidden) return
      clearTimeout(timer)
      const current = ++sequence
      await animation.current?.stopAnimation()
      if (current !== sequence) return
      await animation.current?.startAnimation()
      if (active && current === sequence) timer = setTimeout(() => { void play() }, 600)
    }
    const interact = () => { if (!active) void play() }
    const leave = () => { if (!active) stop() }
    const visibility = () => { stop(); if (active && !document.hidden) void play() }
    for (const event of ['pointerenter', 'focus', 'click']) parent.addEventListener(event, interact)
    for (const event of ['pointerleave', 'blur']) parent.addEventListener(event, leave)
    document.addEventListener('visibilitychange', visibility)
    if (active) void play()
    return () => {
      stop()
      for (const event of ['pointerenter', 'focus', 'click']) parent.removeEventListener(event, interact)
      for (const event of ['pointerleave', 'blur']) parent.removeEventListener(event, leave)
      document.removeEventListener('visibilitychange', visibility)
    }
  }, [reduced, active, Animated])
  const color = !active ? 'text-muted-foreground' : Icon === BrainCircuit || Icon === ScrollText
    ? 'text-violet-600 dark:text-violet-400' : Icon === LayoutDashboard || Icon === Settings2
      ? 'text-blue-600 dark:text-blue-400' : 'text-cyan-600 dark:text-cyan-400'
  return <span ref={host} className={cn('inline-flex size-5 shrink-0 items-center justify-center', className, color)} aria-hidden>
    {Animated && !reduced ? <Animated ref={animation} size={20} /> : <Icon className="size-5" />}
  </span>
}
