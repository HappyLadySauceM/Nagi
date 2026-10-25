import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Copy, ShieldCheck, UsersRound } from 'lucide-react'
import { toast } from 'sonner'
import { Avatar } from '../../../components/avatar'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../../../components/ui/card'
import { InfoRow } from '../../../components/info-row'
import type { ScopePolicy } from '../../../lib/api/contracts'
import { formatCount, formatDateTime, formatRelativeTime } from '../../../lib/format'
import { useAccountContext } from '../../accounts/account-context'
import { ScopeEditDialog } from './scope-edit-dialog'
import { ScopeStatusBadge } from './scope-status-badge'

export interface DirectorySelection {
  kind: 'friend' | 'group'
  id: number
  name: string
  remark?: string
  memberCount?: number
  lastSeenAt?: string
  policy?: ScopePolicy
}

function copyId(id: number) {
  navigator.clipboard
    .writeText(String(id))
    .then(() => toast.success('已复制号码', { description: String(id) }))
    .catch((error: unknown) => {
      console.error(error)
      toast.error('复制失败，请手动选择号码文本')
    })
}

/**
 * Detail side of the master-detail layout. It only shows facts the backend
 * actually returned: no fabricated activity or member previews.
 * 主从布局的详情侧，只展示后端真实返回的事实，不编造活跃度或成员预览。
 */
export function ContactDetailPanel({ selection }: { selection?: DirectorySelection }) {
  const [editOpen, setEditOpen] = useState(false)
  const { selectedBotId } = useAccountContext()

  if (!selection) {
    return (
      <Card className="flex min-h-[320px] items-center justify-center">
        <p className="max-w-xs text-center text-body leading-relaxed text-muted-foreground">
          从左侧选择一个好友或群聊，查看资料、同步时间与作用域策略。
        </p>
      </Card>
    )
  }

  const isGroup = selection.kind === 'group'
  const scopeType = isGroup ? 'group' : 'private'
  const title = selection.name || (isGroup ? '未命名群聊' : '未命名好友')

  return (
    <>
      <Card className="flex flex-col">
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-3 space-y-0">
        <div className="flex min-w-0 items-center gap-3">
          <Avatar id={selection.id} kind={isGroup ? 'group' : 'user'} name={title} size="lg" />
          <div className="min-w-0">
            <CardTitle className="truncate text-title">{title}</CardTitle>
            <p className="mt-1 font-mono text-meta text-muted-foreground tabular-nums">
              {isGroup ? '群号' : 'QQ'} {selection.id}
            </p>
          </div>
        </div>
        <Badge variant={isGroup ? 'accent' : 'secondary'}>{isGroup ? '群聊' : '好友'}</Badge>
      </CardHeader>

      <CardContent className="pt-2">
        <InfoRow label={isGroup ? '群名称' : '昵称'} value={selection.name || '未记录'} />
        {isGroup ? (
          <InfoRow
            label="成员数"
            value={selection.memberCount === undefined ? '未知' : formatCount(selection.memberCount)}
            mono
          />
        ) : (
          <InfoRow label="备注" value={selection.remark || '未设置'} />
        )}
        <InfoRow
          label="最近同步"
          value={
            selection.lastSeenAt
              ? formatRelativeTime(selection.lastSeenAt)
              : '尚无同步记录'
          }
        />
        <div className="flex items-center justify-between gap-3 border-b border-border/60 py-2.5">
          <span className="text-meta text-muted-foreground">作用域策略</span>
          <ScopeStatusBadge policy={selection.policy} />
        </div>

        {!selection.policy?.enabled ? (
          <p className="mt-3 rounded-lg border border-border/70 bg-secondary/40 px-3 py-2 text-meta leading-relaxed text-muted-foreground">
            该目标默认关闭：机器人不会回复、不保存消息，也不写入记忆。需要回复时先保存一份启用的策略。
          </p>
        ) : null}

        <div className="mt-4 flex flex-wrap gap-2">
          <Button
            size="sm"
            disabled={selectedBotId === undefined}
            onClick={() => setEditOpen(true)}
          >
            <ShieldCheck aria-hidden />
            {selection.policy ? '编辑策略' : '配置策略'}
          </Button>
          {isGroup ? (
            <Button asChild variant="outline" size="sm">
              <Link
                to="/directory/$groupId"
                params={{ groupId: String(selection.id) }}
                search={{ search: '', page: 1, limit: 20, name: selection.name }}
              >
                <UsersRound aria-hidden />
                查看成员
              </Link>
            </Button>
          ) : null}
          <Button variant="ghost" size="sm" onClick={() => copyId(selection.id)}>
            <Copy aria-hidden />
            复制号码
          </Button>
        </div>

        {selection.lastSeenAt ? (
          <p className="mt-3 text-caption text-muted-foreground">
            最近一次同步：{formatDateTime(selection.lastSeenAt)}
          </p>
        ) : null}
      </CardContent>
    </Card>
      {selectedBotId !== undefined ? (
        <ScopeEditDialog
          open={editOpen}
          onOpenChange={setEditOpen}
          botId={selectedBotId}
          scopeType={scopeType}
          scopeId={selection.id}
          targetLabel={title}
          savedPolicy={selection.policy}
        />
      ) : null}
    </>
  )
}
