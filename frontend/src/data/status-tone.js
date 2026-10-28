const success = new Set([
  'ACTIVE', 'AVAILABLE', 'COMPLETE', 'COMPLETED', 'CONFIRMED', 'HEALTHY', 'LOADED',
  'ONLINE', 'PUBLISHED', 'READY', 'RUNNING', 'SUCCESS', 'SUCCEEDED',
])
const progress = new Set([
  'APPLYING', 'AUTHORIZING', 'CERT_PENDING', 'COLLECTING', 'CREATING', 'DEPLOYING',
  'DNS_PENDING', 'INITIALIZING', 'ISSUED', 'PENDING', 'PREPARING', 'PUBLISHING',
  'QUEUED', 'RENEWING', 'RESTARTING', 'STARTING', 'UPDATING', 'VALIDATING',
])
const failure = new Set([
  'APPLY_FAILED', 'DOWN', 'ERROR', 'EXPIRED', 'FAILED', 'INCOMPATIBLE', 'MISSING',
  'OFFLINE', 'OOM', 'ORPHANED', 'REJECTED', 'REVOKED', 'ROLLBACK_FAILED',
  'UNAVAILABLE', 'UNHEALTHY', 'UNREACHABLE',
])
const caution = new Set([
  'BLOCKED', 'COLLECTION_GAP', 'CONFLICT', 'DEGRADED', 'EXTERNAL_CHANGE',
  'MISMATCH', 'NOT_LOADED', 'PARTIAL', 'PAUSED', 'RISK', 'ROLLED_BACK',
  'STALE', 'STOPPED', 'UNKNOWN',
])
const inactive = new Set(['CANCELLED', 'CANCELED', 'DISABLED', 'DRAFT', 'EXITED', 'INACTIVE', 'REMOVED', 'SHUTDOWN'])
const normalize = value => String(value || '').trim().replace(/[\s-]+/g, '_').toUpperCase()

export function statusTone(value, context = 'resource') {
  const status = normalize(value)
  if (!status || inactive.has(status)) return ''
  if (failure.has(status)) return 'red'
  if (caution.has(status)) return 'amber'
  if (status === 'RUNNING' && context === 'operation') return 'blue'
  if (success.has(status)) return 'green'
  if (progress.has(status) || status === 'OBSERVED') return 'blue'
  return 'amber'
}

export function knownStatusTone(value, fallback = '') {
  if (typeof value !== 'string') return fallback
  const status = normalize(value)
  if (![success, progress, failure, caution, inactive].some(group => group.has(status)) && status !== 'OBSERVED') return fallback
  return statusTone(value, value === value.toLowerCase() ? 'operation' : 'resource')
}
