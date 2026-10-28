import { translate } from '../i18n'

const fresh = value => value && !value.stale && Number.isFinite(Date.parse(value.at)) && Date.now() - Date.parse(value.at) <= 30000 && Date.parse(value.at) - Date.now() <= 5000
const gib = bytes => (bytes / 1073741824).toFixed(2)
const states = new Set(['healthy', 'degraded', 'ready', 'disabled', 'leader', 'follower', 'initializing', 'stale', 'offline', 'apply_failed', 'incompatible'])
const status = value => translate(`nodeHealth.${states.has(value) ? value : 'unavailable'}`)

export function nodeMetrics(node) {
  const host = fresh(node.host) ? node.host : null
  return { cpu: Number.isFinite(host?.cpu_percent) ? Number(host.cpu_percent.toFixed(1)) : null,
    used: Number.isFinite(host?.memory_used) ? Number(gib(host.memory_used)) : null,
    total: Number.isFinite(host?.memory_total) ? Number(gib(host.memory_total)) : null }
}

export function nodeHealth(node, kind) {
  if (kind === 'disk') {
    if (!fresh(node.host) || !node.host.disks?.length) return status('unavailable')
    return node.host.disks.map(disk => `${disk.mount}: ${gib(disk.used)} / ${gib(disk.total)} GiB${disk.read_only ? ` · ${translate('nodeHealth.readOnly')}` : ''}`).join('; ')
  }
  const health = fresh(node.controller) ? node.controller : null
  if (kind === 'controller') return health ? `${status(health.controller)} · ${status(health.role)} · ${health.instance_id}` : status('unavailable')
  return status(health?.[kind])
}

export function nodeMetric(node, kind) {
  // Demo nodes retain the archive's independent simulated metrics. Real nodes
  // have null numeric fields when no host observation was returned.
  const metrics = node.host ? nodeMetrics(node) : node
  if (kind === 'cpu') return Number.isFinite(metrics.cpu) ? `${metrics.cpu}%` : status('unavailable')
  return Number.isFinite(metrics.used) && Number.isFinite(metrics.total) ? `${metrics.used} / ${metrics.total} GiB` : status('unavailable')
}
