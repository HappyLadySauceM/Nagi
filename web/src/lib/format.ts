const UNAVAILABLE = '不可获取'

/** Formats a count; undefined means the value could not be fetched. */
export function formatCount(value: number | undefined): string {
  if (value === undefined || Number.isNaN(value)) {
    return UNAVAILABLE
  }
  return value.toLocaleString('zh-CN')
}

export const UNAVAILABLE_TEXT = UNAVAILABLE

/** Formats an uptime in seconds into a compact Chinese duration. */
export function formatUptime(seconds: number | undefined): string {
  if (seconds === undefined || Number.isNaN(seconds)) {
    return UNAVAILABLE
  }
  const total = Math.max(0, Math.floor(seconds))
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)

  if (days > 0) {
    return `${days} 天 ${hours} 小时`
  }
  if (hours > 0) {
    return `${hours} 小时 ${minutes} 分`
  }
  if (minutes > 0) {
    return `${minutes} 分 ${total % 60} 秒`
  }
  return `${total} 秒`
}

/** Formats an RFC3339 timestamp as an absolute local date time. */
export function formatDateTime(value: string | undefined): string {
  if (!value) {
    return UNAVAILABLE
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return UNAVAILABLE
  }
  return date.toLocaleString('zh-CN', { hour12: false })
}

/** Formats a timestamp as a relative time such as “23 秒前”. */
export function formatRelativeTime(value: string | undefined): string {
  if (!value) {
    return UNAVAILABLE
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return UNAVAILABLE
  }
  const deltaSeconds = Math.round((Date.now() - date.getTime()) / 1000)
  if (deltaSeconds < 0) {
    return '刚刚'
  }
  if (deltaSeconds < 60) {
    return `${deltaSeconds} 秒前`
  }
  const minutes = Math.floor(deltaSeconds / 60)
  if (minutes < 60) {
    return `${minutes} 分前`
  }
  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    return `${hours} 小时前`
  }
  return `${Math.floor(hours / 24)} 天前`
}

/** Formats the last-updated timestamp of a query. */
export function formatUpdatedAt(timestamp: number | undefined): string {
  if (!timestamp) {
    return '尚未更新'
  }
  return new Date(timestamp).toLocaleTimeString('zh-CN', { hour12: false })
}
