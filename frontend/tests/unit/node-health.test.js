import { afterEach, describe, expect, it, vi } from 'vitest'
import { nodeHealth, nodeMetrics, nodeMetric } from '../../src/data/node-health'
vi.mock('../../src/i18n', () => ({translate: key => key}))
afterEach(() => vi.useRealTimers())
const at = '2026-09-16T10:00:00Z'
function fixture() {
 vi.useFakeTimers(); vi.setSystemTime(new Date(at))
 return {host:{at,cpu_percent:0,memory_used:0,memory_total:4*1073741824,disks:[{mount:'/data/<name>',used:1073741824,total:4*1073741824,read_only:true}]},controller:{at,controller:'healthy',collector:'degraded',gateway:'ready',role:'follower',instance_id:'controller-two'}}
}
describe('node health evidence', () => {
 it('preserves measured zero and independent component health', () => {
  const node=fixture()
  expect(nodeMetrics(node)).toEqual({cpu:0,used:0,total:4})
  expect(nodeMetric(node,'cpu')).toBe('0%')
  expect(nodeMetric(node,'memory')).toBe('0 / 4 GiB')
  expect(nodeHealth(node,'controller')).toContain('nodeHealth.follower')
  expect(nodeHealth(node,'collector')).toBe('nodeHealth.degraded')
  expect(nodeHealth(node,'gateway')).toBe('nodeHealth.ready')
  expect(nodeHealth(node,'disk')).toBe('/data/<name>: 1.00 / 4.00 GiB · nodeHealth.readOnly')
 })
 it('never replaces missing metrics with capacity or container statistics', () => {
  fixture()
  const node={memory_bytes:8589934592,metric:{cpu_percent:20,memory_usage:200}}
  expect(nodeMetrics(node)).toEqual({cpu:null,used:null,total:null})
  for(const kind of ['controller','collector','gateway','disk']) expect(nodeHealth(node,kind)).toBe('nodeHealth.unavailable')
 })
 it('expires independent timestamps and rejects future samples', () => {
  const node=fixture();vi.advanceTimersByTime(31000)
  expect(nodeMetrics(node).cpu).toBeNull()
  expect(nodeMetric(node,'cpu')).toBe('nodeHealth.unavailable')
  expect(nodeHealth(node,'controller')).toBe('nodeHealth.unavailable')
  expect(nodeHealth(node,'disk')).toBe('nodeHealth.unavailable')
  node.host.at='2026-09-16T10:00:40Z'
  expect(nodeMetrics(node).cpu).toBeNull()
  expect(nodeMetric(node,'cpu')).toBe('nodeHealth.unavailable')
  node.host.at=new Date().toISOString();node.host.stale=true
  expect(nodeMetrics(node).cpu).toBeNull()
  expect(nodeMetric(node,'cpu')).toBe('nodeHealth.unavailable')
 })
})
