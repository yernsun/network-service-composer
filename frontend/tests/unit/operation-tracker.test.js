import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { operationTracker } from '../../src/data/operation-tracker'

let abort
beforeEach(() => { vi.useFakeTimers(); abort = new AbortController() })
afterEach(() => { abort.abort(); vi.useRealTimers() })

it('follows beyond one minute, deduplicates and reads the terminal result', async () => {
  const read = vi.fn().mockResolvedValue({ status: 'RUNNING' })
  const complete = vi.fn()
  const tracker = operationTracker({ read, complete, error: vi.fn(), signal: abort.signal })
  const first = tracker.track('long-operation')
  expect(tracker.track('long-operation')).toBe(first)
  await vi.advanceTimersByTimeAsync(75000)
  expect(read.mock.calls.length).toBeGreaterThan(60)
  expect(complete).not.toHaveBeenCalled()
  read.mockResolvedValue({ id: 'long-operation', status: 'SUCCEEDED' })
  await vi.advanceTimersByTimeAsync(1000)
  await first
  expect(complete).toHaveBeenCalledExactlyOnceWith({ id: 'long-operation', status: 'SUCCEEDED' })
})

it('resumes server inventory after refresh and retains unknown outcomes through temporary read failures', async () => {
  const read = vi.fn().mockRejectedValueOnce({ status: 503 }).mockResolvedValueOnce({ status: 'UNKNOWN' }).mockResolvedValue({ status: 'FAILED' })
  const complete = vi.fn(), error = vi.fn()
  const tracker = operationTracker({ read, complete, error, signal: abort.signal })
  tracker.resume([{ id: 'done', status: 'SUCCEEDED' }, { id: 'pending', status: 'RUNNING' }])
  await vi.advanceTimersByTimeAsync(5000)
  expect(complete).not.toHaveBeenCalled()
  expect(error).toHaveBeenCalledTimes(1)
  await vi.advanceTimersByTimeAsync(5000)
  expect(read.mock.calls.every(([id]) => id === 'pending')).toBe(true)
  expect(complete).toHaveBeenCalledExactlyOnceWith({ status: 'FAILED' })
})

it.each([401, 403, 404])('stops observation after access is lost (%s)', async status => {
  const read = vi.fn().mockRejectedValue({ status }), complete = vi.fn()
  const tracker = operationTracker({ read, complete, error: vi.fn(), signal: abort.signal })
  await tracker.track('operation')
  await vi.advanceTimersByTimeAsync(90000)
  expect(read).toHaveBeenCalledTimes(1)
  expect(complete).not.toHaveBeenCalled()
})

it('cancels pending waits and performs no reads after the workspace is disposed', async () => {
  const read = vi.fn().mockResolvedValue({ status: 'RUNNING' })
  const tracker = operationTracker({ read, complete: vi.fn(), error: vi.fn(), signal: abort.signal })
  const pending = tracker.track('operation')
  await vi.advanceTimersByTimeAsync(0)
  abort.abort()
  await pending
  await vi.advanceTimersByTimeAsync(90000)
  expect(read).toHaveBeenCalledTimes(1)
})

it('publishes intermediate evidence and terminal evidence before inventory refresh', async () => {
  const seen = []
  const read = vi.fn().mockResolvedValueOnce({ status: 'RUNNING', log: 'checking readiness' }).mockResolvedValueOnce({ status: 'UNKNOWN', log: 'node disconnected' }).mockResolvedValue({ status: 'SUCCEEDED', log: 'verified' })
  const tracker = operationTracker({ read, observe: o => seen.push(o), complete: o => seen.push({ completed: o.status }), error: vi.fn(), signal: abort.signal })
  const pending = tracker.track('operation')
  await vi.advanceTimersByTimeAsync(6000)
  await pending
  expect(seen).toEqual([{ status: 'RUNNING', log: 'checking readiness' }, { status: 'UNKNOWN', log: 'node disconnected' }, { status: 'SUCCEEDED', log: 'verified' }, { completed: 'SUCCEEDED' }])
})

it('discards an in-flight response after disposal', async () => {
  let finish
  const observe = vi.fn(), complete = vi.fn()
  const tracker = operationTracker({ read: () => new Promise(resolve => { finish = resolve }), observe, complete, error: vi.fn(), signal: abort.signal })
  const pending = tracker.track('operation')
  abort.abort()
  finish({ status: 'SUCCEEDED' })
  await pending
  expect(observe).not.toHaveBeenCalled()
  expect(complete).not.toHaveBeenCalled()
})

it('retains terminal evidence and stops polling when its follow-up refresh fails', async () => {
  const read = vi.fn().mockResolvedValue({ id: 'finished', status: 'SUCCEEDED' })
  const observe = vi.fn(), error = vi.fn()
  const failure = new Error('refresh unavailable')
  const complete = vi.fn().mockRejectedValue(failure)
  const tracker = operationTracker({ read, observe, complete, error, signal: abort.signal })
  await tracker.track('finished')
  await vi.advanceTimersByTimeAsync(30000)
  expect(read).toHaveBeenCalledOnce()
  expect(complete).toHaveBeenCalledOnce()
  expect(observe).toHaveBeenCalledWith({ id: 'finished', status: 'SUCCEEDED' })
  expect(error).toHaveBeenCalledWith(failure)
})
