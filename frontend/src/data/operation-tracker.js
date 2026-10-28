import { terminal } from '../shared/api/client'

// Observation failures and unknown states never become execution success/failure.
export function operationTracker({ read, observe = () => {}, complete, error, signal }) {
  const active = new Map()
  async function delay(ms) {
    if (signal.aborted) return
    await new Promise(resolve => {
      const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve() }
      const timer = setTimeout(finish, ms)
      signal.addEventListener('abort', finish, { once: true })
    })
  }
  async function follow(id) {
    let reported = false
    while (!signal.aborted) {
      try {
        const operation = await read(id, signal)
        if (signal.aborted) return
        observe(operation)
        if (terminal(operation.status)) {
          // A failed follow-up read does not change a known execution outcome.
          try { await complete(operation) } catch (failure) { if (!signal.aborted) error(failure) }
          return
        }
        reported = false
        await delay(['QUEUED', 'RUNNING'].includes(operation.status) ? 1000 : 5000)
      } catch (failure) {
        if (signal.aborted) return
        if (!reported) { error(failure); reported = true }
        if ([401, 403, 404].includes(failure.status)) return
        await delay(5000)
      }
    }
  }
  function track(id) {
    if (!id || signal.aborted || active.has(id)) return active.get(id)
    const task = follow(id).finally(() => active.delete(id))
    active.set(id, task)
    return task
  }
  function resume(operations) {
    for (const operation of operations || []) if (!terminal(operation.status)) void track(operation.id)
  }
  return { track, resume }
}
