import {
  useModels,
  type ModelProfile,
  type ModelView,
  type Usage,
} from '../models'
import { arkVoices } from '../ark-voices'
import { confirmAction } from '../../../components/confirm-action'
import { useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus,
  Settings2,
  Hammer,
  Eye,
  Brain,
  Mic,
  Volume2,
  ChevronLeft,
  ChevronRight,
} from 'lucide-react'
import { apiRequest } from '../../../lib/api/client'
import { Button } from '../../../components/ui/button'
import { Input } from '../../../components/ui/input'
import { Switch } from '../../../components/ui/switch'
import { Select } from '../../../components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetTitle,
  SheetDescription,
} from '../../../components/ui/sheet'
import { ConfirmDialog } from '../../../components/confirm-dialog'
import { FeedbackState } from '../../../components/feedback-state'
import { useEditGuard } from '../../intent/edit-guard'
import { queryKeys } from '../../../lib/query-keys'
import type { SectionProps } from './types'

const LABELS: Record<Usage, string> = {
  generation: '生成模型',
  intent: '意图模型',
  asr: '语音识别',
  tts: '语音合成',
}
const emptyProfile = (): ModelProfile => ({
  ID: '',
  Name: '',
  Usage: 'generation',
  Protocol: 'openai',
  BaseURL: '',
  Model: '',
  Images: false,
  Tools: true,
  Voice: '',
  MaxOutputTokens: 0,
  TimeoutSeconds: 60,
  Version: 0,
  HasCredential: false,
})
function useModelChange() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({
      path,
      method,
      body,
      signal,
    }: {
      path: string
      method: 'POST' | 'PUT' | 'PATCH' | 'DELETE'
      body: unknown
      signal?: AbortSignal
    }) =>
      apiRequest<ModelView>(path, { method, body, signal, timeoutMs: 65000 }),
    onSuccess: (view) => {
      client.setQueryData(['models'], view)
      void client.invalidateQueries({ queryKey: queryKeys.settings })
    },
  })
}

/** Each saved endpoint has an independent editing drawer and write-only credential.
 * 每个已保存端点使用独立编辑抽屉及只写凭证。 */
