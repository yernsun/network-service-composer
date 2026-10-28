import { translate } from '../i18n'

const operationState = status => ({ QUEUED: 'running', RUNNING: 'running', SUCCEEDED: 'success', FAILED: 'failed', ROLLED_BACK: 'failed', ROLLBACK_FAILED: 'failed' })[status] || 'unknown'

const actionKeys = { SCALE: 'serviceOps.scale', REMOVE_RUNTIME: 'serviceOps.remove', DELETE_SERVICE: 'operationActions.deleteService', RESTORE_SERVICE: 'operationActions.restoreService', RESTORE_APPLICATION: 'operationActions.restoreApplication', CREATE_SERVICES: 'operationActions.createServices', UPDATE_SERVICES: 'operationActions.updateServices', UPGRADE: 'operationActions.upgrade' }
export function operationAction(operation) {
  const action = operation.business_action || operation.type
  return actionKeys[action] ? translate(actionKeys[action]) : operation.type
}

// Keep the original view model identical for initial inventory and live reads.
export function operationView(operation, scope = {}, version = null) {
  const state = operationState(operation.status)
  return { ...operation, app: operation.application_id || '', project: scope.project_id || '', env: scope.id || '',
    runtime: operation.runtime_id, version, state, targets: operation.service_id ? [operation.service_id] : [],
    results: operation.service_id ? { [operation.service_id]: state } : {},
    snapshot: { revision_id: operation.revision_id || null },
    attempts: [{ number: operation.attempt, state, time: operation.updated_at, logs: operation.log ? [operation.log] : [] }],
    action: operationAction(operation), time: operation.created_at }
}
