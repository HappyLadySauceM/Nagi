import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Copy, ShieldCheck, UsersRound } from 'lucide-react'
import { toast } from 'sonner'
import { Avatar } from '../../../components/avatar'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { InfoRow } from '../../../components/info-row'
import type { ScopePolicy } from '../../../lib/api/contracts'
import { REPLY_MODE_LABELS } from '../../../lib/api/contracts'
import { formatCount, formatRelativeTime } from '../../../lib/format'
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
    return <div className="flex min-h-[320px] items-center justify-center p-8"><p className="max-w-xs text-center text-sm leading-relaxed text-muted-foreground">从左侧选择一个好友或群聊，查看资料与参与策略。</p></div>
  }

  const isGroup = selection.kind === 'group'
  const scopeType = isGroup ? 'group' : 'private'
  const title = selection.name || (isGroup ? '未命名群聊' : '未命名好友')

  return (
    <>
      <div className="p-5 sm:p-7">
        <header className="flex flex-wrap items-start justify-between gap-4 border-b border-border pb-6">
          <div className="flex min-w-0 items-center gap-4">
            <Avatar id={selection.id} kind={isGroup ? 'group' : 'user'} name={title} size="lg" />
            <div className="min-w-0"><h2 className="line-clamp-2 break-words text-xl font-semibold">{title}</h2><p className="mt-1 font-mono text-meta text-muted-foreground">{isGroup ? '群号' : 'QQ'} {selection.id}</p></div>
          </div>
          <Badge variant="secondary">{isGroup ? '群聊' : '好友'}</Badge>
        </header>
        <section className="py-6" aria-label="联系人资料">
          <h3 className="mb-3 text-sm font-medium">基本资料</h3>
          <InfoRow label={isGroup ? '群名称' : '名称'} value={selection.name || '未记录'} />
          {isGroup ? <InfoRow label="成员数" value={selection.memberCount === undefined ? '未知' : formatCount(selection.memberCount)} mono /> : <InfoRow label="昵称" value={selection.remark || '未设置'} />}
          <InfoRow label="最近同步" value={selection.lastSeenAt ? formatRelativeTime(selection.lastSeenAt) : '尚无同步记录'} />
          <div className="mt-4 flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => copyId(selection.id)}><Copy aria-hidden />复制号码</Button>
            {isGroup ? <Button asChild variant="outline" size="sm"><Link to="/directory/$groupId" params={{ groupId: String(selection.id) }} search={{ search: '', page: 1, limit: 20, name: selection.name }}><UsersRound aria-hidden />查看成员</Link></Button> : null}
          </div>
        </section>
        <section className="border-t border-border py-6" aria-label="参与策略">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3"><h3 className="text-sm font-medium">参与策略</h3><ScopeStatusBadge policy={selection.policy} /></div>
          <InfoRow label="策略来源" value={!selection.policy ? '正在读取' : selection.policy.source === 'default' ? (isGroup ? '跟随群聊默认' : '跟随好友默认') : '独立策略'} />
          <InfoRow label="回复模式" value={selection.policy ? REPLY_MODE_LABELS[selection.policy.replyMode] : '未加载'} />
          {selection.policy && !selection.policy.enabled ? <p className="mt-4 rounded-md border border-border bg-muted/40 px-3 py-2.5 text-meta leading-relaxed text-muted-foreground">当前会话已关闭：机器人不会回复、不保存消息，也不写入记忆。编辑并启用策略后开始参与。</p> : null}
          <Button className="mt-5" size="sm" disabled={selectedBotId === undefined} onClick={() => setEditOpen(true)}><ShieldCheck aria-hidden />编辑策略</Button>
        </section>
      </div>
      {selectedBotId !== undefined && editOpen ? (
        <ScopeEditDialog
          open
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
