import type { ReactNode } from 'react'
import { Label } from './ui/label'
import { cn } from '../lib/utils'

interface FormFieldProps {
  id: string
  label: string
  hint?: string
  error?: string
  required?: boolean
  children: ReactNode
  className?: string
}

/**
 * Label + control + hint/error. Controls should point at `${id}-hint` and
 * `${id}-error` through `aria-describedby`.
 * 标签 + 控件 + 提示/错误；控件需通过 aria-describedby 关联 `${id}-hint` 与 `${id}-error`。
 */
export function FormField({
  id,
  label,
  hint,
  error,
  required = false,
  children,
  className,
}: FormFieldProps) {
  return (
    <div className={cn('space-y-1.5', className)}>
      <div className="flex items-baseline gap-1">
        <Label htmlFor={id}>{label}</Label>
        {required ? (
          <span className="text-[10px] text-destructive" aria-hidden>
            *
          </span>
        ) : null}
      </div>
      {children}
      {error ? (
        <p id={`${id}-error`} className="text-[11px] text-destructive" role="alert">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-[11px] leading-relaxed text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  )
}
