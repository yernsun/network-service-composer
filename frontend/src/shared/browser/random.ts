export class RandomUnavailableError extends Error {
  constructor() {
    super('console.random_unavailable')
  }
}
export function hasSecureRandom(): boolean {
  return typeof globalThis.crypto?.getRandomValues === 'function'
}
export function secureBytes(size: number): Uint8Array {
  if (!hasSecureRandom()) throw new RandomUnavailableError()
  return globalThis.crypto.getRandomValues(new Uint8Array(size))
}
export function randomID(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID()
  const bytes = secureBytes(16)
  bytes[6] = (bytes[6]! & 15) | 64
  bytes[8] = (bytes[8]! & 63) | 128
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
function canonical(value: unknown): string {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']'
  if (value && typeof value === 'object')
    return (
      '{' +
      Object.entries(value)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => JSON.stringify(k) + ':' + canonical(v))
        .join(',') +
      '}'
    )
  return JSON.stringify(value) ?? 'null'
}
// Kept only in the lifetime of the form; never persist request bodies or secrets.
export function idempotencyKey() {
  let signature = '',
    key = ''
  return {
    forRequest(...identity: unknown[]) {
      const next = canonical(identity)
      if (!key || next !== signature) {
        key = randomID()
        signature = next
      }
      return key
    },
    reset() {
      signature = ''
      key = ''
    },
  }
}
