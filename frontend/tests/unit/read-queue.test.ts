import { expect, it } from 'vitest'
import { scheduleRead } from '../../src/shared/api/read-queue'
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(finish => { resolve = finish })
  return { promise, resolve }
}

it('cancels a queued read immediately while all network slots remain occupied', async () => {
  const blockers = Array.from({ length: 4 }, deferred)
  const jobs = blockers.map(blocker => scheduleRead(() => blocker.promise))
  const abort = new AbortController()
  let cancelledRan = false
  const cancelled = scheduleRead(async () => { cancelledRan = true }, abort.signal)
  const rejection = expect(cancelled).rejects.toMatchObject({ name: 'AbortError' })
  abort.abort()
  try {
    await rejection
    expect(cancelledRan).toBe(false)
  } finally {
    blockers.forEach(blocker => blocker.resolve())
    await Promise.all(jobs)
  }
})

it('keeps surviving reads in order and within the concurrency limit after cancellations', async () => {
  const blockers = Array.from({ length: 8 }, deferred)
  const started = Array.from({ length: 8 }, deferred)
  const order: number[] = []
  let executing = 0, maximum = 0
  const abort = new AbortController()
  const jobs = blockers.map((blocker, index) => scheduleRead(async () => {
    order.push(index); executing++; maximum = Math.max(maximum, executing)
    started[index]!.resolve()
    await blocker.promise
    executing--
  }, index === 5 ? abort.signal : undefined))
  const cancelled = expect(jobs[5]).rejects.toMatchObject({ name: 'AbortError' })
  abort.abort()
  await cancelled
  await Promise.all(started.slice(0, 4).map(job => job.promise))
  expect(order).toEqual([0, 1, 2, 3])
  for (const [released, next] of [[0, 4], [1, 6], [2, 7]]) {
    blockers[released!]!.resolve()
    await started[next!]!.promise
  }
  blockers.forEach(blocker => blocker.resolve())
  await Promise.allSettled(jobs)
  expect(order).toEqual([0, 1, 2, 3, 4, 6, 7])
  expect(maximum).toBe(4)
})

it('does not start a read whose signal was already cancelled', async () => {
  const abort = new AbortController()
  abort.abort()
  let ran = false
  await expect(scheduleRead(async () => { ran = true }, abort.signal)).rejects.toMatchObject({ name: 'AbortError' })
  expect(ran).toBe(false)
})

it('releases occupied slots after both synchronous and asynchronous failures', async () => {
  const failure = new Error('network unavailable')
  await expect(scheduleRead(() => { throw failure })).rejects.toBe(failure)
  await expect(scheduleRead(() => Promise.reject(failure))).rejects.toBe(failure)
  const values = await Promise.all(Array.from({ length: 8 }, (_, index) => scheduleRead(async () => index)))
  expect(values).toEqual([0, 1, 2, 3, 4, 5, 6, 7])
})
