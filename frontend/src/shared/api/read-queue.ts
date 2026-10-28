// Bound collection fan-out so pagination and related-resource reads do not flood
// the Controller. A task retains its slot while honoring Retry-After.
let active = 0
const waiting: (() => void)[] = []
export function scheduleRead<T>(work: () => Promise<T>, signal?: AbortSignal): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const cancel = () => {
      const index = waiting.indexOf(start)
      if (index < 0) return
      waiting.splice(index, 1)
      signal?.removeEventListener('abort', cancel)
      reject(new DOMException('Cancelled', 'AbortError'))
    }
    const start = () => {
      signal?.removeEventListener('abort', cancel)
      if (signal?.aborted) { reject(new DOMException('Cancelled', 'AbortError')); return }
      active++
      void Promise.resolve().then(work).then(resolve, reject).finally(() => { active--; waiting.shift()?.() })
    }
    if (signal?.aborted) { reject(new DOMException('Cancelled', 'AbortError')); return }
    if (active < 4) start()
    else { waiting.push(start); signal?.addEventListener('abort', cancel, { once: true }) }
  })
}
