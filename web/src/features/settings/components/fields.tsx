import type { ReactNode } from 'react'
import { Input } from '../../../components/ui/input'
import { Label } from '../../../components/ui/label'
import { Select } from '../../../components/ui/select'
import { Switch } from '../../../components/ui/switch'
import { cn } from '../../../lib/utils'

interface FieldShellProps {
  id: string
  label: string
  hint?: string
  error?: string
  children: ReactNode
  disabled?: boolean
}

/** Label + hint + inline error around one control. */
function FieldShell({ id, label, hint, error, children, disabled }: FieldShellProps) {
  return (
    <div className={cn('py-2.5', disabled ? 'opacity-55' : undefined)}>
      <Label htmlFor={id} className="text-sm">
        {label}
      </Label>
      <div className="mt-1.5">{children}</div>
      {error ? (
        <p id={`${id}-error`} role="alert" className="mt-1 text-meta text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="mt-1 text-meta leading-relaxed text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  )
}

interface SwitchFieldProps {
  id: string
  label: string
  description: string
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
  error?: string
  badge?: string
}

/** Left-aligned switch row; the description explains the concrete effect. */
export function SwitchField({
  id,
  label,
  description,
  checked,
  onChange,
  disabled = false,
  error,
  badge,
}: SwitchFieldProps) {
  return (
    <div className="flex items-start justify-between gap-6 border-b border-border/60 py-3 last:border-b-0">
      <div className="min-w-0">
        <span className="flex items-center gap-2">
          <Label htmlFor={id} className="text-sm">
            {label}
          </Label>
          {badge ? (
            <span className="rounded-full border border-border bg-secondary px-2 py-0.5 text-caption text-muted-foreground">
              {badge}
            </span>
          ) : null}
        </span>
        <p className="mt-0.5 text-meta leading-relaxed text-muted-foreground">{description}</p>
        {error ? (
          <p role="alert" className="mt-1 text-meta text-destructive">
            {error}
          </p>
        ) : null}
      </div>
      <Switch
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onChange}
        aria-describedby={`${id}-hint`}
      />
    </div>
  )
}

interface TextFieldProps {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  hint?: string
  placeholder?: string
  error?: string
  disabled?: boolean
  mono?: boolean
}

/** Single-line text input with the shared label/error shell. */
export function TextField({
  id,
  label,
  value,
  onChange,
  hint,
  placeholder,
  error,
  disabled = false,
  mono = false,
}: TextFieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error} disabled={disabled}>
      <Input
        id={id}
        value={value}
        autoComplete="off"
        spellCheck={false}
        placeholder={placeholder}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : hint ? `${id}-hint` : undefined}
        className={mono ? 'font-mono text-meta' : undefined}
        onChange={(event) => onChange(event.target.value)}
      />
    </FieldShell>
  )
}

interface NumberFieldProps {
  id: string
  label: string
  value: number
  onChange: (value: number) => void
  unit?: string
  hint?: string
  error?: string
  disabled?: boolean
  step?: number
}

/** Numeric input; an empty value becomes NaN so validation reports it. */
export function NumberField({
  id,
  label,
  value,
  onChange,
  unit,
  hint,
  error,
  disabled = false,
  step = 1,
}: NumberFieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error} disabled={disabled}>
      <div className="relative">
        <Input
          id={id}
          type="number"
          inputMode="decimal"
          step={step}
          value={Number.isFinite(value) ? value : ''}
          disabled={disabled}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${id}-error` : hint ? `${id}-hint` : undefined}
          className={cn('font-mono tabular-nums', unit ? 'pr-12' : undefined)}
          onChange={(event) => onChange(event.target.valueAsNumber)}
        />
        {unit ? (
          <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-meta text-muted-foreground">
            {unit}
          </span>
        ) : null}
      </div>
    </FieldShell>
  )
}

interface SelectFieldProps {
  id: string
  label: string
  value: string
  options: Array<{ value: string; label: string }>
  onChange: (value: string) => void
  hint?: string
  error?: string
  disabled?: boolean
}

/** Select with the same shell as the other fields. */
export function SelectField({
  id,
  label,
  value,
  options,
  onChange,
  hint,
  error,
  disabled = false,
}: SelectFieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error} disabled={disabled}>
      <Select
        id={id}
        value={value}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </Select>
    </FieldShell>
  )
}
