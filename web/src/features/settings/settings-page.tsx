import { useEffect, useMemo, useRef, useState } from 'react'
import { Archive, BrainCircuit, RefreshCw, Wrench } from 'lucide-react'
import { toast } from 'sonner'
import { ConfirmDialog } from '../../components/confirm-dialog'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Skeleton } from '../../components/ui/skeleton'
import { ApiError } from '../../lib/api/client'
import type { SystemSettings } from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { formatDateTime } from '../../lib/format'
import { ModelSection } from './components/model-section'
import { RetentionSection } from './components/retention-section'
import { SaveBar } from './components/save-bar'
import { SectionNav, type SectionNavItem } from './components/section-nav'
import { ToolsSection } from './components/tools-section'
import { countChanges, validateSettings } from './schema'
import { useDraft } from './draft'
import { settingsErrorCopy } from './error-copy'
import { useResetSystemSettings, useSaveSystemSettings, useSystemSettings } from './queries'

const SECTIONS: SectionNavItem[] = [
  { id: 'model', label: '模型与意图', hint: '生成模型与决策阈值', icon: BrainCircuit },
  { id: 'tools', label: '工具与能力', hint: '提供方、运行限制与调用预算', icon: Wrench },
  { id: 'retention', label: '消息与留存', hint: '全局消息保留期限', icon: Archive },
]

/**
 * System settings page: one place for the model, the outbound budget and the
 * tool registry. Saving swaps the live snapshot, so the next conversation uses
 * the new values without restarting the process.
 * 系统配置页：集中维护模型、出站预算与工具注册表。保存即替换实时快照，
 * 下一次会话无需重启即可使用新配置。
 */
