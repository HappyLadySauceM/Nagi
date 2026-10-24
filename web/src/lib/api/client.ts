export type ApiErrorKind =
  | 'network'
  | 'timeout'
  | 'cancelled'
  | 'unauthorized'
  | 'not-found'
  | 'conflict'
  | 'invalid'
  | 'unavailable'
  | 'server'
  | 'contract'

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

/** Distinguishes an unregistered route from a missing resource. */
export type ApiErrorDetail = 'unregistered' | 'missing'

interface ApiErrorInit {
  kind: ApiErrorKind
  message: string
  endpoint: string
  status?: number
  code?: number
  /** Machine-readable hint, e.g. `unregistered` or a failing field name. */
  detail?: string
}

export class ApiError extends Error {
  readonly kind: ApiErrorKind
  readonly endpoint: string
  readonly status?: number
  readonly code?: number
  readonly detail?: string

  constructor(init: ApiErrorInit) {
    super(init.message)
    this.name = 'ApiError'
    this.kind = init.kind
    this.endpoint = init.endpoint
    this.status = init.status
    this.code = init.code
    this.detail = init.detail
  }
}

export interface RequestOptions {
  method?: HttpMethod
  /** Serialised as JSON; `undefined` sends no body. */
  body?: unknown
  timeoutMs?: number
  signal?: AbortSignal
}

interface ParsedEnvelope {
  code: number
  message: string
  data?: unknown
}

const DEFAULT_TIMEOUT_MS = 15_000

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseEnvelope(raw: string): ParsedEnvelope | undefined {
  if (raw.trim() === '') {
    return undefined
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch (error) {
    console.error(error)
    return undefined
  }
  if (!isRecord(parsed) || typeof parsed.code !== 'number' || typeof parsed.message !== 'string') {
    return undefined
  }
  return { code: parsed.code, message: parsed.message, data: parsed.data }
}

function buildTimeoutSignal(timeoutMs: number, external?: AbortSignal): AbortSignal {
  const timeoutSignal = AbortSignal.timeout(timeoutMs)
  if (!external) {
    return timeoutSignal
  }
  if (typeof AbortSignal.any === 'function') {
    return AbortSignal.any([external, timeoutSignal])
  }
  return timeoutSignal
}

function translateTransportError(error: unknown, endpoint: string): ApiError {
  const name = error instanceof Error ? error.name : ''
  if (name === 'TimeoutError') {
    return new ApiError({
      kind: 'timeout',
      endpoint,
      message: '请求超时，后端未在预期时间内响应',
    })
  }
  // TanStack Query aborts the signal when a view unmounts, and an aborted
  // request is not a timeout.
  // TanStack Query 会在视图卸载时中止请求，被中止不等于超时。
  if (name === 'AbortError') {
    return new ApiError({
      kind: 'cancelled',
      endpoint,
      message: '请求已取消',
    })
  }
  console.error(error)
  return new ApiError({
    kind: 'network',
    endpoint,
    message: '无法连接后端管理 API',
  })
}

function buildHttpError(
  status: number,
  endpoint: string,
  envelope: ParsedEnvelope | undefined,
): ApiError {
  const backendMessage = envelope?.message

  if (status === 400) {
    return new ApiError({
      kind: 'invalid',
      endpoint,
      status,
      message: backendMessage ?? '提交内容未通过后端校验',
    })
  }
  if (status === 401) {
    return new ApiError({
      kind: 'unauthorized',
      endpoint,
      status,
      message: backendMessage ?? '需要管理员令牌',
    })
  }
  if (status === 404) {
    // Go's ServeMux answers unregistered routes with plain text, and management
    // routes are only registered when the database is configured.
    if (envelope) {
      return new ApiError({
        kind: 'not-found',
        endpoint,
        status,
        message: backendMessage ?? '资源不存在',
        detail: 'missing',
      })
    }
    return new ApiError({
      kind: 'not-found',
      endpoint,
      status,
      message: '该接口未注册',
      detail: 'unregistered',
    })
  }
  if (status === 409) {
    return new ApiError({
      kind: 'conflict',
      endpoint,
      status,
      message: backendMessage ?? '当前状态不允许该操作',
    })
  }
  if (status === 503) {
    return new ApiError({
      kind: 'unavailable',
      endpoint,
      status,
      message: backendMessage ?? '依赖服务未就绪',
    })
  }
  return new ApiError({
    kind: 'server',
    endpoint,
    status,
    message: backendMessage ?? `服务端返回 ${status}`,
  })
}

/**
 * Performs one request against the same-origin management API and unwraps the
 * `{code,message,data}` envelope. Relative paths only: the browser never talks
 * to a third-party origin and never holds the admin token.
 */
export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const signal = buildTimeoutSignal(timeoutMs, options.signal)
  const hasBody = options.body !== undefined

  let response: Response
  try {
    response = await fetch(path, {
      method: options.method ?? 'GET',
      headers: hasBody
        ? { Accept: 'application/json', 'Content-Type': 'application/json' }
        : { Accept: 'application/json' },
      body: hasBody ? JSON.stringify(options.body) : undefined,
      credentials: 'same-origin',
      cache: 'no-store',
      signal,
    })
  } catch (error) {
    throw translateTransportError(error, path)
  }

  let raw = ''
  try {
    raw = await response.text()
  } catch (error) {
    throw translateTransportError(error, path)
  }

  const envelope = parseEnvelope(raw)

  if (!response.ok) {
    throw buildHttpError(response.status, path, envelope)
  }

  if (!envelope) {
    throw new ApiError({
      kind: 'contract',
      endpoint: path,
      status: response.status,
      message: '响应不是预期的 JSON 信封',
    })
  }
  if (envelope.code !== 200) {
    throw buildHttpError(envelope.code, path, envelope)
  }
  return envelope.data as T
}