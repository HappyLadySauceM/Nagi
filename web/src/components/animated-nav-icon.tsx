import { useEffect, useRef } from 'react'
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

export function AnimatedNavIcon({ icon: Icon, className }: { icon: LucideIcon; className?: string }) {
  const host = useRef<HTMLSpanElement>(null)
  const animation = useRef<{ startAnimation: () => void; stopAnimation: () => void }>(null)
  const reduced = useReducedMotion()
  const Animated = icons.get(Icon)
  useEffect(() => {
    const parent = host.current?.closest('button, a')
    if (!parent || reduced) return
    let sequence = 0
    const play = async () => {
      const current = ++sequence
      await animation.current?.stopAnimation()
      if (current === sequence) animation.current?.startAnimation()
    }
    const stop = () => { sequence++; animation.current?.stopAnimation() }
    for (const event of ['pointerenter', 'focus', 'click']) parent.addEventListener(event, play)
    for (const event of ['pointerleave', 'blur']) parent.addEventListener(event, stop)
    return () => {
      sequence++
      for (const event of ['pointerenter', 'focus', 'click']) parent.removeEventListener(event, play)
      for (const event of ['pointerleave', 'blur']) parent.removeEventListener(event, stop)
    }
  }, [reduced])
  return <span ref={host} className={`inline-flex size-4 shrink-0 items-center justify-center ${className ?? ''}`} aria-hidden>
    {Animated && !reduced ? <Animated ref={animation} size={16} /> : <Icon className="size-4" />}
  </span>
}
