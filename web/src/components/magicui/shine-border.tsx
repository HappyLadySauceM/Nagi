import type { CSSProperties } from 'react'
import { cn } from '../../lib/utils'

interface ShineBorderProps {
  color?: string
  duration?: number
  width?: number
  className?: string
}

/**
 * Animated gradient border, adapted from Magic UI's ShineBorder (MIT). Used on a
 * single card; the animation is disabled by `prefers-reduced-motion` in CSS.
 */
export function ShineBorder({
  color = 'hsl(var(--primary))',
  duration = 14,
  width = 1,
  className,
}: ShineBorderProps) {
  const style = {
    '--shine-color': color,
    '--shine-duration': `${duration}s`,
    '--shine-width': `${width}px`,
  } as CSSProperties

  return <span aria-hidden className={cn('shine-border', className)} style={style} />
}
