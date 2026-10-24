import { Children, type ReactNode } from 'react'
import { motion, useReducedMotion } from 'motion/react'

interface AnimatedGroupProps {
  children: ReactNode
  className?: string
  childClassName?: string
  delay?: number
}

/**
 * Staggers children into view, following Motion Primitives' AnimatedGroup (MIT).
 * The stagger is skipped entirely for reduced-motion users.
 */
export function AnimatedGroup({
  children,
  className,
  childClassName,
  delay = 0,
}: AnimatedGroupProps) {
  const reduceMotion = useReducedMotion()

  if (reduceMotion) {
    return <div className={className}>{children}</div>
  }

  return (
    <motion.div
      className={className}
      initial="hidden"
      animate="visible"
      variants={{
        hidden: {},
        visible: { transition: { staggerChildren: 0.04, delayChildren: delay } },
      }}
    >
      {Children.map(children, (child) => (
        <motion.div
          className={childClassName}
          variants={{
            hidden: { opacity: 0, y: 8 },
            visible: {
              opacity: 1,
              y: 0,
              transition: { duration: 0.18, ease: 'easeOut' },
            },
          }}
        >
          {child}
        </motion.div>
      ))}
    </motion.div>
  )
}
