import { reactive } from 'vue'
import { stringify } from 'yaml'
import { request } from '../shared/api/client'
import { parseCompose, validateCompose } from './validator'

const sessions = new WeakMap()
// Preserve the API failure category; a failed request is not node evidence.
export function composeFailure(error) {
  const code = typeof error?.code === 'string' ? error.code : 'internal.operation_failed'
  return { code, key: code === 'system.project_protected' ? 'compose.systemTargetProtected' : code === 'network' ? 'console.network' : `problem.${code}`, params: error?.params || {} }
}
export function composeSource(w) {
  if (w.source === 'template' && w.swarmTemplate && w.swarmRaw) return w.swarmRaw
  if (w.source !== 'image') return w.raw || ''
  return stringify({ services: { [w.key || 'main']: { image: w.image, ...(w.port ? { expose: [String(w.port)] } : {}), ...(w.publishedPorts?.length ? { ports: w.publishedPorts } : {}), deploy: { replicas: w.replicas, resources: { limits: { memory: `${w.memory}M` } } } } } })
}
export function composeSession(w) {
  if (!sessions.has(w)) sessions.set(w, reactive({ pending: true, result: null, local: null, sequence: 0, signature: '', error: false, editing: false, review: null, reviewPending: false }))
  return sessions.get(w)
}
export function planFor(w) { return w.deploymentPlan || { storage: [], acknowledged: [] } }
export function invalidateCompose(w, { keepAcknowledged = false } = {}) {
  const s = composeSession(w)
  s.sequence++; s.pending = true; s.signature = ''; s.error = false
  w.reviewAck = false
  if (!keepAcknowledged && w.deploymentPlan?.acknowledged?.length) w.deploymentPlan = { ...w.deploymentPlan, acknowledged: [] }
}
export function composeEntrypoint(w) {
 if (w.access === 'internal' || !w.access) return undefined
 const services = Object.entries(parseCompose(composeSource(w)).tree?.services || {})
 const [name, value] = services[w.routeService || 0] || []
 const port = w.source === 'image' ? Number(w.port) : Number(String(value?.expose?.[0] || value?.ports?.[0]?.target || (typeof value?.ports?.[0] === 'string' ? value.ports[0].split(':').at(-1) : '')).split('/')[0])
 return { service: name, host: w.domain, port, gateway_id: w.gateway, ...(w.certificateID ? { certificate_id: w.certificateID } : {}) }
}
export function composeSignature(w) { return JSON.stringify([composeSource(w), w.runtimeEnv || '', w.template, w.env, w.runtime, w.networkName || '', planFor(w), composeEntrypoint(w)]) }
export async function checkCompose(w, swarm) {
  const s = composeSession(w), sequence = ++s.sequence, signature = composeSignature(w)
  s.swarm = swarm
  s.pending = true; s.error = false
  s.local = validateCompose(composeSource(w), { swarm })
  if (!s.local.schema_valid) { s.result = null; s.pending = false; s.signature = signature; return s }
  try {
    const response = await request('post', '/environments/{id}/compose-validation', { params: { path: { id: w.env } }, body: { ...(w.source === 'template' ? { catalog_app_version_id: w.template } : { compose_yaml: composeSource(w) }), runtime_env: w.runtimeEnv || '', network_name: swarm ? w.networkName || '' : '', deployment_plan: planFor(w) } })
    if (sequence !== s.sequence || signature !== composeSignature(w)) return s
    s.result = response.data; s.signature = signature
  } catch (error) {
    if (sequence !== s.sequence || signature !== composeSignature(w)) return s
    s.result = null; s.error = composeFailure(error)
  } finally { if (sequence === s.sequence) s.pending = false }
  return s
}
export function deploymentReady(w) {
  const s = composeSession(w)
  return !s.pending && !s.editing && !w.portDraft && !s.error && s.signature === composeSignature(w) && s.result?.deployable === true
}
export function composeDiagnostics(w) {
  const s = composeSession(w)
  return s.result && s.signature === composeSignature(w) ? s.result.diagnostics : s.local?.diagnostics || []
}

export async function reviewCompose(w) {
  const s = composeSession(w), signature = composeSignature(w)
  if (!deploymentReady(w) || w.step !== 4 || s.review?.signature === signature || s.reviewPending) return
  s.reviewPending = true
  const catalog = w.source === 'template'
  const body = { runtime_env: w.runtimeEnv || '', ...(!catalog && s.swarm ? { service_mode: 'create' } : {}), entrypoint: composeEntrypoint(w), ...(catalog ? { catalog_app_version_id: w.template } : { compose_yaml: composeSource(w) }), network_name: s.swarm ? w.networkName || '' : '', deployment_plan: JSON.parse(JSON.stringify(planFor(w))) }
  const environment = w.env
  try {
    const env = await request('get', '/environments/{id}', { params: { path: { id: environment } } })
    if (signature !== composeSignature(w)) return
    const response = await request('post', s.swarm && !catalog ? '/environments/{id}/service-preflight' : '/environments/{id}/preflight', { params: { path: { id: environment } }, body })
    if (signature === composeSignature(w)) s.review = { signature, id: response.data.preflight_id, etag: env.response.headers.get('ETag'), preview: response.data }
  } catch (error) { if (signature === composeSignature(w)) { s.error = composeFailure(error); s.review = null } }
  finally {
    s.reviewPending = false
    if (signature !== composeSignature(w) && deploymentReady(w) && w.step === 4) await reviewCompose(w)
  }
}
