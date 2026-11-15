import { RefreshCw, ShieldCheck } from 'lucide-react'
import { FeedbackState } from '../../../components/feedback-state'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { Skeleton } from '../../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../../components/ui/table'
import { RULE_ACTION_LABELS, type ScopeType } from '../../../lib/api/contracts'
import { useBuiltinRuleCatalog, useEffectiveRules } from '../rules-queries'



function sourceLabel(source: string): string {
  switch (source) {
    case 'builtin':
      return '内置'
    case 'local':
      return '本地规则集'
    case 'global':
      return '全局规则集'
    case 'specific':
      return '指定会话规则集'
    case 'server':
      return '服务端'
    default:
      return source || '未知'
  }
}

function scopeLabel(scopeType: ScopeType, scopeId: number): string {
  return `${scopeType === 'group' ? '群聊' : '私聊'} ${scopeId > 0 ? scopeId : '所有会话'}`
}

/**
 * Read-only builtin rule catalog and effective view. The catalog owns titles,
 * order, conditions and actions so the page never invents a second definition.
 * 只读内置规则目录与生效视图；标题、顺序、条件与动作全部以后端目录为准，
 * 页面不维护第二份定义。
 */
export function BuiltinRulesPanel({
  botId,
  scopeType,
  scopeId,
}: {
  botId?: number
  scopeType: ScopeType
  scopeId: number
}) {
  const catalog = useBuiltinRuleCatalog()
  const effective = useEffectiveRules(
    botId !== undefined && Number.isInteger(scopeId) && scopeId >= 0
      ? { botId, scopeType, scopeId }
      : null,
  )
  const rules = catalog.data?.rules ?? []
  const states = effective.data?.builtinRules ?? []
  const fixedConstraints =
    effective.data?.fixedConstraints ??
    (catalog.data?.fixedConstraints ?? []).map((constraint) => ({
      ...constraint,
      applicable: true,
      status: 'active',
      reason: '',
    }))
  const activeRuleset = effective.data?.activeRuleset

  return (
    <div className="space-y-4">
      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-border/60 px-5 py-4">
          <ShieldCheck className="size-4 text-muted-foreground" aria-hidden />
          <h2 className="text-sm font-semibold">内置规则目录</h2>
          <Badge variant="outline">与执行同源 · 只读</Badge>
          {catalog.data ? (
            <Badge variant="secondary">版本 {catalog.data.version || '—'}</Badge>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto h-8 px-2 text-meta"
            onClick={() => {
              void catalog.refetch()
              void effective.refetch()
            }}
          >
            <RefreshCw
              className={catalog.isFetching || effective.isFetching ? 'size-3 animate-spin' : 'size-3'}
              aria-hidden
            />
            刷新
          </Button>
        </div>

        {catalog.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 5 }).map((_, index) => (
              <Skeleton key={index} className="h-10 w-full" />
            ))}
          </div>
        ) : catalog.isError ? (
          <FeedbackState
            variant="error"
            title="读取内置规则目录失败"
            error={catalog.error}
            onRetry={() => void catalog.refetch()}
          />
        ) : rules.length === 0 ? (
          <FeedbackState
            variant="empty"
            title="没有内置规则"
            description="后端未返回内置规则定义，请确认当前后端版本已提供规则目录接口。"
          />
        ) : (
          <Table label="内置规则目录">
            <TableHeader>
              <TableRow>
                <TableHead>顺序</TableHead>
                <TableHead>规则</TableHead>
                <TableHead>实际条件</TableHead>
                <TableHead>动作</TableHead>
                <TableHead>来源</TableHead>
                <TableHead>当前状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((rule) => {
                const state = states.find((item) => item.ruleId === rule.ruleId)
                const notApplicable = state !== undefined && (state.status === 'not_applicable' || !state.applicable)
                const disabled = state !== undefined && state.reason === 'disabled'
                const statusLabel =
                  state === undefined ? '—' : state.status === 'active' ? '启用' : state.reason === 'disabled' ? '已关闭' : '不适用'
                return (
                  <TableRow key={rule.ruleId}>
                    <TableCell className="font-mono text-meta tabular-nums">{rule.order}</TableCell>
                    <TableCell>
                      <div className="text-sm font-medium">{rule.title}</div>
                      <div className="mt-0.5 font-mono text-meta text-muted-foreground">{rule.ruleId}</div>
                    </TableCell>
                    <TableCell className="max-w-[320px] text-meta text-muted-foreground">
                      {notApplicable ? '不适用' : rule.condition || '—'}
                    </TableCell>
                    <TableCell className="text-meta">
                      {notApplicable ? '不适用' : (RULE_ACTION_LABELS[rule.action] ?? rule.action)}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{sourceLabel(rule.source)}</Badge>
                      <div className="mt-1 text-meta text-muted-foreground">
                        {rule.editable ? '可在草稿中开关' : '只读'}
                      </div>
                    </TableCell>
                    <TableCell>
                      {state === undefined ? (
                        <Badge variant="outline">—</Badge>
                      ) : statusLabel === '启用' ? (
                        <Badge variant="success">启用</Badge>
                      ) : disabled ? (
                        <Badge variant="warning">已关闭</Badge>
                      ) : (
                        <Badge variant="outline">不适用</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}

        <div className="grid gap-2 border-t border-border/60 px-5 py-4 text-xs sm:grid-cols-3">
          <div>
            <div className="text-muted-foreground">实际目标</div>
            <div className="mt-1 font-medium text-foreground">{scopeLabel(scopeType, scopeId)}</div>
          </div>
          <div>
            <div className="text-muted-foreground">生效版本 / 来源</div>
            <div className="mt-1 font-medium text-foreground">
              {effective.data?.version || catalog.data?.version || '—'} · {sourceLabel(effective.data?.source ?? 'builtin')}
            </div>
          </div>
          <div>
            <div className="text-muted-foreground">自定义规则</div>
            <div className="mt-1 font-medium text-foreground">
              {activeRuleset
                ? `v${activeRuleset.version} · ${activeRuleset.status === 'published' ? '已发布' : activeRuleset.status}`
                : '无（使用内置规则）'}
            </div>
          </div>
        </div>
        {effective.isError ? (
          <div className="border-t border-border/60 px-5 py-3">
            <FeedbackState
              compact
              variant="error"
              title="读取实际生效规则失败"
              error={effective.error}
              onRetry={() => void effective.refetch()}
            />
          </div>
        ) : null}
        {botId === undefined ? (
          <p className="border-t border-border/60 px-5 py-3 text-xs text-muted-foreground">
            选择机器人账号后显示该目标的实际开关状态。
          </p>
        ) : null}
      </Card>

      <Card className="space-y-2 p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-sm font-semibold">固定约束</h2>
          <Badge variant="outline">服务端始终生效</Badge>
        </div>
        {fixedConstraints.length === 0 ? (
          <p className="text-xs text-muted-foreground">后端未返回固定约束目录；请确认后端版本。</p>
        ) : (
          <dl className="grid gap-2 text-xs sm:grid-cols-2">
            {fixedConstraints.map((constraint) => {
              const notApplicable = !constraint.applicable
              return (
                <div key={constraint.constraintId} className="rounded-lg border border-border/70 bg-secondary/20 px-3 py-2">
                  <dt className="flex flex-wrap items-center gap-2 font-medium text-foreground">
                    {constraint.title}
                    <Badge variant={constraint.status === 'active' ? 'secondary' : 'outline'}>
                      {constraint.status === 'active' ? '生效' : '不适用'}
                    </Badge>
                  </dt>
                  <dd className="mt-1 leading-relaxed text-muted-foreground">
                    {notApplicable ? '不适用' : `${constraint.condition || '—'} → ${constraint.action || '—'}`}
                  </dd>
                </div>
              )
            })}
          </dl>
        )}
        <p className="text-xs text-muted-foreground">
          规则页面只展示并编辑规则目录与版本草稿；硬权限、observe、频率和预算不提供绕过入口。图与执行共用后端同一份规则定义，页面不写死条件与顺序。
        </p>
      </Card>
    </div>
  )
}
