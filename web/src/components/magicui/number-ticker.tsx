import { useEffect, useRef, useState } from 'react'
import { animate, useReducedMotion } from 'motion/react'
import { cn } from '../../lib/utils'

interface NumberTickerProps {
  value: number
  className?: string
  duration?: number
  prefix?: string
  suffix?: string
}

/**
 * Counts up to the target value on mount and on change. Adapted from Magic UI's
 * NumberTicker (MIT); reduced-motion users see the final value immediately.
 */
export function NumberTicker({
  value,
  className,
  duration = 0.6,
  prefix,
  suffix,
}: NumberTickerProps) {
  const reduceMotion = useReducedMotion()
  const [display, setDisplay] = useState(0)
  const previous = useRef(0)

  useEffect(() => {
    if (reduceMotion) {
      setDisplay(value)
      previous.current = value
      return
    }
    const controls = animate(previous.current, value, {
      duration,
      ease: 'easeOut',
      onUpdate: (latest) => setDisplay(latest),
    })
    previous.current = value
    return () => controls.stop()
  }, [value, duration, reduceMotion])

  return (
    <span className={cn('tabular-nums', className)}>
      {prefix}
      {Math.round(display).toLocaleString('zh-CN')}
      {suffix}
    </span>
  )
}
