import { createServer } from 'node:http'
import { createHash } from 'node:crypto'
import process from 'node:process'
import { readFile, stat } from 'node:fs/promises'
import { extname, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { URL } from 'node:url'
import YAML from 'yaml'

// This process is deliberately self-contained. It never proxies requests, opens a
// Docker socket, or reads Controller configuration or production credentials.
const root = resolve(fileURLToPath(new URL('../../internal/api/frontend-dist/', import.meta.url)))
const host = '127.0.0.1'
const port = Number(process.env.NSC_FAKER_PORT || 4174)
if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('Invalid NSC_FAKER_PORT')
const mime = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.json': 'application/json', '.png': 'image/png', '.ico': 'image/x-icon' }
const languages = ['en', 'ja', 'de', 'zh-CN', 'zh-TW', 'fr', 'ru', 'es']
const now = '2026-09-27T00:00:00Z'
const demoCompose = 'services:\n  web:\n    image: nginx:alpine\n    expose: ["80"]\n'
const pairCompose = 'services:\n  web:\n    image: nginx:alpine\n  worker:\n    image: nginx:alpine\n    volumes: [data:/data]\nvolumes:\n  data: {}\n'
const seed = () => ({
  projects: [{ id: 'prj_demo', slug: 'demo', name: 'Demo', display_name: 'Demo', description: 'Isolated demonstration project', revision: 1, created_at: now }],
  environments: [{ id: 'env_demo', project_id: 'prj_demo', name: 'dev', display_name: 'Development', kind: 'development', runtime_id: 'rt_demo', project_name: 'demo_dev', gateway_ids: [], revision: 1, created_at: now }],
  pools: [{ id: 'pool_demo', name: 'demo-pool', backend_network: 'demo_backend', config_revision: 1, revision: 1 }],
  runtimes: [{ id: 'rt_demo', pool_id: 'pool_demo', name: 'demo-runtime', runtime_type: 'docker', enabled: true, management_enabled: true, status: 'ONLINE', revision: 1, last_seen_at: now }],
  applications: [], services: [], domains: [], routes: [], gateways: [], certificates: [], operations: [],
  catalog: [{ id: 'cat_demo_nginx', source_id: 'source_demo', app_key: 'nginx', name: 'Nginx', description: 'Isolated sample web server',
    version: '1.0.0', artifact_digest: 'fake-catalog-artifact', compose_sha256: 'fake-compose-digest', compose_yaml: demoCompose,
    services: ['web'], compose_compatible: true, swarm_compatible: true, withdrawn: false, revision: 1 },
  { id: 'cat_demo_pair', source_id: 'source_demo', app_key: 'pair', name: 'Pair', description: 'Two-service lifecycle fixture',
    version: '1.0.0', artifact_digest: 'fake-pair-artifact', compose_sha256: 'fake-pair-digest', compose_yaml: pairCompose,
    services: ['web', 'worker'], compose_compatible: true, swarm_compatible: true, withdrawn: false, revision: 1 }],
  drafts: new Map(), reviews: new Map(), sources: new Map(), revisions: [], sessions: new Map(), next: 1,
})
let db = seed()
const send = (res, status, data, headers = {}) => {
  res.writeHead(status, { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json', 'Cache-Control': 'no-store', ...headers })
  res.end(JSON.stringify(data))
}
const problem = (res, status, code) => send(res, status, { type: 'about:blank', title: code, status, code })
const list = (res, rows) => send(res, 200, { data: rows, pagination: { limit: 200, next_cursor: '' } })
const body = async req => {
  let value = ''
  for await (const chunk of req) {
    value += chunk
    if (value.length > 4 * 1024 * 1024) throw new Error('request too large')
  }
  return value ? JSON.parse(value) : {}
}
const sessionFor = req => db.sessions.get(/(?:^|;\s*)nsc_faker=([^;]+)/.exec(req.headers.cookie || '')?.[1])
const sessionResponse = session => ({ authenticated: Boolean(session), locale: session?.locale || 'zh-CN', supported_languages: languages,
  ...(session ? { role: session.role, user_id: session.user, username: session.user, csrf_token: session.csrf, expires_at: 4102444800 } : {}) })
const etag = row => ({ ETag: `"rev-${row?.revision || 0}"` })
const collection = new Map([['projects', 'projects'], ['gateway-pools', 'pools'], ['runtimes', 'runtimes'], ['applications', 'applications'], ['services', 'services'], ['domains', 'domains'], ['routes', 'routes'], ['gateways', 'gateways'], ['certificates', 'certificates'], ['operations', 'operations']])
async function api(req, res, path) {
  const method = req.method
  if (path === '/auth/session' && method === 'GET') return send(res, 200, { data: sessionResponse(sessionFor(req)) })
  if (path === '/auth/login' && method === 'POST') {
    const value = await body(req)
    if (!['admin', 'viewer'].includes(value.username) || value.password !== 'demo') return problem(res, 401, 'auth.invalid_credentials')
    const token = `demo-${db.next++}`
    const session = { user: value.username, role: value.username === 'admin' ? 'administrator' : 'viewer', locale: 'zh-CN', csrf: `csrf-${token}` }
    db.sessions.set(token, session)
    return send(res, 200, { data: sessionResponse(session) }, { 'Set-Cookie': `nsc_faker=${token}; HttpOnly; SameSite=Strict; Path=/` })
  }
  const session = sessionFor(req)
  if (!session) return problem(res, 401, 'auth.required')
  if (method !== 'GET' && method !== 'HEAD' && (session.role !== 'administrator' || req.headers['x-csrf-token'] !== session.csrf)) return problem(res, 403, 'auth.read_only')
  if (path === '/auth/logout' && method === 'POST') {
    const token = /(?:^|;\s*)nsc_faker=([^;]+)/.exec(req.headers.cookie || '')?.[1]
    db.sessions.delete(token)
    return send(res, 200, { data: sessionResponse(null) }, { 'Set-Cookie': 'nsc_faker=; HttpOnly; SameSite=Strict; Path=/; Max-Age=0' })
  }
  if (path === '/locale' && method === 'POST') {
    const value = await body(req)
    if (!languages.includes(value.lang)) return problem(res, 422, 'validation.failed')
    session.locale = value.lang
    return send(res, 200, { data: sessionResponse(session) })
  }
  if (path === '/__faker/reset' && method === 'POST') { db = seed(); return send(res, 200, { data: { reset: true } }) }
  if (path === '/catalog/apps' && method === 'GET') return list(res, db.catalog)
  const readiness = /^\/operations\/([^/]+)\/readiness$/.exec(path)
  if (readiness && method === 'GET') {
    const operation = db.operations.find(row => row.id === readiness[1])
    if (!operation) return problem(res, 404, 'resource.not_found')
    // Faker stores acceptance but has no real workload or Gateway evidence.
    const stages = ['accepted', 'workload', 'certificate', 'gateway', 'client_access'].map(name => ({ name,
      state: name === 'accepted' ? 'CONFIRMED' : 'UNKNOWN',
      ...(name === 'accepted' ? {} : { error_code: name === 'client_access' ? 'client_not_verified' : 'workload_unavailable' }) }))
    return send(res, 200, { data: { operation_id: operation.id, revision: operation.revision || 1,
      revision_id: operation.revision_id || '', observed_at: new Date().toISOString(), baseline_digest: createHash('sha256').update(JSON.stringify({ operation_id: operation.id, revision_id: operation.revision_id || '' })).digest('hex'),
      stages, gateway_targets: [], access_targets: [] } }, { 'Cache-Control': 'no-store' })
  }
  const catalogVersion = /^\/catalog\/versions\/([^/]+)$/.exec(path)
  if (catalogVersion && method === 'GET') {
    const version = db.catalog.find(item => item.id === catalogVersion[1])
    return version ? send(res, 200, { data: version }, etag(version)) : problem(res, 404, 'resource.not_found')
  }
  const deploymentDraft = /^\/environments\/([^/]+)\/deployment-draft$/.exec(path)
  if (deploymentDraft) {
    if (session.role !== 'administrator') return problem(res, 403, 'auth.read_only')
    if (!db.environments.some(item => item.id === deploymentDraft[1])) return problem(res, 404, 'resource.not_found')
    const key = `${deploymentDraft[1]}:${session.user}`
    const current = db.drafts.get(key) || { environment_id: deploymentDraft[1], user_id: session.user, revision: 0 }
    if (method === 'GET') return send(res, 200, { data: current }, etag(current))
    if (req.headers['if-match'] !== `"rev-${current.revision}"`) return problem(res, 412, 'conflict')
    if (method === 'PUT') {
      const input = await body(req)
      if (!input.data || typeof input.data !== 'object') return problem(res, 422, 'validation.failed')
      const saved = { ...current, data: input.data, revision: current.revision + 1 }
      db.drafts.set(key, saved)
      return send(res, 200, { data: saved }, etag(saved))
    }
    if (method === 'DELETE') { db.drafts.delete(key); res.writeHead(204); return res.end() }
  }
  const projectEnvironments = /^\/projects\/([^/]+)\/environments$/.exec(path)
  if (projectEnvironments) {
    const project = db.projects.find(item => item.id === projectEnvironments[1])
    if (!project) return problem(res, 404, 'resource.not_found')
    if (method === 'GET') return list(res, db.environments.filter(item => item.project_id === project.id))
    if (method === 'POST') {
      if (req.headers['if-match'] !== `"rev-${project.revision}"`) return problem(res, 412, 'conflict')
      const input = await body(req)
      if (!input.name) return problem(res, 422, 'validation.failed')
      const row = { id: `env_fake_${db.next++}`, project_id: project.id, name: input.name, display_name: input.display_name || input.name,
        kind: input.kind || 'custom', runtime_id: input.runtime_id || 'rt_demo', project_name: `${project.slug}-${input.name}`, gateway_ids: input.gateway_ids || [], revision: 1, created_at: now }
      db.environments.push(row)
      project.revision++
      return send(res, 201, { data: row }, etag(row))
    }
  }
  const environment = /^\/environments\/([^/]+)$/.exec(path)
  if (environment && method === 'GET') {
    const row = db.environments.find(item => item.id === environment[1])
    return row ? send(res, 200, { data: row }, etag(row)) : problem(res, 404, 'resource.not_found')
  }
  const domainDrafts = /^\/environments\/([^/]+)\/domain-drafts$/.exec(path)
  if (domainDrafts && method === 'GET') return list(res, [])
  const composeCheck = /^\/environments\/([^/]+)\/compose-validation$/.exec(path)
  if (composeCheck && method === 'POST') {
    const input = await body(req)
    if (!db.environments.some(item => item.id === composeCheck[1]) || !input.compose_yaml) return problem(res, 422, 'validation.failed')
    return send(res, 200, { data: { deployable: true, diagnostics: [], services: [{ name: 'web', mounts: [], ports: [] }], nodes: [] } })
  }
  const catalogReview = /^\/environments\/([^/]+)\/preflight$/.exec(path)
  if (catalogReview && method === 'POST') {
    const env = db.environments.find(item => item.id === catalogReview[1])
    const input = await body(req)
    const version = db.catalog.find(item => item.id === input.catalog_app_version_id && !item.withdrawn)
    if (!env || !version || env.application_id || input.compose_yaml) return problem(res, 422, 'validation.failed')
    const preflight_id = `review_fake_${db.next++}`
    db.reviews.set(preflight_id, { env: env.id, version: version.id, revision: env.revision })
    return send(res, 200, { data: { preflight_id, project_name: env.project_name, resolved_yaml: version.compose_yaml,
      validation: { deployable: true, diagnostics: [] }, configuration_source: { catalog_app_version_id: version.id } } })
  }
  const deployment = /^\/environments\/([^/]+)\/deployments$/.exec(path)
  if (deployment && method === 'POST') {
    const env = db.environments.find(item => item.id === deployment[1])
    const input = await body(req)
    const review = db.reviews.get(input.preflight_id)
    if (!env || !review || review.env !== env.id || env.revision !== review.revision || req.headers['if-match'] !== `"rev-${env.revision}"`) return problem(res, 412, 'conflict')
    const version = db.catalog.find(item => item.id === review.version)
    const app = { id: `app_fake_${db.next++}`, runtime_id: env.runtime_id, name: version.name, project_name: env.project_name,
      catalog_app_version_id: version.id, status: 'RUNNING', managed: true, system: false, lifecycle_operations: ['START', 'STOP', 'DOWN', 'REDEPLOY'],
      revision: 1, created_at: now, updated_at: now }
    const revision = { id: `rev_fake_${db.next++}`, application_id: app.id, sequence: 1, compose_sha256: version.compose_sha256,
      environment_sha256: 'fake-env-digest', resolved_sha256: 'fake-resolved-digest', environment_keys: [], encryption_key_id: 'faker', created_at: now,
      configuration_source: { catalog_app_version_id: version.id } }
    app.active_revision_id = app.desired_revision_id = revision.id
    db.applications.push(app); db.revisions.push(revision); db.sources.set(app.id, version.compose_yaml)
    for (const name of version.services) db.services.push({ id: `svc_fake_${db.next++}`, application_id: app.id, runtime_id: env.runtime_id, name,
      service_key: `${env.project_name}/${name}`, namespace: env.project_name, managed: true, system: false, status: 'RUNNING',
      desired_instances: 1, running_instances: 1, healthy_instances: 1, revision: 1, created_at: now, updated_at: now })
    env.application_id = app.id; env.revision++
    const operation = { id: `op_fake_${db.next++}`, application_id: app.id, runtime_id: env.runtime_id, revision_id: revision.id,
      type: 'INSTALL', status: 'SUCCEEDED', attempt: 1, revision: 1, created_at: now, updated_at: now }
    db.operations.push(operation); db.reviews.delete(input.preflight_id)
    return send(res, 202, { data: operation })
  }
  const appRevisions = /^\/applications\/([^/]+)\/revisions$/.exec(path)
  if (appRevisions && method === 'GET') return list(res, db.revisions.filter(item => item.application_id === appRevisions[1]))
  const serviceRestorePreview = /^\/services\/([^/]+)\/revisions\/([^/]+)\/restore-preview$/.exec(path)
  if (serviceRestorePreview && method === 'POST') {
    const service = db.services.find(item => item.id === serviceRestorePreview[1])
    const app = db.applications.find(item => item.id === service?.application_id)
    const revision = db.revisions.find(item => item.id === serviceRestorePreview[2] && item.application_id === app?.id)
    if (!service || !app || !revision) return problem(res, 404, 'resource.not_found')
    const id = `restore_fake_${db.next++}`
    const value = { id, application_id: app.id, source_revision_id: revision.id, base_revision: app.revision,
      expires_at: new Date(Date.now() + 5 * 60_000).toISOString(), data_restored: false,
      preview: { resolved_yaml: db.sources.get(app.id), diff: {} } }
    db.reviews.set(id, { kind: 'service-restore', service: service.id, revision: revision.id, base: app.revision })
    return send(res, 200, { data: value })
  }
  const serviceRestore = /^\/services\/([^/]+)\/restore$/.exec(path)
  if (serviceRestore && method === 'POST') {
    const service = db.services.find(item => item.id === serviceRestore[1])
    const app = db.applications.find(item => item.id === service?.application_id)
    const input = await body(req)
    const review = db.reviews.get(input.review_id)
    if (!service || !app || !review || review.kind !== 'service-restore' || review.service !== service.id) return problem(res, 412, 'conflict')
    if (req.headers['if-match'] !== `"rev-${review.base}"` || app.revision !== review.base) return problem(res, 412, 'conflict')
    if (!input.acknowledge_data_boundary || !req.headers['idempotency-key']) return problem(res, 400, 'validation.failed')
    const revision = { ...db.revisions.find(item => item.id === review.revision), id: `rev_fake_${db.next++}`,
      sequence: db.revisions.filter(item => item.application_id === app.id).length + 1, created_at: now }
    const operation = { id: `op_fake_${db.next++}`, application_id: app.id, service_id: service.id, runtime_id: app.runtime_id,
      revision_id: revision.id, type: 'UPGRADE', status: 'SUCCEEDED', attempt: 1, revision: 1, created_at: now, updated_at: now }
    db.revisions.push(revision); db.operations.push(operation)
    app.active_revision_id = app.desired_revision_id = revision.id
    app.revision++
    db.reviews.delete(input.review_id)
    return send(res, 202, { data: operation })
  }
  const serviceDelete = /^\/services\/([^/]+)\/delete$/.exec(path)
  if (serviceDelete && method === 'POST') {
    const service = db.services.find(item => item.id === serviceDelete[1])
    const app = db.applications.find(item => item.id === service?.application_id)
    if (!service || !app) return problem(res, 404, 'resource.not_found')
    const key = req.headers['idempotency-key']
    const prior = db.operations.find(item => item.service_id === service.id && item.idempotency_key === key && item.type === 'UPGRADE')
    if (prior) return send(res, 202, { data: prior })
    if (!key || req.headers['if-match'] !== `"rev-${service.revision}"`) return problem(res, 412, 'conflict')
    const input = await body(req)
    if (input.acknowledge_data_boundary !== true) return problem(res, 422, 'validation.failed')
    if (!service.managed || service.system || service.status === 'REMOVED' || app.status !== 'RUNNING') return problem(res, 412, 'conflict')
    const source = YAML.parse(db.sources.get(app.id) || '')
    if (!source?.services?.[service.name] || Object.keys(source.services).length < 2) return problem(res, 422, 'service.last_service')
    delete source.services[service.name]
    const revision = { ...db.revisions.find(item => item.id === app.active_revision_id), id: `rev_fake_${db.next++}`,
      sequence: db.revisions.filter(item => item.application_id === app.id).length + 1, created_at: now }
    const operation = { id: `op_fake_${db.next++}`, application_id: app.id, service_id: service.id, runtime_id: app.runtime_id,
      revision_id: revision.id, type: 'UPGRADE', status: 'SUCCEEDED', attempt: 1, revision: 1,
      idempotency_key: key, created_at: now, updated_at: now }
    db.revisions.push(revision); db.operations.push(operation); db.sources.set(app.id, YAML.stringify(source))
    app.active_revision_id = app.desired_revision_id = revision.id; app.revision++
    service.status = 'REMOVED'; service.removed_at = now; service.running_instances = 0; service.desired_instances = 0; service.revision++
    for (const route of db.routes) if (route.service_id === service.id) route.status = 'UNAVAILABLE'
    return send(res, 202, { data: operation })
  }
  const appSource = /^\/applications\/([^/]+)\/(source|draft)$/.exec(path)
  if (appSource && method === 'GET') {
    if (session.role !== 'administrator') return problem(res, 403, 'auth.read_only')
    const app = db.applications.find(item => item.id === appSource[1])
    if (!app) return problem(res, 404, 'resource.not_found')
    if (appSource[2] === 'draft') return send(res, 200, { data: { application_id: app.id, application_revision: app.revision, revision: 0 } }, etag({ revision: 0 }))
    return send(res, 200, { data: { compose_yaml: db.sources.get(app.id), runtime_env: '', revision_id: app.active_revision_id,
      baseline_revision_id: app.active_revision_id, format: 'yaml' } })
  }
  const serviceInstances = /^\/services\/([^/]+)\/instances$/.exec(path)
  if (serviceInstances && method === 'GET') return list(res, [])
  const vars = /^\/(projects|environments)\/([^/]+)\/variables$/.exec(path)
  if (vars && method === 'GET') return send(res, 200, { data: { revision: 1, values: [] } }, etag({ revision: 1 }))
  const nginx = /^\/gateway-pools\/([^/]+)\/nginx$/.exec(path)
  if (nginx && method === 'GET') return send(res, 200, { data: { active: { revision: 1, routes: [], snippets: [] }, draft: { revision: 0, routes: [], snippets: [] } } })
  const cluster = /^\/runtimes\/([^/]+)\/cluster(?:\/nodes)?$/.exec(path)
  if (cluster && method === 'GET') return path.endsWith('/nodes') ? list(res, []) : send(res, 200, { data: { id: cluster[1], observed_at: now, inventory_complete: true, stale: false, manager_available: false } })
  const resource = /^\/([a-z-]+)(?:\/([^/]+))?$/.exec(path)
  if (resource && collection.has(resource[1])) {
    const rows = db[collection.get(resource[1])]
    if (method === 'GET') {
      if (!resource[2]) return list(res, rows)
      const row = rows.find(item => item.id === resource[2])
      return row ? send(res, 200, { data: row }, etag(row)) : problem(res, 404, 'resource.not_found')
    }
    if (resource[1] === 'projects' && !resource[2] && method === 'POST') {
      const input = await body(req)
      if (!input.name || !input.slug) return problem(res, 422, 'validation.failed')
      const row = { id: `prj_fake_${db.next++}`, name: input.name, slug: input.slug, description: input.description || '', revision: 1, created_at: now }
      if (input.initial_environment) {
        const initial = input.initial_environment
        const env = { id: `env_fake_${db.next++}`, project_id: row.id, name: initial.name, display_name: initial.display_name || initial.name,
          kind: initial.kind || 'custom', runtime_id: input.default_runtime_id || 'rt_demo', project_name: `${row.slug}-${initial.name}`,
          gateway_ids: [], revision: 1, created_at: now }
        row.initial_environment_id = env.id
        db.environments.push(env)
      }
      rows.push(row)
      return send(res, 201, { data: row }, etag(row))
    }
  }
  return problem(res, 501, 'faker.unimplemented')
}
const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url, `http://${host}:${port}`)
    const path = decodeURIComponent(url.pathname)
    if (path === '/healthz') return send(res, 200, { status: 'ok', kind: 'faker' })
    if (path.startsWith('/api/v1/')) return await api(req, res, path.slice('/api/v1'.length))
    if (req.method !== 'GET' && req.method !== 'HEAD') return problem(res, 405, 'method.not_allowed')
    const candidate = resolve(root, '.' + path)
    if (candidate !== root && !candidate.startsWith(root + sep)) return problem(res, 404, 'resource.not_found')
    const file = await stat(candidate).then(info => info.isFile() ? candidate : resolve(root, 'index.html')).catch(() => resolve(root, 'index.html'))
    const data = await readFile(file)
    res.writeHead(200, { 'Content-Type': mime[extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store' })
    res.end(req.method === 'HEAD' ? undefined : data)
  } catch { problem(res, 500, 'faker.internal') }
})
server.listen(port, host, () => process.stdout.write(`NSC faker listening on http://${host}:${port}\n`))
