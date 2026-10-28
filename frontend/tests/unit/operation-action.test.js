import { describe, expect, it, vi } from 'vitest'
import { operationAction, operationView } from '../../src/data/operation-view'
vi.mock('../../src/i18n', () => ({ translate: key => key }))

describe('operation business actions', () => {
  it.each([
    ['SCALE', 'serviceOps.scale'], ['DELETE_SERVICE', 'operationActions.deleteService'],
    ['RESTORE_SERVICE', 'operationActions.restoreService'], ['RESTORE_APPLICATION', 'operationActions.restoreApplication'],
    ['CREATE_SERVICES', 'operationActions.createServices'], ['UPDATE_SERVICES', 'operationActions.updateServices'], ['UPGRADE', 'operationActions.upgrade'],
  ])('uses %s without changing the executor type or accepted result', (action, key) => {
    const op = { id: 'op', type: 'UPGRADE', business_action: action, service_id: 'svc', status: 'SUCCEEDED' }
    const view = operationView(op)
    expect(view.action).toBe(key); expect(view.type).toBe('UPGRADE'); expect(view.state).toBe('success'); expect(view.targets).toEqual(['svc'])
    expect(op.status).toBe('SUCCEEDED')
  })
  it('uses a safe upgrade fallback for older or unsupported API metadata', () => {
    expect(operationAction({ type: 'UPGRADE', service_id: 'svc' })).toBe('operationActions.upgrade')
    expect(operationAction({ type: 'UPGRADE', business_action: 'FUTURE' })).toBe('UPGRADE')
    expect(operationAction({ type: 'REMOVE_RUNTIME' })).toBe('serviceOps.remove')
  })
})
