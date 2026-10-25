import { useEffect, useState } from 'react'
import { UsersRound } from 'lucide-react'
import { cn } from '../lib/utils'

type AvatarSize = 'sm' | 'md' | 'lg'

const SIZE_CLASS: Record<AvatarSize, string> = {
  sm: 'size-8 rounded-lg text-xs',
  md: 'size-10 rounded-xl text-sm',
  lg: 'size-14 rounded-2xl text-lg',
}

interface AvatarProps {
  /** QQ number or group number; without it the fallback glyph is shown. */
  id?: number
  kind?: 'user' | 'group'
  name?: string
  size?: AvatarSize
  className?: string
}

function qqImageUrl(id: number, kind: 'user' | 'group'): string {
  return kind === 'group'
    ? `https://p.qlogo.cn/gh/${id}/${id}/100`
    : `https://q1.qlogo.cn/g?b=qq&nk=${id}&s=100`
}

/**
 * QQ avatar with a deterministic fallback. Remote images can fail (offline,
 * privacy settings), so the fallback is rendered instead of a broken icon.
 * QQ 头像带确定性兜底：离线或隐私设置导致加载失败时显示首字/图标，而不是破图。
 */
export function Avatar({ id, kind = 'user', name, size = 'md', className }: AvatarProps) {
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    setFailed(false)
  }, [id, kind])

  const label = (name || '').trim()
  const initial = label ? label.slice(0, 1) : ''

  return (
    <span
      className={cn(
        'relative flex shrink-0 items-center justify-center overflow-hidden bg-gradient-to-br from-secondary to-secondary/60 font-medium text-secondary-foreground ring-1 ring-border/70',
        SIZE_CLASS[size],
        className,
      )}
      aria-hidden
    >
      {id !== undefined && !failed ? (
        <img
          src={qqImageUrl(id, kind)}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          className="size-full object-cover"
          onError={() => setFailed(true)}
        />
      ) : kind === 'group' ? (
        <UsersRound className="size-1/2" />
      ) : initial ? (
        <span>{initial}</span>
      ) : (
        <span className="size-1/3 rounded-full bg-muted-foreground/40" />
      )}
    </span>
  )
}
