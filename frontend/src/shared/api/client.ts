import { scheduleRead } from './read-queue'
import { RandomUnavailableError } from '../browser/random'
import createClient from 'openapi-fetch'
import { ref } from 'vue'
import type { paths, components } from './schema'
import { i18n, setLocale } from '../i18n'
import { transport } from '@workspace-transport'
import { invalidateInventoryWrite } from '../../data/inventory-cache'
import { resourcesChanged } from '../../data/resource-changes'
export type Session = components['schemas']['BrowserSession']
export type Row = Record<string, unknown>
export const session = ref<Session>({ authenticated: false, locale: 'en', supported_languages: [] })
export const restored = ref(false)
export class ApiRequestError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly params: Record<string, unknown> = {},
    readonly fields: { pointer: string; code: string; params?: Record<string, unknown> }[] = [],
    readonly retryAfter: number | null = null,
  ) {
    super(code)
  }
}
export const client = createClient<paths>({ baseUrl: '/api/v1', credentials: 'same-origin', fetch: transport })
client.use({
  onRequest({ request }) {
    // Preserve browser negotiation on the first session request.
    if (restored.value) request.headers.set('Accept-Language', session.value.locale)
    if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method)) {
      request.headers.set('Content-Type', 'application/json')
      if (session.value.csrf_token) request.headers.set('X-CSRF-Token', session.value.csrf_token)
    }
  },
  onResponse({ response }) {
    if (response.status === 401 && !response.url.endsWith('/auth/login')) {
      session.value = {
        authenticated: false,
        locale: session.value.locale,
        supported_languages: session.value.supported_languages,
      }
      window.dispatchEvent(new Event('nsc:unauthorized'))
    }
  },
})
export type RequestOptions = {
  body?: unknown
  params?: { path?: Record<string, string>; query?: Record<string, unknown> }
  headers?: Record<string, string>
  signal?: AbortSignal
  cache?: RequestCache
}
// One dynamic boundary for the metadata-driven resource editor. The client and all
// exported domain DTOs are generated from the same checked OpenAPI contract.
export async function request(
  method: 'get' | 'post' | 'put' | 'patch' | 'delete',
  path: keyof paths,
  options: RequestOptions = {},
): Promise<{
  data: unknown
  response: Response
  pagination?: { next_cursor: string; limit: number }
  observation?: { complete: boolean; observed_at: string; node_id?: string; engine_id?: string; node_name?: string }
  coverage?: { state: string; retained_from: string; sources: { id: string; node_id?: string; state: string; last_error?: string }[] }
}> {
  try {
    const selfKeyMutation = ['/users/{userID}/api-keys', '/users/{userID}/api-keys/{keyID}'].includes(path) && options.params?.path?.userID === session.value.user_id
    if (session.value.role === 'viewer' && method !== 'get' && !selfKeyMutation && !['/locale', '/auth/logout', '/auth/login', '/me/password'].includes(path)) throw new ApiRequestError(403, 'auth.read_only')
    const execute = async () => {
      let result = await client.request(method as 'get', path as '/gateway-pools', options as never)
    // A throttled read has no mutation outcome to reconcile. Respect the server's
    // delay for bounded retries; submissions keep their explicit error behavior.
    for (let retry = 0; method === 'get' && result.response.status === 429 && retry < 2; retry++) {
      const delay = Math.min(60, Math.max(1, Number(result.response.headers.get('Retry-After')) || 1)) * 1000
      await new Promise<void>((resolve, reject) => {
        const signal = options.signal
        const cancel = () => { clearTimeout(timer); reject(new DOMException('Cancelled', 'AbortError')) }
        const timer = setTimeout(() => { signal?.removeEventListener('abort', cancel); resolve() }, delay)
        if (signal?.aborted) cancel()
        else signal?.addEventListener('abort', cancel, { once: true })
      })
      result = await client.request(method as 'get', path as '/gateway-pools', options as never)
    }
      return result
    }
    const result = await (method === 'get' ? scheduleRead(execute, options.signal) : execute())
    if (!result.response.ok) {
      const problem = result.error as components['schemas']['Problem'] | undefined
      throw new ApiRequestError(
        result.response.status,
        problem?.code ?? 'internal.operation_failed',
        problem?.params ?? {},
        problem?.errors ?? [],
        Number(result.response.headers.get('Retry-After')) || null,
      )
    }
    const envelope = result.data as
      { complete?: boolean; observed_at?: string; node_id?: string; engine_id?: string; node_name?: string; data?: unknown; pagination?: { next_cursor: string; limit: number }; coverage?: { state: string; retained_from: string; sources: { id: string; node_id?: string; state: string; last_error?: string }[] } } | undefined
    if (method !== 'get' && invalidateInventoryWrite(method, path, options.params?.path)) {
      const value = asRow(envelope?.data)
      const operation = asRow(value.operation || value)
      const operationID = typeof operation.id === 'string' && typeof operation.type === 'string' && typeof operation.status === 'string' ? operation.id : undefined
      resourcesChanged({ reason: 'mutation', method, path, params: options.params?.path || {}, ...(operationID ? { operationID } : {}) })
    }
    return {
      data: envelope?.data,
      response: result.response,
      ...(envelope?.pagination ? { pagination: envelope.pagination } : {}),
      ...(typeof envelope?.complete === "boolean" ? { observation: { complete: envelope.complete, observed_at: envelope.observed_at || "", ...(envelope.node_id ? { node_id: envelope.node_id } : {}), ...(envelope.engine_id ? { engine_id: envelope.engine_id } : {}), ...(envelope.node_name ? { node_name: envelope.node_name } : {}) } } : {}),
      ...(envelope?.coverage ? { coverage: envelope.coverage } : {}),
    }
  } catch (error) {
    if (
      error instanceof ApiRequestError ||
      (error instanceof DOMException && error.name === 'AbortError')
    )
      throw error
    throw new ApiRequestError(0, 'network')
  }
}
export async function restoreSession() {
  const result = await request('get', '/auth/session')
  session.value = result.data as Session
  setLocale(session.value.locale)
  restored.value = true
  return session.value
}
export async function signIn(username: string, password: string) {
  const result = await request('post', '/auth/login', { body: { username, password } })
  session.value = result.data as Session
  setLocale(session.value.locale)
}
export async function changeLocale(lang: string) {
  await request('post', '/locale', { body: { lang } })
  session.value = { ...session.value, locale: lang }
  setLocale(lang)
}
export function errorText(error: unknown): string {
  if (error instanceof RandomUnavailableError) return i18n.global.t(error.message)
  if (!(error instanceof ApiRequestError)) return i18n.global.t('ui.error.generic')
  if (error.status === 0) return i18n.global.t('console.network')
  if (error.status === 412) return i18n.global.t('console.conflict')
  if (error.status === 401) return i18n.global.t('console.expired')
  const key = 'problem.' + error.code
  return i18n.global.te(key) ? i18n.global.t(key, error.params) : i18n.global.t('ui.error.generic')
}
export function asRows(data: unknown): Row[] {
  return Array.isArray(data) ? data : []
}
export function asRow(data: unknown): Row {
  return data && typeof data === 'object' && !Array.isArray(data) ? (data as Row) : {}
}
export function valueAt(row: Row, key: string): unknown {
  return key.split('.').reduce<unknown>((value, part) => asRow(value)[part], row)
}
export const terminal = (status: unknown) =>
  ['SUCCEEDED', 'FAILED', 'CANCELLED', 'ROLLED_BACK', 'ROLLBACK_FAILED'].includes(String(status))