export function ModelSection(_props: SectionProps) {
  const testController = useRef<AbortController | null>(null)
  const query = useModels()
  const change = useModelChange()
  const [editing, setEditing] = useState<ModelProfile | null>(null)
  const [base, setBase] = useState('')
  const [headers, setHeaders] = useState<
    Array<{ key: string; value: string; saved: boolean }>
  >([])
  const [headerBase, setHeaderBase] = useState('[]')
  const [headerError, setHeaderError] = useState('')
  const [version, setVersion] = useState(0)
  const [token, setToken] = useState('')
  const [customEffort, setCustomEffort] = useState(false)

  const [cookie, setCookie] = useState('')
  const [clearCookie, setClearCookie] = useState(false)
  const dirty =
    editing !== null &&
    (JSON.stringify(editing) !== base ||
      token !== '' ||
      JSON.stringify(headers) !== headerBase)
  useEditGuard(dirty || cookie !== '' || clearCookie, change.isPending)
  const [discardOpen, setDiscardOpen] = useState(false)
  const discard = () => {
    setDiscardOpen(false)
    setEditing(null)
    setToken('')
    change.reset()
  }
  const close = () => {
    if (change.isPending) return
    if (dirty) {
      setDiscardOpen(true)
      return
    }
    setEditing(null)
    setToken('')
    change.reset()
  }
  const edit = (p: ModelProfile) => {
    const rows = Object.keys(p.HTTPHeaders ?? {}).map((key) => ({
      key,
      value: '',
      saved: true,
    }))
    setHeaders(rows)
    setHeaderBase(JSON.stringify(rows))
    setHeaderError('')
    setCustomEffort(
      Boolean(
        p.ReasoningEffort &&
        !'low,medium,high,max'.split(',').includes(p.ReasoningEffort),
      ),
    )
    setEditing({ ...p })
    setBase(JSON.stringify(p))
    setVersion(query.data?.Version ?? 0)
    setToken('')
    change.reset()
  }
  if (query.isLoading) return <p role="status">正在加载模型配置…</p>
  if (query.isError)
    return (
      <FeedbackState
        variant="error"
        error={query.error}
        onRetry={() => void query.refetch()}
      />
    )
  if (!query.data) return null
  const view = query.data
  const patch = (p: Partial<ModelProfile>) =>
    setEditing((current) => (current ? { ...current, ...p } : current))
  return (
    <section
      id="model"
      className="space-y-4 rounded-lg border border-border p-5"
    >
      <div className="flex items-center justify-between">
        <h2 className="font-medium">模型配置</h2>
        <Button variant="outline" onClick={() => edit(emptyProfile())}>
          <Plus aria-hidden />
          新增模型
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        在侧边配置模型和凭证；每个会话可选择自己的生成模型。模型修改单独保存后生效。已启用会话的图片和语音会交给所配置的模型处理，历史内容遵守会话的数据权限。
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        {(Object.keys(LABELS) as Usage[]).map((usage) => (
          <label key={usage} className="space-y-2 text-sm">
            <span>默认{LABELS[usage]}</span>
            <Select
              aria-label={`默认${LABELS[usage]}`}
              value={view.Catalog.Defaults[usage] ?? ''}
              disabled={change.isPending}
              onChange={(e) =>
                change.mutate({
                  path: '/api/v1/models',
                  method: 'PATCH',
                  body: {
                    Defaults: { [usage]: e.target.value },
                    Version: view.Version,
                  },
                })
              }
            >
              <option value="">未配置</option>
              {view.Catalog.Profiles.filter((p) => p.Usage === usage).map(
                (p) => (
                  <option key={p.ID} value={p.ID}>
                    {p.Name}
                  </option>
                ),
              )}
            </Select>
          </label>
        ))}
      </div>
      {view.Catalog.Profiles.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          尚未配置模型，请新增模型后选择默认用途。
        </p>
      ) : (
        view.Catalog.Profiles.map((p) => (
          <div
            key={p.ID}
            className="flex items-center justify-between gap-4 rounded-md border border-border p-3"
          >
            <div>
              <p className="text-sm font-medium">
                {p.Name} · {LABELS[p.Usage]}
              </p>
              <div className="flex gap-3 text-xs text-muted-foreground">
                {p.Tools && p.Usage === 'generation' && (
                  <span className="inline-flex items-center gap-1 rounded-md border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-amber-500">
                    <Hammer size={14} className="text-amber-400" />
                    工具调用
                  </span>
                )}
                {p.Images && p.Usage === 'generation' && (
                  <span className="inline-flex items-center gap-1 rounded-md border border-sky-500/30 bg-sky-500/10 px-2 py-1 text-sky-500">
                    <Eye size={14} className="text-sky-400" />
                    多模态
                  </span>
                )}
                {p.Usage === 'asr' && (
                  <span className="inline-flex items-center gap-1 rounded-md border border-teal-500/30 bg-teal-500/10 px-2 py-1 text-teal-500">
                    <Mic size={14} />
                    语音识别
                  </span>
                )}
                {p.Usage === 'tts' && (
                  <span className="inline-flex items-center gap-1 rounded-md border border-pink-500/30 bg-pink-500/10 px-2 py-1 text-pink-500">
                    <Volume2 size={14} />
                    语音合成
                  </span>
                )}
                {p.Protocol === 'jev' && (
                  <span className="inline-flex items-center gap-1 rounded-md border border-violet-500/30 bg-violet-500/10 px-2 py-1 text-violet-500">
                    <Brain size={14} className="text-violet-400" />
                    SystemOne
                  </span>
                )}
              </div>
              <p className="text-xs text-muted-foreground">
                {p.Model} · {p.HasCredential ? '凭证已配置' : '未配置凭证'} ·{' '}
                {p.VerifiedVersion === p.Version && p.VerifiedAt
                  ? '测试通过'
                  : '需要重新测试'}
              </p>
            </div>
            <Button variant="outline" onClick={() => edit(p)}>
              <Settings2 aria-hidden />
              配置
            </Button>
          </div>
        ))
      )}
      <div className="space-y-2 border-t border-border pt-4">
        <h3 className="text-sm font-medium">
          B 站登录凭证 ·{' '}
          {view.Catalog.HasBilibiliCredential
            ? '已配置'
            : '未配置，使用匿名访问'}
        </h3>
        <Input
          type="password"
          autoComplete="new-password"
          aria-label="B站 Cookie"
          value={cookie}
          placeholder="粘贴 Cookie，留空保留原值"
          onChange={(e) => setCookie(e.target.value)}
        />
        <Button
          variant="outline"
          disabled={change.isPending || !view.Catalog.HasBilibiliCredential}
          onClick={async () => {
            if (
              await confirmAction(
                '确认清除已保存的 B 站 Cookie？之后将使用匿名访问。',
              )
            )
              change.mutate(
                {
                  path: '/api/v1/models',
                  method: 'PATCH',
                  body: { Version: view.Version, ClearCookie: true },
                },
                {
                  onSuccess: () => {
                    setCookie('')
                    setClearCookie(false)
                  },
                },
              )
          }}
        >
          清除已保存的 Cookie
        </Button>
        <Button
          variant="outline"
          disabled={change.isPending || (!cookie && !clearCookie)}
          onClick={() =>
            change.mutate(
              {
                path: '/api/v1/models',
                method: 'PATCH',
                body: {
                  Version: view.Version,
                  ...(cookie ? { Cookie: cookie } : {}),
                  ClearCookie: clearCookie,
                },
              },
              {
                onSuccess: () => {
                  setCookie('')
                  setClearCookie(false)
                },
              },
            )
          }
        >
          保存站点凭证
        </Button>
      </div>
      {change.isError && !editing ? (
        <p role="alert" className="text-sm text-destructive">
          {change.error.message}
        </p>
      ) : null}
      <Sheet
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) close()
        }}
      >
        <SheetContent
          onOpenAutoFocus={(event) => {
            event.preventDefault()
          }}
          side="right"
          className="w-full overflow-y-auto bg-background p-6 sm:max-w-3xl"
        >
          <div className="flex items-center justify-between pr-8">
            <SheetTitle tabIndex={-1}>
              {editing?.ID ? '配置模型' : '新增模型'}
            </SheetTitle>
            {editing && (
              <div className="flex items-center gap-3" aria-label="模型用途">
                <Button
                  type="button"
                  variant="ghost"
                  disabled={change.isPending}
                  aria-label="上一个模型用途"
                  onClick={() => {
                    const usages = Object.keys(LABELS) as Usage[]
                    const usage =
                      usages[(usages.indexOf(editing.Usage) + 3) % 4]
                    patch({
                      Usage: usage,
                      Protocol: usage === 'intent' ? 'jev' : 'openai',
                    })
                  }}
                >
                  <ChevronLeft />
                </Button>
                <span>{LABELS[editing.Usage]}</span>
                <Button
                  type="button"
                  variant="ghost"
                  disabled={change.isPending}
                  aria-label="下一个模型用途"
                  onClick={() => {
                    const usages = Object.keys(LABELS) as Usage[]
                    const usage =
                      usages[(usages.indexOf(editing.Usage) + 1) % 4]
                    patch({
                      Usage: usage,
                      Protocol: usage === 'intent' ? 'jev' : 'openai',
                    })
                  }}
                >
                  <ChevronRight />
                </Button>
              </div>
            )}
          </div>
          <SheetDescription>
            凭证不会回显；留空保留原凭证。保存前将真实调用模型测试，可能产生少量费用。
          </SheetDescription>
          {editing ? (
            <form
              className="mt-6 grid grid-cols-1 gap-x-4 gap-y-5 rounded-xl border border-border/70 bg-muted/10 p-5 sm:grid-cols-2"
              onSubmit={(e) => {
                e.preventDefault()
                if (change.isPending) return
                const names = headers.map((h) => h.key.toLowerCase())
                if (
                  headers.some(
                    (h) => !h.key.trim() || (!h.saved && !h.value),
                  ) ||
                  new Set(names).size !== names.length
                ) {
                  setHeaderError(
                    '请填写请求头名称和值，名称不能重复（不区分大小写）',
                  )
                  return
                }
                setHeaderError('')
                testController.current = new AbortController()
                change.mutate(
                  {
                    signal: testController.current.signal,
                    path: editing.ID
                      ? `/api/v1/models/${editing.ID}`
                      : '/api/v1/models',
                    method: editing.ID ? 'PUT' : 'POST',
                    body: {
                      Version: version,
                      Profile: {
                        ...editing,
                        HTTPHeaders: Object.fromEntries(
                          headers.map((h) => [h.key, h.value]),
                        ),
                        TimeoutSeconds: 60,
                      },
                      ...(token ? { Credential: token } : {}),
                    },
                  },
                  {
                    onSuccess: () => {
                      setEditing(null)
                      setToken('')
                    },
                  },
                )
              }}
            >
              <fieldset disabled={change.isPending} className="contents">
                <div className="flex items-center gap-2 border-b border-border/60 pb-3 text-sm font-medium sm:col-span-2">
                  <Settings2 size={16} className="text-emerald-400" />
                  连接与凭证
                </div>
                <label className="block space-y-1 text-sm">
                  配置名称
                  <Input
                    required
                    value={editing.Name}
                    onChange={(e) => patch({ Name: e.target.value })}
                  />
                </label>
                {['intent', 'asr', 'tts'].includes(editing.Usage) ? (
                  <label className="block space-y-1 text-sm">
                    <span className="flex items-center gap-2">
                      <Brain size={15} className="text-violet-400" />
                      接口协议
                    </span>
                    <Select
                      aria-label="接口协议"
                      value={editing.Protocol}
                      onChange={(e) => {
                        const protocol = e.target.value
                        patch({
                          Protocol: protocol,
                          ...(protocol === 'ark-plan'
                            ? {
                                BaseURL:
                                  editing.Usage === 'asr'
                                    ? 'wss://openspeech.bytedance.com/api/v3/plan/sauc/bigmodel_nostream'
                                    : 'https://openspeech.bytedance.com/api/v3/plan/tts/unidirectional',
                                Model:
                                  editing.Usage === 'asr'
                                    ? 'doubao-seed-asr-2.0'
                                    : 'seed-tts-2.0',
                                Voice:
                                  editing.Usage === 'tts'
                                    ? 'zh_female_gaolengyujie_uranus_bigtts'
                                    : '',
                              }
                            : {}),
                        })
                      }}
                    >
                      <option value="openai">OpenAI 兼容接口</option>
                      {editing.Usage === 'intent' ? (
                        <option value="jev">SystemOne</option>
                      ) : (
                        <option value="ark-plan">
                          火山 Agent Plan 语音协议
                        </option>
                      )}
                    </Select>
                  </label>
                ) : null}
                {editing.Protocol === 'ark-plan' && (
                  <p className="text-xs text-muted-foreground sm:col-span-2">
                    使用 Agent Plan 专属 API Key。识别采用 WebSocket
                    上传服务端音频，合成采用 HTTP；请填写完整接口地址和官方音色
                    ID。无需添加认证请求头。
                    <a
                      className="ml-1 underline"
                      href="https://docs.volcengine.com/docs/ark/agent-plan-personal-voice-model?lang=zh"
                      target="_blank"
                      rel="noreferrer"
                    >
                      官方文档
                    </a>
                  </p>
                )}
                <label className="block space-y-1 text-sm">
                  接口地址
                  <Input
                    type="url"
                    required
                    placeholder="https://example.com/v1"
                    value={editing.BaseURL}
                    onChange={(e) => patch({ BaseURL: e.target.value })}
                  />
                </label>
                <label className="block space-y-1 text-sm">
                  模型名
                  <Input
                    required
                    value={editing.Model}
                    onChange={(e) => patch({ Model: e.target.value })}
                  />
                </label>
                <label className="block space-y-1 text-sm">
                  Token 凭证
                  <Input
                    type="password"
                    autoComplete="new-password"
                    placeholder={
                      editing.HasCredential
                        ? '已配置，留空保留'
                        : '可留空用于无需认证的本地服务'
                    }
                    value={token}
                    onChange={(e) => {
                      setToken(e.target.value)
                    }}
                  />
                </label>
                {editing.Usage === 'generation' ? (
                  <>
                    <div className="flex items-center gap-2 border-b border-border/60 pt-2 pb-3 text-sm font-medium sm:col-span-2">
                      <Hammer size={16} className="text-amber-400" />
                      模型能力
                    </div>
                    <div className="flex items-center justify-between gap-3 rounded-lg border border-border/70 bg-background/70 p-3">
                      <span className="flex items-center gap-2 text-sm">
                        <Eye size={17} className="text-sky-400" />
                        支持图片输入
                      </span>
                      <Switch
                        aria-label="支持图片输入"
                        checked={editing.Images}
                        onCheckedChange={(value) => patch({ Images: value })}
                      />
                    </div>
                    <div className="flex items-center justify-between gap-3 rounded-lg border border-border/70 bg-background/70 p-3">
                      <span className="flex items-center gap-2 text-sm">
                        <Hammer size={17} className="text-amber-400" />
                        支持工具调用
                      </span>
                      <Switch
                        aria-label="支持工具调用"
                        checked={editing.Tools}
                        onCheckedChange={(value) => patch({ Tools: value })}
                      />
                    </div>
                  </>
                ) : null}
                {(editing.Usage === 'generation' ||
                  (editing.Usage === 'intent' &&
                    editing.Protocol === 'openai')) && (
                  <>
                    <div
                      className={
                        editing.Usage === 'intent'
                          ? 'space-y-2'
                          : 'grid grid-cols-1 gap-4 sm:col-span-2 sm:grid-cols-2'
                      }
                    >
                      {(
                        [
                          'input',
                          ...(editing.Usage === 'generation' ? ['output'] : []),
                        ] as string[]
                      ).map((kind) => {
                        const input = kind === 'input'
                        const value = input
                          ? editing.ContextWindow
                          : editing.MaxOutputTokens
                        return (
                          <div key={kind} className="space-y-2">
                            <label className="block space-y-1 text-sm">
                              {input ? '输入' : '输出'}
                              <Input
                                type="number"
                                min={1}
                                max={input ? 10000000 : 1000000}
                                placeholder="使用供应商默认值"
                                value={value || ''}
                                onChange={(e) =>
                                  patch(
                                    input
                                      ? {
                                          ContextWindow: Number(e.target.value),
                                        }
                                      : {
                                          MaxOutputTokens: Number(
                                            e.target.value,
                                          ),
                                        },
                                  )
                                }
                              />
                            </label>
                            <div className="flex items-center gap-1">
                              {(input
                                ? [32, 64, 128, 256]
                                : [8, 16, 32, 64]
                              ).map((k) => (
                                <Button
                                  key={k}
                                  type="button"
                                  variant="ghost"
                                  className="h-7 px-2 text-xs text-muted-foreground"
                                  aria-pressed={value === k * 1024}
                                  onClick={() =>
                                    patch(
                                      input
                                        ? { ContextWindow: k * 1024 }
                                        : { MaxOutputTokens: k * 1024 },
                                    )
                                  }
                                >
                                  {k}K
                                </Button>
                              ))}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                    <div className="flex items-center gap-2 border-b border-border/60 pt-2 pb-3 text-sm font-medium sm:col-span-2">
                      <Brain size={16} className="text-violet-400" />
                      推理设置
                    </div>
                    <div className="flex items-center justify-between gap-3 rounded-lg border border-border/70 bg-background/70 p-3">
                      <div>
                        <span className="flex items-center gap-2 text-sm">
                          <Brain size={17} className="text-violet-400" />
                          开启思考
                        </span>
                        <p className="mt-1 text-xs text-muted-foreground">
                          为复杂问题启用推理
                        </p>
                      </div>
                      <Switch
                        aria-label="开启思考"
                        checked={editing.Thinking ?? false}
                        onCheckedChange={(value) => patch({ Thinking: value })}
                      />
                    </div>
                    <label className="block space-y-1 text-sm">
                      思考级别
                      <Select
                        disabled={!editing.Thinking}
                        value={
                          customEffort
                            ? 'custom'
                            : editing.ReasoningEffort || 'medium'
                        }
                        onChange={(e) =>
                          e.target.value === 'custom'
                            ? (setCustomEffort(true),
                              patch({ ReasoningEffort: '' }))
                            : (setCustomEffort(false),
                              patch({ ReasoningEffort: e.target.value }))
                        }
                      >
                        <option value="low">low</option>
                        <option value="medium">medium</option>
                        <option value="high">high</option>
                        <option value="max">max</option>
                        <option value="custom">自定义</option>
                      </Select>
                      {customEffort && (
                        <Input
                          aria-label="自定义思考级别"
                          placeholder="输入供应商支持的级别"
                          required={editing.Thinking}
                          maxLength={64}
                          disabled={!editing.Thinking}
                          value={editing.ReasoningEffort || ''}
                          onChange={(e) =>
                            patch({ ReasoningEffort: e.target.value })
                          }
                        />
                      )}
                    </label>
                  </>
                )}
                {editing.Usage === 'asr' && (
                  <div className="flex items-center gap-2 text-sm text-teal-500 sm:col-span-2">
                    <Mic size={16} />
                    语音识别 · 将当前会话语音转写为文字
                  </div>
                )}
                {editing.Usage === 'tts' ? (
                  <label className="block space-y-1 text-sm sm:col-span-2">
                    <span className="flex items-center gap-2">
                      <Volume2 size={16} className="text-pink-500" />
                      音色标识
                    </span>
                    {editing.Protocol === 'ark-plan' ? (
                      <>
                        <Select
                          aria-label="音色标识"
                          required
                          value={editing.Voice}
                          onChange={(e) => patch({ Voice: e.target.value })}
                        >
                          <option value="" disabled>
                            请选择官方音色
                          </option>
                          {!arkVoices.some(([id]) => id === editing.Voice) &&
                            editing.Voice && (
                              <option value={editing.Voice}>
                                当前音色 · {editing.Voice}
                              </option>
                            )}
                          {arkVoices.map(([id, name]) => (
                            <option key={id} value={id}>
                              {name} · {id}
                            </option>
                          ))}
                        </Select>
                        <span className="block text-xs text-muted-foreground">
                          官方 2.0 音色列表；是否可用以测试保存结果为准。
                        </span>
                      </>
                    ) : (
                      <Input
                        required
                        value={editing.Voice}
                        onChange={(e) => patch({ Voice: e.target.value })}
                      />
                    )}
                  </label>
                ) : null}
                <details className="rounded-lg border border-border/60 bg-background/40 p-4 sm:col-span-2">
                  <summary className="cursor-pointer text-sm font-medium">
                    高级配置 · 自定义 HTTP 请求头
                    {headers.length ? `（${headers.length}）` : ''}
                  </summary>
                  <p className="mt-3 text-xs text-muted-foreground">
                    请求头用于模型测试和实际调用，同名项覆盖默认请求头。值加密保存且不回显；已有项留空保留，删除该行则移除。
                  </p>
                  <div className="mt-3 space-y-3">
                    {headers.map((header, index) => (
                      <div key={index} className="flex items-end gap-2">
                        <label className="min-w-0 flex-1 space-y-1 text-xs">
                          名称
                          <Input
                            aria-label={`请求头名称 ${index + 1}`}
                            value={header.key}
                            maxLength={128}
                            placeholder="X-Custom-Header"
                            onChange={(e) =>
                              setHeaders((rows) =>
                                rows.map((h, i) =>
                                  i === index
                                    ? {
                                        ...h,
                                        key: e.target.value,
                                        saved: false,
                                      }
                                    : h,
                                ),
                              )
                            }
                          />
                        </label>
                        <label className="min-w-0 flex-1 space-y-1 text-xs">
                          值
                          <Input
                            aria-label={`请求头值 ${index + 1}`}
                            type="password"
                            autoComplete="new-password"
                            value={header.value}
                            maxLength={8192}
                            placeholder={
                              header.saved
                                ? '已保存，留空保留'
                                : '请输入请求头值'
                            }
                            onChange={(e) =>
                              setHeaders((rows) =>
                                rows.map((h, i) =>
                                  i === index
                                    ? { ...h, value: e.target.value }
                                    : h,
                                ),
                              )
                            }
                          />
                        </label>
                        <Button
                          type="button"
                          variant="ghost"
                          aria-label={`删除请求头 ${index + 1}`}
                          onClick={() =>
                            setHeaders((rows) =>
                              rows.filter((_, i) => i !== index),
                            )
                          }
                        >
                          删除
                        </Button>
                      </div>
                    ))}
                    <Button
                      type="button"
                      variant="outline"
                      disabled={headers.length >= 32}
                      onClick={() =>
                        setHeaders((rows) => [
                          ...rows,
                          { key: '', value: '', saved: false },
                        ])
                      }
                    >
                      添加请求头
                    </Button>
                    {headerError && (
                      <p role="alert" className="text-sm text-destructive">
                        {headerError}
                      </p>
                    )}
                  </div>
                </details>
              </fieldset>
              {change.isError ? (
                <p role="alert" className="text-sm text-destructive">
                  {change.error.message}
                </p>
              ) : null}
              <div className="sticky bottom-0 -mx-5 -mb-5 flex items-center justify-between gap-3 rounded-b-xl border-t border-border/70 bg-background/95 px-5 py-4 backdrop-blur sm:col-span-2">
                {editing.ID ? (
                  <Button
                    type="button"
                    variant="destructive"
                    disabled={change.isPending}
                    onClick={async () => {
                      if (
                        await confirmAction(
                          '删除这个模型配置？正在使用的配置无法删除。',
                        )
                      )
                        change.mutate(
                          {
                            path: `/api/v1/models/${editing.ID}`,
                            method: 'DELETE',
                            body: { Version: version },
                          },
                          { onSuccess: () => setEditing(null) },
                        )
                    }}
                  >
                    删除
                  </Button>
                ) : (
                  <span />
                )}
                {change.isPending && (
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => testController.current?.abort()}
                  >
                    取消测试
                  </Button>
                )}
                <Button type="submit" disabled={change.isPending}>
                  {change.isPending ? '测试中…' : '测试保存模型'}
                </Button>
              </div>
            </form>
          ) : null}
          <ConfirmDialog
            open={discardOpen}
            onOpenChange={setDiscardOpen}
            title="放弃未保存的修改？"
            description="当前模型配置尚未保存，放弃后将丢失本次修改。"
            cancelLabel="继续编辑"
            confirmLabel="放弃修改"
            destructive
            onConfirm={discard}
          />
        </SheetContent>
      </Sheet>
    </section>
  )
}

export function ConversationModel({
  botId,
  kind,
  targetId,
}: {
  botId: number
  kind: string
  targetId: number
}) {
  const query = useModels()
  const change = useModelChange()
  if (query.isLoading) return <p>正在加载模型…</p>
  if (query.isError)
    return (
      <FeedbackState
        variant="error"
        error={query.error}
        onRetry={() => void query.refetch()}
      />
    )
  if (!query.data) return null
  const view = query.data
  const value = view.Catalog.Selections[`${botId}:${kind}:${targetId}`] ?? ''
  return (
    <div className="mb-5 space-y-2 rounded-md border border-border p-3">
      <label className="block space-y-2 text-sm">
        生成模型
        <Select
          aria-label="生成模型"
          value={value}
          disabled={change.isPending}
          onChange={(e) =>
            change.mutate({
              path: '/api/v1/models/selection',
              method: 'PUT',
              body: {
                BotID: botId,
                Kind: kind,
                TargetID: targetId,
                ModelID: e.target.value,
                Version: view.Version,
              },
            })
          }
        >
          <option value="">
            {targetId === 0 ? '跟随全局默认' : '跟随好友／群默认模板'}
          </option>
          {view.Catalog.Profiles.filter((p) => p.Usage === 'generation').map(
            (p) => (
              <option key={p.ID} value={p.ID}>
                {p.Name}
              </option>
            ),
          )}
        </Select>
      </label>
      <p className="text-xs text-muted-foreground">
        此选择独立保存，立即用于后续回复。
      </p>
      {change.isError ? (
        <p role="alert" className="text-destructive">
          {change.error.message}
        </p>
      ) : null}
    </div>
  )
}
