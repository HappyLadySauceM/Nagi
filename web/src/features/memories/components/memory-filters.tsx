import { Button } from '../../../components/ui/button'
import { Select } from '../../../components/ui/select'
import { SearchField } from '../../../components/search-field'
import {
  MEMORY_STATUS_LABELS,
  MEMORY_VISIBILITY_LABELS,
  type MemoryStatus,
  type MemoryVisibility,
} from '../../../lib/api/contracts'
import type { MemorySearch } from '../../../lib/search-params'

const STATUS_OPTIONS: MemoryStatus[] = ['candidate', 'active', 'superseded', 'deleted']
const VISIBILITY_OPTIONS: MemoryVisibility[] = [
  'bot_shared',
  'group',
  'private_user',
  'conversation',
]

interface MemoryFiltersProps {
  value: MemorySearch
  onChange: (patch: Partial<MemorySearch>) => void
  disabled?: boolean
}

export function MemoryFilters({ value, onChange, disabled = false }: MemoryFiltersProps) {
  const hasFilters =
    value.search !== '' ||
    value.ownerKey !== '' ||
    value.status !== '' ||
    value.visibility !== ''

  return (
    <div className="flex flex-col gap-2 border-b border-border/60 px-5 py-4 lg:flex-row lg:items-center lg:justify-between">
      <div className="flex w-full flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
        <SearchField
          label="搜索记忆内容"
          placeholder="按内容关键词搜索"
          value={value.search}
          onChange={(search) => onChange({ search })}
          disabled={disabled}
        />
        <SearchField
          label="按归属精确筛选"
          placeholder="归属（群号或 QQ）"
          value={value.ownerKey}
          onChange={(ownerKey) => onChange({ ownerKey })}
          disabled={disabled}
          className="sm:max-w-[180px]"
        />
        <Select
          aria-label="按状态筛选"
          containerClassName="w-full sm:w-[136px]"
          value={value.status}
          disabled={disabled}
          onChange={(event) => onChange({ status: event.target.value as MemoryStatus | '' })}
        >
          <option value="">全部状态</option>
          {STATUS_OPTIONS.map((status) => (
            <option key={status} value={status}>
              {MEMORY_STATUS_LABELS[status]}
            </option>
          ))}
        </Select>
        <Select
          aria-label="按可见性筛选"
          containerClassName="w-full sm:w-[136px]"
          value={value.visibility}
          disabled={disabled}
          onChange={(event) =>
            onChange({ visibility: event.target.value as MemoryVisibility | '' })
          }
        >
          <option value="">全部可见性</option>
          {VISIBILITY_OPTIONS.map((visibility) => (
            <option key={visibility} value={visibility}>
              {MEMORY_VISIBILITY_LABELS[visibility]}
            </option>
          ))}
        </Select>
      </div>
      {hasFilters ? (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onChange({ search: '', ownerKey: '', status: '', visibility: '' })}
          disabled={disabled}
        >
          清除筛选
        </Button>
      ) : null}
    </div>
  )
}
 