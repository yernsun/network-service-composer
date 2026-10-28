import { describe, expect, it } from 'vitest'
import { confirmsCurrentPoolConfiguration, loadedDomainRevisions } from '../../src/data/gateway-application'

const digest = character => character.repeat(64)
const confirmedGateway = () => ({
  id: 'gateway-a', pool_id: 'pool-a', enabled: true, status: 'READY',
  desired_config_revision: 12, applied_config_revision: 12,
  model_digest: digest('a'), rendered_digest: digest('b'),
  evidence: {
    loading_confirmed: true, startup_state: 'CONFIRMED', configuration_state: 'CONFIRMED',
    confirmed_revision: 12, last_confirmed_revision: 12, last_confirmed_at: '2026-09-21T12:00:00Z',
    model_digest: digest('a'), rendered_digest: digest('b'), disk_digest: digest('b'),
  },
})

describe('Gateway configuration evidence', () => {
  it('projects the current domain revision only from a fully confirmed pool generation', () => {
    const gateway = confirmedGateway()
    expect(confirmsCurrentPoolConfiguration(gateway, 12)).toBe(true)
    expect(loadedDomainRevisions({ pool_id: 'pool-a', revision: 7 }, [gateway], 12)).toEqual({ 'gateway-a': 7 })
  })

  it.each([
    ['missing evidence', gateway => { delete gateway.evidence }],
    ['disabled Gateway', gateway => { gateway.enabled = false }],
    ['non-ready Gateway', gateway => { gateway.status = 'STALE' }],
    ['stale target', gateway => { gateway.desired_config_revision = 11 }],
    ['not applied', gateway => { gateway.applied_config_revision = 11 }],
    ['Agent did not confirm loading', gateway => { gateway.evidence.loading_confirmed = false }],
    ['startup risk', gateway => { gateway.evidence.startup_state = 'RISK' }],
    ['configuration mismatch', gateway => { gateway.evidence.configuration_state = 'MISMATCH' }],
    ['wrong confirmed revision', gateway => { gateway.evidence.confirmed_revision = 11 }],
    ['old last confirmation', gateway => { gateway.evidence.last_confirmed_revision = 11 }],
    ['missing confirmation time', gateway => { delete gateway.evidence.last_confirmed_at }],
    ['model mismatch', gateway => { gateway.evidence.model_digest = digest('c') }],
    ['render mismatch', gateway => { gateway.evidence.rendered_digest = digest('c') }],
    ['disk drift', gateway => { gateway.evidence.disk_digest = digest('c') }],
  ])('keeps status unknown for %s', (_name, mutate) => {
    const gateway = confirmedGateway()
    mutate(gateway)
    expect(confirmsCurrentPoolConfiguration(gateway, 12)).toBe(false)
  })

  it('rejects an invalid target and a Gateway outside the domain pool', () => {
    const gateway = confirmedGateway()
    expect(confirmsCurrentPoolConfiguration(gateway, 0)).toBe(false)
    expect(loadedDomainRevisions({ pool_id: 'pool-b', revision: 7 }, [gateway], 12)).toEqual({})
  })
})
