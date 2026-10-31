import { useEffect, useRef, useState } from 'react'
import { Search, X } from 'lucide-react'
import { Input } from './ui/input'
import { cn } from '../lib/utils'

const DEBOUNCE_MS = 300

interface SearchFieldProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  label: string
  className?: string
  disabled?: boolean
}

/**
 * Debounced search input. The parent owns the committed value (usually the URL),
 * so an external reset also clears the draft.
 * 防抖搜索框：父级持有已提交值（通常来自 URL），外部重置时草稿同步清空。
 */
export function SearchField({
  value,
  onChange,
  placeholder = '搜索',
  label,
  className,
  disabled = false,
}: SearchFieldProps) {
  const [draft, setDraft] = useState(value)
  const committed = useRef(value)

  useEffect(() => {
    if (value !== committed.current) {
      committed.current = value
      setDraft(value)
    }
  }, [value])

  useEffect(() => {
    if (draft === committed.current) {
      return
    }
    const timer = window.setTimeout(() => {
      committed.current = draft
      onChange(draft)
    }, DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [draft, onChange])

  const commitNow = () => {
    if (draft !== committed.current) {
      committed.current = draft
      onChange(draft)
    }
  }

  return (
    <div className={cn('relative w-full sm:max-w-xs', className)}>
      <Search
        className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
        aria-hidden
      />
      <Input
        type="search"
        aria-label={label}
        placeholder={placeholder}
        value={draft}
        disabled={disabled}
        className="h-10 pl-8 pr-8 [&::-webkit-search-cancel-button]:hidden"
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault()
            commitNow()
          }
        }}
        onBlur={commitNow}
      />
      {draft ? (
        <button
          type="button"
          aria-label="清除搜索"
          className="absolute right-2 top-1/2 -translate-y-1/2 cursor-pointer rounded p-0.5 text-muted-foreground transition-colors hover:text-foreground"
          onClick={() => {
            setDraft('')
            committed.current = ''
            onChange('')
          }}
        >
          <X className="size-3.5" aria-hidden />
        </button>
      ) : null}
    </div>
  )
}
