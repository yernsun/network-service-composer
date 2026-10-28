// Presentation of Compose ports; the server is authoritative after interpolation.
export function portFields(value) {
  if (value && typeof value === 'object') return { ...value, target: String(value.target ?? ''), published: String(value.published ?? ''), protocol: value.protocol || 'tcp', mode: value.mode || 'ingress', host_ip: value.host_ip || '' }
  const [address, protocol = 'tcp'] = String(value).split('/')
  const parts = address.split(':')
  const target = parts.pop(), published = parts.pop() || '', host_ip = parts.join(':').replace(/^\[|\]$/g, '')
  return { target, published, host_ip, protocol, mode: 'ingress' }
}
export function portValue(fields, swarm) {
  const valid = n => /^\d+$/.test(String(n)) && Number(n) > 0 && Number(n) <= 65535
  if (!valid(fields.target) || !valid(fields.published) || !['tcp', 'udp'].includes(fields.protocol)) return null
  const value = { target: Number(fields.target), published: String(Number(fields.published)), protocol: fields.protocol }
  if (swarm) value.mode = fields.mode === 'host' ? 'host' : 'ingress'
  else if (fields.host_ip?.trim()) value.host_ip = fields.host_ip.trim()
  return value
}
export function publishedPorts(tree) {
  return Object.entries(tree?.services || {}).flatMap(([service, definition]) => (Array.isArray(definition?.ports) ? definition.ports : []).map((raw, index) => ({ service, index, raw, ...portFields(raw) })))
}
