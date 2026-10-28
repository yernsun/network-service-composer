import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiRequestError, changeLocale, client, errorText, request, restoreSession, session, signIn } from '../../src/shared/api/client'
import { RandomUnavailableError } from '../../src/shared/browser/random'
import { i18n, setLocale } from '../../src/shared/i18n'
import { allPages } from '../../src/data/pagination'
import { inventoryKey, queryClient } from '../../src/data/inventory-cache'
import { QueryObserver } from '@tanstack/vue-query'
import type { ResourceChange } from '../../src/data/resource-changes'
afterEach(() => {
  queryClient.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  setLocale('en')
})
beforeEach(() => {
  session.value = {
    authenticated: true,
    locale: 'en',
    role: 'administrator',
    supported_languages: ['en'],
  }
})
describe('NSC transport boundaries', () => {
  it('notifies only accepted mutations with resource identities and the accepted operation ID', async () => {
    const changes: ResourceChange[] = []
    const changed = (event: Event) => changes.push((event as CustomEvent<ResourceChange>).detail)
    window.addEventListener('nsc:resources-changed', changed)
    try {
      vi.spyOn(client, 'request')
        .mockResolvedValueOnce({ response: new Response('', { status: 412 }) } as never)
        .mockResolvedValueOnce({ response: new Response('', { status: 200 }), data: { data: {} } } as never)
        .mockResolvedValueOnce({ response: new Response('', { status: 202 }), data: { data: { operation: { id: 'operation', type: 'INSTALL', status: 'QUEUED' }, configuration: 'confidential response' } } } as never)
      await expect(request('put', '/environments/{id}/variables', { params: { path: { id: 'e' } }, body: { secret: 'confidential input' } })).rejects.toMatchObject({ status: 412 })
      await request('post', '/environments/{id}/preflight', { params: { path: { id: 'e' } } })
      await request('post', '/applications', { body: { compose_yaml: 'confidential input' } })
      expect(changes).toEqual([{ reason: 'mutation', method: 'post', path: '/applications', params: {}, operationID: 'operation' }])
      expect(JSON.stringify(changes)).not.toContain('confidential')
    } finally { window.removeEventListener('nsc:resources-changed', changed) }
  })
  it('updates an active component query through the shared client after a service write', async () => {
    let server = 'before'
    const observer = new QueryObserver(queryClient, { queryKey: ['service-bindings', 's'], queryFn: async () => server })
    const stop = observer.subscribe(() => {})
    try {
      await vi.waitFor(() => expect(observer.getCurrentResult().data).toBe('before'))
      server = 'after'
      vi.spyOn(client, 'request').mockResolvedValue({ response: new Response('', { status: 202 }), data: { data: {} } } as never)
      await request('post', '/services/{id}/scale', { params: { path: { id: 's' } }, body: { replicas: 2 } })
      await vi.waitFor(() => expect(observer.getCurrentResult().data).toBe('after'))
    } finally { stop() }
  })
  it('refreshes certificate assets and their directory cache after accepted domain publication', async () => {
    let status = 'PENDING'
    const key = inventoryKey('/certificates')
    queryClient.setQueryData(key, 'PENDING')
    const observer = new QueryObserver(queryClient, { queryKey: ['certificate-assets'], queryFn: async () => status })
    const stop = observer.subscribe(() => {})
    try {
      await vi.waitFor(() => expect(observer.getCurrentResult().data).toBe('PENDING'))
      status = 'ACTIVE'
      vi.spyOn(client, 'request').mockResolvedValue({ response: new Response('', { status: 202 }), data: { data: {} } } as never)
      await request('post', '/domain-drafts/{id}/publish', { params: { path: { id: 'domain-draft' } } })
      await vi.waitFor(() => expect(observer.getCurrentResult().data).toBe('ACTIVE'))
      expect(await queryClient.fetchQuery({ queryKey: key, queryFn: async () => status, staleTime: Infinity })).toBe('ACTIVE')
    } finally { stop() }
  })
  it('invalidates only accepted writes and keeps unrelated variable caches reusable', async () => {
    const target = inventoryKey('/environments/{id}/variables', { id: 'target' })
    const unrelated = inventoryKey('/environments/{id}/variables', { id: 'unrelated' })
    queryClient.setQueryData(target, 'before')
    queryClient.setQueryData(unrelated, 'preserved')
    const mock = vi.spyOn(client, 'request')
      .mockResolvedValueOnce({ response: new Response('', { status: 412 }) } as never)
      .mockResolvedValue({ data: { data: {} }, response: new Response() } as never)
    const read = vi.fn(async () => 'saved')
    const cached = (key: ReturnType<typeof inventoryKey>) => queryClient.fetchQuery({ queryKey: key, queryFn: read, staleTime: Infinity })
    await expect(request('put', '/environments/{id}/variables', { params: { path: { id: 'target' } }, body: { values: [] } })).rejects.toMatchObject({ status: 412 })
    expect(await cached(target)).toBe('before')
    await request('get', '/environments/{id}/variables', { params: { path: { id: 'target' } } })
    expect(await cached(target)).toBe('before')
    await request('put', '/environments/{id}/variables', { params: { path: { id: 'target' } }, body: { values: [] } })
    expect(await cached(target)).toBe('saved')
    expect(await cached(unrelated)).toBe('preserved')
    expect(read).toHaveBeenCalledOnce()
    expect(mock).toHaveBeenCalledTimes(3)
  })
  it('preserves cancellation and prevents read-only writes before network access', async () => {
    const mock = vi
      .spyOn(client, 'request')
      .mockRejectedValue(new DOMException('cancel', 'AbortError'))
    await expect(request('get', '/services')).rejects.toMatchObject({ name: 'AbortError' })
    session.value.role = 'viewer'
    mock.mockClear()
    await expect(request('post', '/catalog/sources')).rejects.toMatchObject({
      status: 403,
      code: 'auth.read_only',
    })
    expect(mock).not.toHaveBeenCalled()
    mock.mockResolvedValue({ data: { data: {} }, response: new Response() } as never)
    await request('post', '/locale', { body: { lang: 'es' } })
    expect(mock).toHaveBeenCalledOnce()
  })
  it('uses safe defaults for missing error envelopes and preserves 204', async () => {
    const mock = vi
      .spyOn(client, 'request')
      .mockResolvedValueOnce({ response: new Response('', { status: 500 }) } as never)
      .mockResolvedValueOnce({ response: new Response(null, { status: 204 }) } as never)
    await expect(request('post', '/catalog/sources')).rejects.toMatchObject({
      code: 'internal.operation_failed',
      params: {},
      fields: [],
      retryAfter: null,
    })
    expect((await request('delete', '/routes/{id}')).data).toBeUndefined()
    expect(mock).toHaveBeenCalledTimes(2)
  })
  it('restores and updates session language only after accepted responses', async () => {
    vi.spyOn(client, 'request')
      .mockResolvedValueOnce({
        data: { data: { authenticated: true, locale: 'ru', supported_languages: ['ru', 'es'] } },
        response: new Response(),
      } as never)
      .mockResolvedValueOnce({
        data: { data: { authenticated: true, locale: 'es', supported_languages: ['ru', 'es'] } },
        response: new Response(),
      } as never)
      .mockResolvedValueOnce({
        data: { data: { locale: 'en' } },
        response: new Response(),
      } as never)
    await restoreSession()
    expect(document.documentElement.lang).toBe('ru')
    await signIn('test', 'test-password')
    expect(session.value.locale).toBe('es')
    await changeLocale('en')
    expect(document.documentElement.lang).toBe('en')
  })
  it.each([
    [0, 'console.network'],
    [412, 'console.conflict'],
    [401, 'console.expired'],
    [403, 'problem.auth.read_only'],
  ])('maps status %s without exposing diagnostics', (status, key) => {
    expect(errorText(new ApiRequestError(status as number, 'auth.read_only'))).toBe(
      i18n.global.t(String(key)),
    )
  })
  it('maps generic, unknown and secure-random errors safely', () => {
    expect(errorText(new Error('private-token'))).toBe(i18n.global.t('ui.error.generic'))
    expect(errorText(new ApiRequestError(500, 'unknown'))).toBe(i18n.global.t('ui.error.generic'))
    expect(errorText(new RandomUnavailableError())).toBe(
      i18n.global.t('console.random_unavailable'),
    )
  })
  it('rejects repeated pagination cursors and passes cancellation through', async () => {
    const ctrl = new AbortController()
    const mock = vi
      .spyOn(client, 'request')
      .mockResolvedValue({
        data: { data: [{ id: 'x' }], pagination: { next_cursor: 'same' } },
        response: new Response(),
      } as never)
    await expect(allPages('/services', {}, ctrl.signal)).rejects.toThrow(
      'Repeated pagination cursor',
    )
    expect(mock).toHaveBeenCalledTimes(2)
    expect(mock.mock.calls[0]?.[2]).toMatchObject({ signal: ctrl.signal })
  })
})

it('retries only throttled reads with the server delay and keeps writes explicit', async () => {
  vi.useFakeTimers()
  try {
    const mock = vi.spyOn(client, 'request').mockResolvedValueOnce({ response: new Response('', { status: 429, headers: { 'Retry-After': '1' } }) } as never).mockResolvedValueOnce({ data: { data: [{ id: 'read' }] }, response: new Response() } as never)
    const read = request('get', '/projects')
    await vi.advanceTimersByTimeAsync(999)
    expect(mock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect((await read).data).toEqual([{ id: 'read' }])
    mock.mockResolvedValue({ response: new Response('', { status: 429 }) } as never)
    await expect(request('post', '/projects', { body: { name: 'Keep input', slug: 'keep' } })).rejects.toMatchObject({ status: 429 })
    expect(mock).toHaveBeenCalledTimes(3)
  } finally { vi.useRealTimers() }
})
