import { afterEach, describe, expect, it, vi } from 'vitest'
import { webcrypto } from 'node:crypto'
import {
  idempotencyKey,
  randomID,
  RandomUnavailableError,
  secureBytes,
} from '../../src/shared/browser/random'
afterEach(() => vi.unstubAllGlobals())
describe('HTTP-compatible secure identifiers', () => {
  it('uses native UUID when available', () => {
    const uuid = vi.fn(() => 'native')
    vi.stubGlobal('crypto', { randomUUID: uuid })
    expect(randomID()).toBe('native')
    expect(uuid).toHaveBeenCalledOnce()
  })
  it('uses secure random bytes without randomUUID', () => {
    vi.stubGlobal('crypto', { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) })
    const ids = Array.from({ length: 1000 }, () => randomID())
    expect(new Set(ids).size).toBe(1000)
    for (const id of ids)
      expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    expect(secureBytes(32)).toHaveLength(32)
  })
  it('keeps forms readable without a crypto implementation', () => {
    vi.stubGlobal('crypto', undefined)
    const key = idempotencyKey()
    expect(() => key.forRequest({})).toThrow(RandomUnavailableError)
    expect(() => secureBytes(32)).toThrow(RandomUnavailableError)
  })
  it('retains retry identity but separates changed bodies, actions and successful operations', () => {
    vi.stubGlobal('crypto', webcrypto)
    const keys = idempotencyKey()
    const first = keys.forRequest('post', '/services', { a: 1, b: 2 })
    expect(keys.forRequest('post', '/services', { b: 2, a: 1 })).toBe(first)
    expect(keys.forRequest('post', '/services', { a: 2 })).not.toBe(first)
    const changed = keys.forRequest('post', '/services', { a: 2 })
    expect(keys.forRequest('delete', '/services', { a: 2 })).not.toBe(changed)
    const last = keys.forRequest('delete', '/services', { a: 2 })
    keys.reset()
    expect(keys.forRequest('delete', '/services', { a: 2 })).not.toBe(last)
  })
})
