import { translate } from '../i18n'

type ServiceIdentity = { id: string; name?: string | null }

/** Presentation only: callers keep the original ID for navigation and writes. */
export function serviceName(services: readonly ServiceIdentity[], id: string | null | undefined): string {
  if (!id) return ''
  const name = services.find(service => service.id === id)?.name
  return typeof name === 'string' && name.trim() ? name : translate('references.service_unavailable', { id })
}

/** Extend the archive's existing search without changing scope or ordering. */
export function serviceSearch(services: readonly ServiceIdentity[], id: string): string {
  return `${serviceName(services, id)} ${id}`.toLowerCase()
}
