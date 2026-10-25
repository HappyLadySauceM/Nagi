import { Badge } from '../../../components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../../components/ui/table'
import type { GroupMember } from '../../../lib/api/contracts'
import { formatDateTime, formatRelativeTime } from '../../../lib/format'

const ROLE_LABELS: Record<string, string> = {
  owner: '群主',
  admin: '管理员',
  member: '成员',
}

const ROLE_VARIANTS: Record<string, 'default' | 'secondary' | 'outline'> = {
  owner: 'default',
  admin: 'secondary',
  member: 'outline',
}

function roleLabel(role: string): string {
  return ROLE_LABELS[role] ?? role ?? '未知'
}

interface MembersTableProps {
  members: GroupMember[]
}

export function MembersTable({ members }: MembersTableProps) {
  return (
    <Table label="群成员列表">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>成员</TableHead>
          <TableHead>角色</TableHead>
          <TableHead className="text-right">QQ 号</TableHead>
          <TableHead className="text-right">最近同步</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {members.map((member) => (
          <TableRow key={member.userId}>
            <TableCell>
              <div className="min-w-0">
                <p className="truncate text-xs font-medium text-foreground">
                  {member.card || member.nickname || '未命名成员'}
                </p>
                {member.card && member.nickname ? (
                  <p className="truncate text-meta text-muted-foreground">
                    昵称 {member.nickname}
                  </p>
                ) : null}
              </div>
            </TableCell>
            <TableCell>
              <Badge variant={ROLE_VARIANTS[member.role] ?? 'outline'}>
                {roleLabel(member.role)}
              </Badge>
            </TableCell>
            <TableCell className="text-right font-mono text-muted-foreground tabular-nums">
              {member.userId}
            </TableCell>
            <TableCell
              className="whitespace-nowrap text-right text-muted-foreground tabular-nums"
              title={member.lastSeenAt ? formatDateTime(member.lastSeenAt) : undefined}
            >
              {member.lastSeenAt ? formatRelativeTime(member.lastSeenAt) : '未记录'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
