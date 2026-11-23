import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../../lib/api/client'
import { loadRunSnapshot, mergeRunEvents, mergeRunRows, readRunBatch, rowKey, RUN_STREAM, type RunEvent, type RunRow } from './live-decision-data'

function auditKey(key: readonly unknown[] | undefined) {
  if (!key) return false
  if (key[0] === 'inbound' || key[0] === 'inbound-message' || key[0] === 'audit') return true
  if (key[0] === 'agent' && key[1] === 'outbound') return true
  return key[0] === 'intent' && typeof key[1] === 'string' && (/^(decision|decisions|replay|overview)(-|$)/).test(key[1])
}

function clearAuditCache(client: QueryClient) {
  const filter = { predicate: ({ queryKey }: { queryKey: readonly unknown[] }) => auditKey(queryKey) }
  void client.cancelQueries(filter, { silent: true })
  client.removeQueries(filter)
  const cache = client.getMutationCache()
  for (const mutation of cache.getAll()) {
    if (auditKey(mutation.options.mutationKey)) cache.remove(mutation)
  }
}

const retryDelay = 800

export function useLiveDecisions(botId: number, selected?: { current: string }, enabled = true) {
  const client = useQueryClient()
  const [view, setView] = useState<{ rows: RunRow[]; history: RunRow[] | null; next: string } | null>(null)
  const [error, setError] = useState<unknown>()
  const [paging, setPaging] = useState(false)
  const [restart, setRestart] = useState(0)
  const controls = useRef<{ older: (before: string) => void }>(null)

  useEffect(() => {
    if (!enabled) return
    let active = true
    let epoch = 0
    let request: AbortController | undefined
    let pageRequest: AbortController | undefined
    let stream: EventSource | undefined
    let timer: number | undefined
    let retry: number | undefined
    let frame: number | undefined
    let deadline = 0
    let pending: RunEvent[] = []
    let nextCursor = ''
    let appliedCursor = ''
    let leading: { rows: RunRow[]; history: RunRow[] | null; next: string } | null = null

    function clear(clearCursor = false) {
      epoch++
      request?.abort()
      pageRequest?.abort()
      pageRequest = undefined
      stream?.close()
      stream = undefined
      window.clearTimeout(timer)
      window.clearTimeout(retry)
      if (frame !== undefined) window.cancelAnimationFrame(frame)
      frame = undefined
      deadline = 0
      if (clearCursor) appliedCursor = ''
      nextCursor = appliedCursor
      leading = null
      pending = []
      clearAuditCache(client)
      if (active) {
        setView(null)
        setPaging(false)
      }
    }

    function retryWhenVisible(clearCursor = false) {
      clear(clearCursor)
      if (active && document.visibilityState !== 'hidden') {
        retry = window.setTimeout(() => void connect(), retryDelay)
      }
    }

    function queueRender() {
      if (frame !== undefined) return
      frame = window.requestAnimationFrame(() => {
        frame = undefined
        if (!active || !leading || performance.now() >= deadline) return
        const historyKeys = new Set(leading.history?.map(row => rowKey(row)))
        const rows = mergeRunEvents(leading.rows, pending)
        const retained = rows.filter((row, index) => index < 150 || row.state === 'running' || historyKeys.has(rowKey(row)) || rowKey(row) === selected?.current)
        leading = { ...leading, rows: retained }
        pending = []
        appliedCursor = nextCursor
        setView(leading)
      })
    }

    function renew(lease: number) {
      deadline = performance.now() + lease
      window.clearTimeout(timer)
      timer = window.setTimeout(() => retryWhenVisible(), lease)
    }

    async function connect() {
      if (!active || document.visibilityState === 'hidden') return
      const session = ++epoch
      request = new AbortController()
      try {
        // Take a fresh authority check before displaying anything after reconnect.
        // 重连后先重新核准授权，再显示任何记录。
        const snapshot = await loadRunSnapshot(botId, request.signal, undefined, selected?.current)
        if (!active || epoch !== session) return
        leading = { rows: snapshot.rows, history: null, next: snapshot.next }
        const cursor = appliedCursor || snapshot.cursor
        nextCursor = cursor
        const params = new URLSearchParams({ bot_id: String(botId), cursor })
        const source = new EventSource(`${RUN_STREAM}?${params}`)
        stream = source
        renew(snapshot.lease)
        source.addEventListener('batch', (message: MessageEvent<string>) => {
          if (!active || epoch !== session) return
          try {
            if (performance.now() >= deadline) {
              retryWhenVisible()
              return
            }
            const batch = readRunBatch(message.data)
            if (!message.lastEventId) throw new Error('缺少续传位置')
            renew(batch.lease)
            nextCursor = message.lastEventId
            pending.push(...batch.events)
            setError(undefined)
            queueRender()
          } catch (cause) {
            setError(cause)
            retryWhenVisible(true)
          }
        })
        source.addEventListener('reset', () => {
          if (epoch === session) retryWhenVisible(true)
        })
        source.onerror = () => {
          if (epoch === session) retryWhenVisible()
        }
      } catch (cause) {
        if (!active || epoch !== session) return
        setError(cause)
        if (cause instanceof ApiError && (cause.status === 401 || cause.status === 403 || cause.status === 404)) clear(true)
        else retryWhenVisible()
      }
    }

    function onVisibility() {
      clear()
      if (document.visibilityState !== 'hidden') void connect()
    }
    function onOffline() { clear() }

    controls.current = {
      older(before) {
        if (!leading || pageRequest || !before) return
        const session = epoch
        pageRequest = new AbortController()
        setPaging(true)
        void loadRunSnapshot(botId, pageRequest.signal, before).then(page => {
          if (!active || epoch !== session || !leading || performance.now() >= deadline) return
          const rows = mergeRunRows(leading.rows, page.rows)
          leading = { rows, history: page.rows, next: page.next }
          queueRender()
        }).catch(cause => {
          if (!active || epoch !== session) return
          setError(cause)
          retryWhenVisible()
        }).finally(() => {
          if (active && epoch === session) {
            pageRequest = undefined
            setPaging(false)
          }
        })
      },
    }
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('offline', onOffline)
    window.addEventListener('online', onVisibility)
    void connect()
    return () => {
      active = false
      controls.current = null
      clear()
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('offline', onOffline)
      window.removeEventListener('online', onVisibility)
    }
  }, [botId, client, enabled, restart, selected])

  return {
    view, error, paging,
    reset: () => { setView(null); setError(undefined); setRestart(value => value + 1) },
    older: () => { if (view?.next) controls.current?.older(view.next) },
  }
}
