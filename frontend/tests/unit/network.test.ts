import { afterEach, expect, it, vi } from 'vitest'
import { transferableAbortController } from 'node:util'
import { transport } from '../../src/data/transport'
afterEach(() => vi.unstubAllGlobals())
it('preserves standalone engine identity and request boundaries in the Chi path parameter', async () => {
  const native = transferableAbortController()
  vi.stubGlobal('AbortController', native.constructor)
  vi.stubGlobal('AbortSignal', native.signal.constructor)
  const fetcher = vi.fn().mockResolvedValue(new Response())
  vi.stubGlobal('fetch', fetcher)
  const request = new Request('https://nsc.test/api/v1/runtimes/rt/cluster/nodes/engine%3Aabc/instances?history=true', { headers: { 'Accept-Language': 'ja' }, credentials: 'same-origin' })
  await transport(request)
  const actual = fetcher.mock.calls[0]![0] as Request
  expect(actual.url).toBe('https://nsc.test/api/v1/runtimes/rt/cluster/nodes/engine:abc/instances?history=true')
  expect(actual.headers.get('Accept-Language')).toBe('ja')
  expect(actual.credentials).toBe('same-origin')
  const unrelated = new Request('https://nsc.test/api/v1/services/a%3Ab%2Fc')
  await transport(unrelated)
  expect(fetcher.mock.calls[1]![0]).toBe(unrelated)
})
