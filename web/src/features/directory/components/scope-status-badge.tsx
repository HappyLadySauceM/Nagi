import { Badge } from '../../../components/ui/badge'
import type { ScopePolicy } from '../../../lib/api/contracts'

interface ScopeStatusBadgeProps {
  policy?: ScopePolicy
}

/**
 * Unknown scopes are not "off by accident": the backend resolves them to the
 * default-deny policy, so the badge says so explicitly.
 * 未保存的作用域并非「意外关闭」：后端解析为默认拒绝，徽章明确说明。
 */
export function ScopeStatusBadge({ policy }: ScopeStatusBadgeProps) {
  if (!policy) {
    return (
      <Badge variant="outline" title="未保存策略，后端按默认关闭处理">
        默认关闭
      </Badge>
    )
  }
  if (policy.enabled) {
    return <Badge variant="success">已启用</Badge>
  }
  return (
    <Badge variant="warning" title="已保存策略但 enabled=false">
      已保存未启用
    </Badge>
  )
}
