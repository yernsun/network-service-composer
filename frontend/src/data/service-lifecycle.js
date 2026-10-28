import { translate } from '../i18n'
import { formatDate } from '../shared/i18n'

const part = (key, value) => value ? `${translate(key)} ${formatDate(value)}` : ''

export function serviceLifecycle(service) {
  const dockerService = ['stack', 'swarmservice'].includes(service.mode)
  const created = dockerService && service.runtimeCreatedAt
    ? part('service.runtime_created_at', service.runtimeCreatedAt)
    : part('service.first_seen_at', service.recordCreatedAt)
  const updated = dockerService && service.runtimeUpdatedAt
    ? part('service.runtime_updated_at', service.runtimeUpdatedAt)
    : part('service.record_updated_at', service.recordUpdatedAt)
  const removed = service.status === 'REMOVED'
    ? service.removedAt
      ? part('service.removed_at', service.removedAt)
      : translate('service.removed_at_unknown')
    : ''
  const rows = [created, updated, removed].filter(Boolean)
  return {
    primary: rows[0] || translate('service.lifecycle_unknown'),
    secondary: rows.slice(1).join(' · '),
    detail: rows.join(' · ') || translate('service.lifecycle_unknown'),
  }
}