export function SettingsPage() {
  const settingsQuery = useSystemSettings()
  const saveMutation = useSaveSystemSettings()
  const resetMutation = useResetSystemSettings()
  const { draft, serverErrors, setServerErrors, reset, update } = useDraft()
  const [activeSection, setActiveSection] = useState(SECTIONS[0].id)
  const [resetOpen, setResetOpen] = useState(false)
  const [message, setMessage] = useState<{ tone: 'success' | 'warning'; text: string } | null>(null)
  const messageTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const data = settingsQuery.data

  useEffect(() => {
    if (data) {
      reset(data.settings)
    }
    // Re-seed only when the stored revision changes, never on every render.
    // 仅在已存储的修订变化时重置草稿，避免覆盖正在编辑的内容。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data?.version])

  useEffect(() => () => {
    if (messageTimer.current) {
      clearTimeout(messageTimer.current)
    }
  }, [])

  // Scroll spy: the nav highlights the section currently under the viewport.
  // 滚动高亮：导航标记当前处于视口内的分区。
  const ready = draft !== null
  useEffect(() => {
    if (!ready || typeof IntersectionObserver === 'undefined') {
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((left, right) => left.boundingClientRect.top - right.boundingClientRect.top)[0]
        if (visible) {
          setActiveSection(visible.target.id)
        }
      },
      { rootMargin: '-25% 0px -60% 0px' },
    )
    for (const section of SECTIONS) {
      const element = document.getElementById(section.id)
      if (element) {
        observer.observe(element)
      }
    }
    return () => observer.disconnect()
  }, [ready])

  const clientErrors = useMemo(
    () => (draft && data ? validateSettings(draft) : {}),
    [draft, data],
  )
  const errors = Object.keys(serverErrors).length > 0 ? serverErrors : clientErrors
  const dirtyCount = draft && data ? countChanges(data.settings, draft) : 0

  const flash = (next: { tone: 'success' | 'warning'; text: string }) => {
    setMessage(next)
    if (messageTimer.current) {
      clearTimeout(messageTimer.current)
    }
    messageTimer.current = setTimeout(() => setMessage(null), 6000)
  }

  const scrollTo = (id: string) => {
    setActiveSection(id)
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  const handleSave = () => {
    if (!draft || !data) {
      return
    }
    if (Object.keys(clientErrors).length > 0) {
      flash({ tone: 'warning', text: '请先修正标红的配置项' })
      const firstField = Object.keys(clientErrors)[0]
      document
        .querySelector(`[data-field="${firstField}"]`)
        ?.scrollIntoView({ behavior: 'smooth', block: 'center' })
      return
    }
    saveMutation.mutate(
      { settings: draft, version: data.version },
      {
        onSuccess: (view) => {
          reset(view.settings)
          flash({ tone: 'success', text: '配置已保存，立即生效' })
          toast.success('系统配置已保存')
        },
        onError: (error) => {
          if (error instanceof ApiError && error.fields) {
            setServerErrors(error.fields)
            flash({ tone: 'warning', text: '后端拒绝了部分配置，请查看标红字段' })
            return
          }
          if (error instanceof ApiError && error.kind === 'conflict') {
            flash({ tone: 'warning', text: '配置已被其他页面修改，请刷新后重试' })
            return
          }
          flash({ tone: 'warning', text: describeError(error).description })
        },
      },
    )
  }

  if (settingsQuery.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-72 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    )
  }

  if (settingsQuery.error) {
    // The variant drives the icon and action; the copy distinguishes an old
    // binary (404) from a missing database (503).
    // 图标与操作由 variant 决定；文案区分旧二进制（404）与缺数据库（503）。
    const described = describeError(settingsQuery.error)
    const copy = settingsErrorCopy(settingsQuery.error)
    return (
      <FeedbackState
        variant={described.variant}
        error={settingsQuery.error}
        title={copy.title}
        description={copy.description}
        onRetry={() => void settingsQuery.refetch()}
      />
    )
  }

  if (!draft || !data) {
    return null
  }

  const sectionProps = {
    draft,
    errors,
    credentials: data.credentials,
    providers: data.providers,
    updateAI: (patch: Partial<SystemSettings['ai']>) => update((next) => Object.assign(next.ai, patch)),
    updateIntent: (patch: Partial<SystemSettings['intent']>) => update((next) => Object.assign(next.intent, patch)),
    updateAgent: (patch: Partial<SystemSettings['agent']>) => update((next) => Object.assign(next.agent, patch)),
    updateMessages: (patch: Partial<SystemSettings['messages']>) =>
      update((next) => Object.assign(next.messages, patch)),
    updateTools: (patch: Partial<SystemSettings['tools']>) => update((next) => Object.assign(next.tools, patch)),
  }

  return (
    <div className="space-y-4 pb-4">
      <PageHeader
        title="系统配置"
        description="集中管理模型、意图、运行限制与全局消息留存；保存后立即生效，无需重启进程。密钥类配置仍由配置文件或环境变量维护。"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={data.credentials.ai ? 'success' : 'warning'}>
              {data.credentials.ai ? 'AI 密钥已配置' : 'AI 密钥缺失'}
            </Badge>
            <Badge variant={data.persisted ? 'outline' : 'destructive'}>
              {data.persisted ? `修订 v${data.version}` : '未持久化'}
            </Badge>
            <Button variant="outline" size="sm" onClick={() => void settingsQuery.refetch()}>
              <RefreshCw className="size-3.5" aria-hidden />
              刷新
            </Button>
          </div>
        }
      />

      <p className="text-meta text-muted-foreground">
        {data.updatedAt
          ? `最后保存于 ${formatDateTime(data.updatedAt)}${data.updatedBy ? ` · ${data.updatedBy}` : ''}`
          : '尚未保存过：当前展示的是配置文件基线。'}
        {data.persisted ? '' : ' 当前进程没有数据库，改动只对本次运行有效。'}
      </p>

      <div className="flex items-start gap-6">
        <SectionNav items={SECTIONS} activeId={activeSection} onSelect={scrollTo} />
        <div className="min-w-0 flex-1 space-y-4">
          <div data-field="AI.Enabled">
            <ModelSection {...sectionProps} />
          </div>
          <div data-field="Agent.MaxSteps">
            <ToolsSection {...sectionProps} />
          </div>
          <div data-field="Messages.RetentionDays">
            <RetentionSection {...sectionProps} />
          </div>

          <SaveBar
            dirtyCount={dirtyCount}
            errorCount={Object.keys(errors).length}
            saving={saveMutation.isPending}
            resetting={resetMutation.isPending}
            message={message}
            onSave={handleSave}
            onDiscard={() => {
              reset(data.settings)
              flash({ tone: 'success', text: '已放弃未保存的修改' })
            }}
            onReset={() => setResetOpen(true)}
          />
        </div>
      </div>

      <ConfirmDialog
        open={resetOpen}
        onOpenChange={setResetOpen}
        title="恢复配置文件默认值？"
        description="这会把所有分区（模型、意图、预算、工具）写回配置文件的基线，并生成一个新修订。"
        details={['已单独保存过的会话策略不会被修改', '恢复后立即生效，进行中的运行不受影响']}
        confirmLabel="恢复默认"
        destructive
        pending={resetMutation.isPending}
        onConfirm={() => {
          resetMutation.mutate(undefined, {
            onSuccess: (view) => {
              reset(view.settings)
              setResetOpen(false)
              flash({ tone: 'success', text: '已恢复配置文件默认值' })
            },
            onError: (error) => {
              setResetOpen(false)
              flash({ tone: 'warning', text: describeError(error).description })
            },
          })
        }}
      />
    </div>
  )
}
