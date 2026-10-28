export const emptyIPAccess = () => ({ access: [], forbidden: [] })
export function ipAccessFor(controls, scope, id) {
  const value = controls?.find(entry => entry.scope === scope && entry.resource_id === id)
  return { access: [...(value?.access || [])], forbidden: [...(value?.forbidden || [])], ...(value?.client_ip ? { clientIP: { header: value.client_ip.header, trusted_proxies: [...value.client_ip.trusted_proxies] } } : {}) }
}
export function domainAccessControls(base, domainID, activeRows, work) {
  const ids = new Set([...activeRows, ...work.routes].map(route => route.id))
  const result = (base || []).filter(entry => !(entry.scope === 'server' && entry.resource_id === domainID) && !(entry.scope === 'location' && ids.has(entry.resource_id)))
  const append = (scope, id, value) => {
    if (value && (value.access.length || value.forbidden.length || value.clientIP)) result.push({ scope, resource_id: id, access: [...value.access], forbidden: [...value.forbidden], ...(value.clientIP ? { client_ip: { header: value.clientIP.header, trusted_proxies: [...value.clientIP.trusted_proxies] } } : {}) })
  }
  append('server', domainID, work.ipAccess)
  for (const route of work.routes) append('location', route.id, route.ipAccess)
  return result
}
