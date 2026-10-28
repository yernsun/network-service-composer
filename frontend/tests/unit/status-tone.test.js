import { expect, it } from 'vitest'
import { knownStatusTone, statusTone } from '../../src/data/status-tone'

it('distinguishes active resources, pending operations, failures and inactive resources', () => {
  expect(statusTone('running')).toBe('green')
  expect(statusTone('running', 'operation')).toBe('blue')
  expect(statusTone('starting')).toBe('blue')
  expect(statusTone('FAILED')).toBe('red')
  expect(statusTone('PARTIAL')).toBe('amber')
  expect(statusTone('AUTHORIZING')).toBe('blue')
  expect(statusTone('EXPIRED')).toBe('red')
  expect(statusTone('CONFIRMED')).toBe('green')
  expect(statusTone('MISMATCH')).toBe('amber')
  expect(statusTone('APPLY_FAILED')).toBe('red')
  expect(statusTone('SHUTDOWN')).toBe('')
})

it('corrects legacy badges only when their labels are recognizable statuses', () => {
  expect(knownStatusTone('failed', 'amber')).toBe('red')
  expect(knownStatusTone('SHUTDOWN', 'green')).toBe('')
  expect(knownStatusTone('running', 'amber')).toBe('blue')
  expect(knownStatusTone('4 / 4 running', 'green')).toBe('green')
})
