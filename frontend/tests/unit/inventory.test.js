import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loadInventory, queryClient } from '../../src/data/inventory'
import { invalidateInventoryWrite } from '../../src/data/inventory-cache'
import { instanceTimeRows } from '../../src/data/service-instances'
import { allPages } from '../../src/data/pagination'
import { request } from '../../src/shared/api/client'
import { setLocale } from '../../src/shared/i18n'
import { applicationServiceVersions } from '../../src/data/application-service-versions'
import { serviceLifecycle } from '../../src/data/service-lifecycle'
vi.mock('../../src/data/pagination', () => ({ allPages: vi.fn() }))
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn() }))
let tables
const loadCompose = () => loadInventory(false, { path: '/projects/p/environments/e/applications/a/compose' })
beforeEach(() => {
  queryClient.clear()
  tables = {
    '/projects': [{ id: 'empty', slug: 'empty', name: 'Empty' }, { id: 'p', slug: 'literal', name: '删除 {name}' }, { id: 'other', slug: 'other', name: 'Other' }],
    '/gateway-pools': [{ id: 'pool', config_revision: 9 }],
    '/runtimes': [{ id: 'rt', runtime_type: 'swarm', status: 'ONLINE', enabled: true, management_enabled: true, name: 'Runtime' }],
    '/applications': [{ id: 'a', runtime_id: 'rt', name: 'READY', project_name: 'literal-prod', desired_revision_id: 'revision', active_revision_id: 'revision', managed: true }],
    '/services': [{ id: 's', application_id: 'a', runtime_id: 'rt', name: 'Service', revision: 90210, desired_instances: 2, running_instances: 2, healthy_instances: 1, runtime_created_at: '2026-09-10T00:00:00Z', runtime_updated_at: '2026-09-14T00:00:00Z', created_at: '2026-09-09T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' }, { id: 'outside', runtime_id: 'rt', name: 'Shared host is not ownership' }],
    '/domains': [{ id: 'domain', host: 'literal.example.test', pool_id: 'pool', revision: 4 }, { id: 'shared', host: 'shared.example.test', pool_id: 'pool', revision: 5 }],
    '/routes': [{ id: 'route', domain_id: 'domain', path: '/', backend: { service_id: 's' }, strip_path: true, websocket_enabled: true, enabled: true }],
    '/gateways': [{ id: 'g', pool_id: 'pool', enabled: true, status: 'READY', desired_config_revision: 9, applied_config_revision: 9,
      model_digest: 'a'.repeat(64), rendered_digest: 'b'.repeat(64), evidence: { loading_confirmed: true, startup_state: 'CONFIRMED', configuration_state: 'CONFIRMED', confirmed_revision: 9, last_confirmed_revision: 9, last_confirmed_at: '2026-09-15T00:00:00Z', model_digest: 'a'.repeat(64), rendered_digest: 'b'.repeat(64), disk_digest: 'b'.repeat(64) } }],
    '/certificates': [], '/operations': [{ id: 'operation', application_id: 'a', status: 'UNRECOGNIZED', revision_id: 'revision' }], '/catalog/apps': [],
  }
  allPages.mockImplementation(async (path, params) => {
    if (path === '/projects/{id}/environments') return params.id === 'p' ? [{ id: 'e', project_id: 'p', name: 'prod', runtime_id: 'rt', application_id: 'a' }] : []
    if (path === '/applications/{id}/revisions') return [{ id: 'revision', sequence: 3 }]
    if (path === '/services/{id}/instances') return [
      { address: '10.0.2.113', runtime_state: 'RUNNING', ready_state: 'READY', health_state: 'UNKNOWN' },
      { address: '10.0.2.112', runtime_state: 'DEAD', ready_state: 'DRAINING', health_state: 'UNKNOWN' },
    ]
    if (path === '/runtimes/{id}/cluster/nodes') return [{ id: 'n', role: 'manager', state: 'ready', availability: 'active', leader: true, connected: true, stale: false, inventory_complete: true }]
    if (path === '/runtimes/{id}/cluster/nodes/{nodeId}/instances') return [
      { id: 'current', service_id: 's', node_id: 'n', kind: 'task', state: 'running', observed_at: '2026-09-15T00:00:00Z', historical: false, image: 'nginx@sha256:example', metric: null },
      { id: 'current', service_id: 's', node_id: 'n', kind: 'task', state: 'running', created_at: '2026-09-10T00:00:00Z', updated_at: '2026-09-14T21:00:00Z', state_changed_at: '2026-09-14T22:00:00Z', container_created_at: '2026-09-14T22:01:00Z', started_at: '2026-09-14T22:02:00Z', finished_at: '2026-09-14T23:00:00Z', restart_count: 3, exit_code: 0, oom_killed: false, observed_at: '2026-09-15T00:01:00Z', historical: false, image: 'nginx@sha256:example', metric: null },
      { id: 'old', service_id: 's', node_id: 'n', kind: 'task', state: 'shutdown', observed_at: '2026-09-14T00:00:00Z', historical: true, metric: null },
    ]
    return tables[path] || []
  })
  request.mockImplementation(async (_, path) => {
    if (path.startsWith('/applications/')) throw { status: 403 }
    if (path === '/runtimes/{id}/cluster') return { data: { id: 'cluster', stale: false, manager_available: true, error: '', observed_at: new Date().toISOString(), inventory_complete: true }, response: new Response(null) }
    return { data: { revision: 7, values: [{ key: 'PASSWORD', mask: true, secret: true }, { key: 'LITERAL', value: '删除 {name}' }] }, response: new Response(null, { headers: { ETag: '"rev-7"' } }) }
  })
})
afterEach(() => queryClient.clear())
describe('archive API data contract', () => {
  it('fetches fresh navigation data inside the existing five-second cache window', async () => {
    const first = await loadInventory(false, { path: '/projects' })
    const originalName = first.state.projects.find(project => project.id === 'p').name
    tables['/projects'] = tables['/projects'].map(project => project.id === 'p' ? { ...project, name: 'Latest name' } : project)
    allPages.mockClear()
    const latest = await loadInventory(false, { path: '/projects', fresh: true })
    expect(originalName).not.toBe('Latest name')
    expect(latest.state.projects.find(project => project.id === 'p').name).toBe('Latest name')
    expect(allPages.mock.calls.some(([path]) => path === '/projects')).toBe(true)
    expect(allPages.mock.calls.some(([path]) => path === '/applications/{id}/source')).toBe(false)
  })
  it('uses the Service instances API for Ready endpoints instead of assuming they are embedded in Service rows', async () => {
    const { state } = await loadCompose()
    expect(state.services.find(service => service.id === 's').endpoints).toEqual([['10.0.2.113', true]])
    expect(allPages).toHaveBeenCalledWith('/services/{id}/instances', { id: 's' }, expect.any(AbortSignal), {})
  })
  it('keeps system application environment and actual runtime separate without fetching deployment source', async () => {
    const original = allPages.getMockImplementation()
    allPages.mockImplementation((path, params, ...rest) => {
      if (path === '/applications') return Promise.resolve([{ id: 'system', name: 'NGINX', runtime_id: 'docker-node', system: true, managed: false, environment: { id: 'e', project_id: 'p' } }])
      if (path === '/applications/{id}/revisions') return Promise.resolve([])
      return original(path, params, ...rest)
    })
    const { state } = await loadInventory(false, { path: '/projects/p/environments/e/services' })
    expect(state.apps[0]).toMatchObject({ id: 'system', system: true, project: 'p', env: 'e', runtime: 'docker-node', managed: false, revisions: [] })
    expect(state.parameters13.documents.system).toMatchObject({ unavailable: true, source: '', draft: null })
    expect(request.mock.calls.some(([, path]) => path.startsWith('/applications/'))).toBe(false)
  })
  it('restores the persisted source draft without substituting active source', async () => {
    const original = request.getMockImplementation()
    request.mockImplementation((method, path, options) => {
      if (path === '/applications/{id}/source') return Promise.resolve({ data: { compose_yaml: 'active source', runtime_env: 'MODE=active' } })
      if (path === '/applications/{id}/draft') return Promise.resolve({ data: { revision: 2, application_revision: 5, compose_yaml: 'saved draft', runtime_env: 'MODE=draft' }, response: new Response(null, { headers: { ETag: '"rev-2"' } }) })
      return original(method, path, options)
    })
    const { state, versions } = await loadCompose()
    expect(state.parameters13.documents.a).toMatchObject({ source: 'active source', runtime: 'MODE=active', draft: { source: 'saved draft', runtime: 'MODE=draft', base: 5 }, unavailable: false })
    expect(versions.get('application-draft:a')).toBe('"rev-2"')
  })
  it('uses explicit membership and never assigns shared infrastructure to an environment', async () => {
    const { state } = await loadCompose()
    expect(state.project).toBe('p')
    expect(state.apps[0]).toMatchObject({ id: 'a', project: 'p', env: 'e', name: 'READY' })
    expect(state.services[0]).toMatchObject({ project: 'p', env: 'e', specVersion: 3, observedVersion: 3, runtimeCreatedAt: '2026-09-10T00:00:00Z', runtimeUpdatedAt: '2026-09-14T00:00:00Z' })
    expect(state.services[1]).toMatchObject({ project: '', env: '' })
    expect(state.gateways[0].env).toBe('')
    expect(state.environments[0].gateways).toEqual([])
    expect(state.domains[0]).toMatchObject({ project: 'p', env: 'e', environmentIds: ['e'], loaded: { g: 4 } })
    expect(state.domains[0].active).toMatchObject({ body: '1m' })
    expect(state.domains[0].active.routes[0]).toMatchObject({ strip: true, websocket: true, timeout: 60, connect: 60 })
    expect(state.domains[1]).toMatchObject({ project: '', env: '', environmentIds: [] })
  })
  it('treats Docker health none as explicitly not configured', async () => {
    const original = allPages.getMockImplementation()
    allPages.mockImplementation(async (path, ...args) => {
      const result = await original(path, ...args)
      if (path.endsWith('/instances')) return result.map(row => row.state === 'running' ? { ...row, health: 'none' } : row)
      return result
    })
    const { state } = await loadCompose()
    expect(state.services[0].health).toBe('not-configured')
    expect(state.instances.filter(row => row.state === 'RUNNING').every(row => row.health === 'not-configured')).toBe(true)
  })
  it('does not mistake a Ready process for confirmed NGINX configuration', async () => {
    tables['/gateways'][0].evidence.configuration_state = 'MISMATCH'
    const { state } = await loadCompose()
    expect(state.gateways[0]).toMatchObject({ available: true, status: 'READY' })
    expect(state.domains[0].loaded).toEqual({})
  })
  it('preserves masked secrets, ETags, history and missing observation evidence', async () => {
    const { state, versions } = await loadCompose()
    expect(versions.get('environment:e')).toBe('"rev-7"')
    expect(state.parameters13.scopes['environment:e'].rows.PASSWORD).toMatchObject({ value: '', mask: true, secret: true })
    expect(state.parameters13.scopes['environment:e'].rows.LITERAL.value).toBe('删除 {name}')
    expect(state.instances).toHaveLength(2)
    expect(state.instances[0]).toMatchObject({
      current: true,
      created: '2026-09-10T00:00:00Z',
      updated: '2026-09-14T21:00:00Z',
      stateChanged: '2026-09-14T22:00:00Z',
      containerCreated: '2026-09-14T22:01:00Z',
      started: '2026-09-14T22:02:00Z',
      finished: '2026-09-14T23:00:00Z',
      observed: '2026-09-15T00:01:00Z',
      restartCount: 3,
      exitCode: 0,
      oomKilled: false,
      logs: '',
      previous: null,
      version: null,
    })
    expect(state.instances[1].current).toBe(false)
    expect(state.appDeployments[0]).toMatchObject({ state: 'unknown', version: 3 })
    expect(state.runtimes[0].nodes[0]).toMatchObject({ status: 'Ready', manager: 'Leader', availability: 'Active' })
    expect(state.parameters13.documents.a).toMatchObject({ source: '', unavailable: true })
  })
  it('counts only physical nodes while retaining unassigned workload evidence', async () => {
    const original = allPages.getMockImplementation()
    allPages.mockImplementation(async (path, ...args) => {
      const result = await original(path, ...args)
      if (path === '/runtimes/{id}/cluster/nodes') return [...result, { id: 'unassigned', virtual: true, role: 'unknown' }]
      if (path === '/runtimes/{id}/cluster/nodes/{nodeId}/instances' && args[0].nodeId === 'unassigned') return [{ id: 'pending', node_id: 'unassigned', kind: 'task', state: 'pending', historical: false }]
      return result
    })
    const { state } = await loadCompose()
    expect(state.runtimes[0].nodes.map(n => n.id)).toEqual(['n'])
    expect(state.instances.find(i => i.id === 'pending')).toMatchObject({ node: 'unassigned', state: 'PENDING' })
  })
  it('does not convert unavailable collection into a healthy empty inventory', async () => {
    const original = allPages.getMockImplementation()
    allPages.mockImplementation((path, ...args) => path === '/runtimes/{id}/cluster/nodes' ? Promise.reject({ code: 'snapshot.unavailable' }) : original(path, ...args))
    const { state } = await loadCompose()
    expect(state.runtimes[0]).toMatchObject({ connection: 'online', managerRead: false, nodes: [] })
    expect(state.runtimes[0].gaps[0].reason).toBe('snapshot.unavailable')
    expect(state.services[0].observedVersion).toBe(3)
  })
  it('fails the active variables view when its variable collection is unreadable', async () => {
    request.mockRejectedValue(new Error('variables unavailable'))
    await expect(loadInventory(false, { path: '/projects/p/variables' })).rejects.toThrow('variables unavailable')
  })
})

describe('page-scoped inventory reads', () => {
  it.each(['/infrastructure/ha', '/operations/activity?environment_id=e'])('opens %s without loading unrelated workload details', async path => {
    request.mockRejectedValue(new Error('unrelated details unavailable'))
    await loadInventory(false, { path })
    expect(request).not.toHaveBeenCalled()
    expect(allPages.mock.calls.some(([resource]) => resource.endsWith('/revisions') || resource.endsWith('/instances') || resource.endsWith('/cluster/nodes'))).toBe(false)
  })

  it('refreshes incident, HA and prerequisite evidence after an accepted deployment while retaining unrelated data', async () => {
    const families = ['activity-hub', 'system-ha', 'environment-capabilities']
    for (const family of [...families, 'unsaved-settings']) queryClient.setQueryData([family], { retained: true })
    invalidateInventoryWrite('post', '/environments/{id}/deployments', { id: 'e' })
    await vi.waitFor(() => {
      for (const family of families) expect(queryClient.getQueryState([family]).isInvalidated).toBe(true)
    })
    expect(queryClient.getQueryState(['unsaved-settings']).isInvalidated).toBe(false)
    expect(queryClient.getQueryData(['unsaved-settings'])).toEqual({ retained: true })
  })

  it.each(['/market', '/projects', '/projects/p/environments/e/domains'])('loads fresh manager evidence when opening the wizard from %s', async path => {
    tables['/applications'] = []; tables['/services'] = []
    const { state } = await loadInventory(false, { path, action: 'install', projectID: 'p', environmentID: 'e', fresh: true })
    expect(state.runtimes[0]).toMatchObject({ connection: 'online', canWrite: true, managerRead: true })
    expect(allPages.mock.calls.filter(([path]) => path === '/runtimes/{id}/cluster/nodes').map(([, params]) => params.id)).toEqual(['rt'])
    expect(request.mock.calls.some(([, path]) => path === '/environments/{id}/variables')).toBe(true)
    expect(allPages.mock.calls.some(([path]) => path.endsWith('/revisions'))).toBe(false)
  })

  function addOtherEnvironment() {
    tables['/applications'].push({ id: 'foreign-app', runtime_id: 'foreign-runtime', managed: true, environment: { id: 'foreign-env', project_id: 'other' } })
    tables['/services'].push({ id: 'foreign-service', application_id: 'foreign-app', runtime_id: 'foreign-runtime', healthy_instances: 1, environment: { id: 'foreign-env', project_id: 'other' } })
    tables['/runtimes'].push({ id: 'foreign-runtime', runtime_type: 'docker', enabled: true, status: 'ONLINE' })
    const original = allPages.getMockImplementation()
    allPages.mockImplementation((path, params, ...rest) => path === '/projects/{id}/environments' && params.id === 'other'
      ? Promise.resolve([{ id: 'foreign-env', project_id: 'other', application_id: 'foreign-app', runtime_id: 'foreign-runtime' }])
      : original(path, params, ...rest))
  }

  it('loads the project directory without reading variables, Compose, histories or instance details', async () => {
    request.mockRejectedValue(new Error('unrelated details unavailable'))
    const { state } = await loadInventory()
    expect(state.projects).toHaveLength(3)
    expect(state.services[0]).toMatchObject({ id: 's', project: 'p', env: 'e' })
    expect(state.domains[0]).toMatchObject({ id: 'domain', project: 'p', env: 'e' })
    expect(request).not.toHaveBeenCalled()
    expect(allPages.mock.calls.map(([path]) => path)).toHaveLength(13)
    expect(allPages.mock.calls.some(([path]) => path.endsWith('/revisions') || path.endsWith('/instances'))).toBe(false)
    expect(state.runtimes[0].inventoryComplete).toBe(false)
  })

  it('reads only the selected Compose application and its project/environment details', async () => {
    addOtherEnvironment()
    const { state, versions } = await loadCompose()
    expect(versions.get('environment:e')).toBe('"rev-7"')
    expect(state.parameters13.scopes['environment:foreign-env']).toBeUndefined()
    expect(state.apps.find(app => app.id === 'foreign-app')).toMatchObject({ project: 'other', env: 'foreign-env', revisions: [] })
    expect(state.runtimes.find(runtime => runtime.id === 'foreign-runtime').inventoryComplete).toBe(false)
    expect(request.mock.calls.some(([, , options]) => ['foreign-app', 'foreign-env', 'other', 'foreign-runtime'].includes(options.params.path.id))).toBe(false)
    expect(allPages.mock.calls.filter(([path]) => path.endsWith('/revisions')).map(([, params]) => params.id)).toEqual(['a'])
  })

  it('fetches another preview environment only when it belongs to the Compose project', async () => {
    addOtherEnvironment()
    const original = allPages.getMockImplementation()
    allPages.mockImplementation(async (path, params, ...rest) => {
      const result = await original(path, params, ...rest)
      return path === '/projects/{id}/environments' && params.id === 'p' ? [...result, { id: 'preview-env', project_id: 'p', runtime_id: 'rt' }] : result
    })
    const preview = await loadInventory(false, { path: '/projects/p/environments/e/applications/a/compose?previewEnv=preview-env' })
    expect(preview.versions.get('environment:preview-env')).toBe('"rev-7"')
    const foreign = await loadInventory(false, { path: '/projects/p/environments/e/applications/a/compose?previewEnv=foreign-env' })
    expect(foreign.versions.has('environment:foreign-env')).toBe(false)
    expect(request.mock.calls.some(([, , options]) => options.params.path.id === 'foreign-env')).toBe(false)
  })

  it('refreshes only a saved variable set even when unrelated cached directory reads have aged', async () => {
    const path = '/projects/p/environments/e/variables'
    await loadInventory(false, { path })
    allPages.mockClear(); request.mockClear()
    vi.useFakeTimers()
    try {
      vi.setSystemTime(Date.now() + 60000)
      invalidateInventoryWrite('put', '/environments/{id}/variables', { id: 'e' })
      request.mockResolvedValue({ data: { revision: 8, values: [{ key: 'LITERAL', value: 'saved value' }] }, response: new Response(null, { headers: { ETag: '"rev-8"' } }) })
      const { state, versions } = await loadInventory(false, { path, reuseCached: true })
      expect(allPages).not.toHaveBeenCalled()
      expect(request).toHaveBeenCalledTimes(1)
      expect(request.mock.calls[0].slice(0, 2)).toEqual(['get', '/environments/{id}/variables'])
      expect(state.parameters13.scopes['environment:e'].rows.LITERAL.value).toBe('saved value')
      expect(state.parameters13.scopes['project:p'].rows.LITERAL.value).toBe('删除 {name}')
      expect(versions.get('environment:e')).toBe('"rev-8"')
    } finally { queryClient.clear(); vi.useRealTimers() }
  })

  it('prevents a read started before a write from replacing the saved resource cache', async () => {
    const path = '/projects/p/environments/e/variables'
    await loadInventory(false, { path })
    // Simulate a stale navigation read still waiting for the old server snapshot.
    queryClient.invalidateQueries({ queryKey: ['workspace-read', '/environments/{id}/variables'], refetchType: 'none' })
    let finish, signal
    request.mockImplementationOnce((_method, _path, options) => {
      signal = options.signal
      return new Promise(resolve => { finish = resolve })
    })
    const old = loadInventory(false, { path })
    const cancelled = expect(old).rejects.toMatchObject({ name: 'AbortError' })
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'))
    invalidateInventoryWrite('put', '/environments/{id}/variables', { id: 'e' })
    expect(signal.aborted).toBe(true)
    request.mockResolvedValue({ data: { revision: 8, values: [{ key: 'LITERAL', value: 'saved' }] }, response: new Response(null, { headers: { ETag: '"rev-8"' } }) })
    const saved = await loadInventory(false, { path, reuseCached: true })
    finish({ data: { revision: 7, values: [{ key: 'LITERAL', value: 'old' }] }, response: new Response() })
    await cancelled
    const cached = await loadInventory(false, { path, reuseCached: true })
    expect(saved.state.parameters13.scopes['environment:e'].rows.LITERAL.value).toBe('saved')
    expect(cached.state.parameters13.scopes['environment:e'].rows.LITERAL.value).toBe('saved')
  })

  it('loads wizard catalog and variables for its explicitly selected environment', async () => {
    addOtherEnvironment()
    const { versions } = await loadInventory(false, { path: '/market', action: 'install', projectID: 'other', environmentID: 'foreign-env' })
    expect(versions.has('environment:foreign-env')).toBe(true)
    expect(versions.has('environment:e')).toBe(false)
    expect(versions.has('project:p')).toBe(false)
    expect(allPages.mock.calls.some(([path]) => path === '/catalog/apps')).toBe(true)
  })

  it('loads service aliases and runtime node deep links without reading other owners', async () => {
    addOtherEnvironment()
    const alias = await loadInventory(false, { path: '/services/s' })
    expect(alias.state.services.find(s => s.id === 's').specVersion).toBe(3)
    expect(request.mock.calls.some(([, , options]) => options.params.path.id === 'foreign-runtime')).toBe(false)
    queryClient.clear(); request.mockClear(); allPages.mockClear()
    const node = await loadInventory(false, { path: '/infrastructure/runtimes/foreign-runtime/nodes/node' })
    expect(node.state.runtimes.find(r => r.id === 'foreign-runtime').nodes).toHaveLength(1)
    expect(request.mock.calls.map(([, , options]) => options.params.path.id)).toEqual(['foreign-runtime'])
    expect(allPages.mock.calls.some(([path]) => path.endsWith('/revisions'))).toBe(false)
  })

  it('scopes operation details by ownership and leaves foreign Compose sources unavailable', async () => {
    addOtherEnvironment()
    const operation = await loadInventory(false, { path: '/operations/operation' })
    expect(operation.state.appDeployments[0].version).toBe(3)
    expect(allPages.mock.calls.filter(([path]) => path.endsWith('/revisions')).map(([, params]) => params.id)).toEqual(['a'])
    queryClient.clear(); request.mockClear(); allPages.mockClear()
    const invalid = await loadInventory(false, { path: '/projects/p/environments/e/applications/foreign-app/compose' })
    expect(invalid.state.parameters13.documents['foreign-app'].unavailable).toBe(true)
    expect(request.mock.calls.some(([, , options]) => options.params.path.id === 'foreign-app')).toBe(false)
    expect(allPages.mock.calls.some(([path]) => path.endsWith('/revisions'))).toBe(false)
  })

  it('keeps global service browsing available while project variables read only their layer', async () => {
    addOtherEnvironment()
    const global = await loadInventory(false, { path: '/projects/all/environments/all/services' })
    expect(global.state.services.find(s => s.id === 'foreign-service').specVersion).toBeNull()
    expect(global.state.apps.find(a => a.id === 'foreign-app').revisions).toHaveLength(1)
    expect(allPages.mock.calls.filter(([path]) => path.endsWith('/revisions')).map(([, params]) => params.id)).toEqual(['a', 'foreign-app'])
    queryClient.clear(); request.mockClear(); allPages.mockClear()
    const variables = await loadInventory(false, { path: '/projects/p/variables' })
    expect(Object.keys(variables.state.parameters13.scopes)).toEqual(['project:p'])
    expect(request.mock.calls.map(([, path]) => path)).toEqual(['/projects/{id}/variables'])
  })

  it('preserves published domain ownership and configuration even without a bound route', async () => {
    tables['/routes'] = []
    const original = allPages.getMockImplementation()
    allPages.mockImplementation((path, params, ...rest) => path === '/environments/{id}/domain-drafts'
      ? Promise.resolve([{ status: 'published', published_domain_id: 'domain', environment_id: params.id }])
      : original(path, params, ...rest))
    request.mockResolvedValue({ data: { active: { snippets: [{ scope: 'server', resource_id: 'domain', content: 'add_header X-Owner preserved;' }] } } })
    const { state } = await loadInventory(false, { path: '/projects/p/environments/e/domains' })
    expect(state.domains.find(d => d.id === 'domain')).toMatchObject({ project: 'p', env: 'e', active: { serverSnippet: 'add_header X-Owner preserved;' } })
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('reuses Compose resources after previews and rereads workload state after deployment', async () => {
    const path = '/projects/p/environments/e/applications'
    const original = request.getMockImplementation()
    request.mockImplementation((method, path, options) => path.startsWith('/applications/')
      ? Promise.resolve({ data: { compose_yaml: 'services: {}', revision: 0 }, response: new Response() })
      : original(method, path, options))
    await loadInventory(false, { path })
    allPages.mockClear(); request.mockClear()
    invalidateInventoryWrite('post', '/environments/{id}/compose-preview', { id: 'e' })
    invalidateInventoryWrite('post', '/environments/{id}/compose-validation', { id: 'e' })
    invalidateInventoryWrite('post', '/services/{id}/revisions/{revisionID}/restore-preview', { id: 's' })
    await loadInventory(false, { path, reuseCached: true })
    expect(allPages).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
    invalidateInventoryWrite('post', '/environments/{id}/deployments', { id: 'e' })
    await loadInventory(false, { path, reuseCached: true })
    expect(allPages.mock.calls.some(([path]) => path === '/applications')).toBe(true)
    expect(request.mock.calls.some(([, path]) => path === '/environments/{id}/deployment-draft')).toBe(true)
    expect(allPages.mock.calls.some(([path]) => path === '/projects')).toBe(false)
  })

  it('keeps stopped services and explicitly requested removed-service history visible', async () => {
    tables['/services'][0].desired_instances = 0
    tables['/services'][0].running_instances = 0
    const current = await loadInventory(false, { path: '/projects/p/environments/e/services' })
    expect(current.state.services[0].desired).toBe(0)
    const original = allPages.getMockImplementation()
    allPages.mockImplementation((path, params, signal, query) => path === '/services' && query.include_removed
      ? Promise.resolve([...tables[path], { id: 'removed', status: 'REMOVED', application_id: 'a', runtime_id: 'rt' }])
      : original(path, params, signal, query))
    const history = await loadInventory(true, { path: '/projects/p/environments/e/services?include_removed=true' })
    expect(history.state.services.find(service => service.id === 'removed')).toMatchObject({ status: 'REMOVED', project: 'p', env: 'e' })
  })
})

it('keeps Docker Task metadata and Container lifecycle labels distinct', () => {
  setLocale('en')
  const task = instanceTimeRows({
    kind: 'task', created: '2026-09-10T00:00:00Z', updated: '2026-09-14T21:00:00Z', stateChanged: '2026-09-14T22:00:00Z',
    containerCreated: '2026-09-14T22:01:00Z', started: '2026-09-14T22:02:00Z', observed: '2026-09-15T00:01:00Z',
  })
  expect(task.primary).toContain('Task created')
  expect(task.primary).toContain('Task updated')
  expect(task.secondary).toContain('Container created')
  expect(task.secondary).toContain('Task state changed')

  const container = instanceTimeRows({ kind: 'container', containerCreated: '2026-09-14T22:01:00Z', started: '2026-09-14T22:02:00Z', finished: '2026-09-14T23:00:00Z', observed: '2026-09-15T00:01:00Z' })
  expect(container.primary).toContain('Container created')
  expect(container.primary).toContain('Started at')
  expect(container.secondary).toContain('Finished at')
  expect(container.secondary).toContain('Observed at')
  expect(container.primary + container.secondary).not.toContain('updated')
})

it('derives desired and applied service versions from independent immutable service heads', () => {
  const revisions = [
    { id: 'desired', sequence: 9, configuration_source: { service_revisions: { web: 'web-desired' } } },
    { id: 'active', sequence: 8, configuration_source: { service_revisions: { web: 'web-active' } } },
    { id: 'web-desired', sequence: 7 },
    { id: 'web-active', sequence: 6 },
  ]
  expect(applicationServiceVersions(
    { name: 'fallback', service_key: 'demo_web' },
    { project_name: 'demo', desired_revision_id: 'desired', active_revision_id: 'active' },
    revisions,
  )).toEqual({ desired: 7, applied: 6 })
  expect(applicationServiceVersions(
    { name: 'web', service_key: 'demo_web' },
    { project_name: 'demo', desired_revision_id: 'desired' },
    revisions,
  )).toEqual({ desired: 7, applied: null })
})

it('labels Docker Service metadata separately from discovery and removal evidence', () => {
  setLocale('en')
  const swarm = serviceLifecycle({ mode: 'stack', status: 'REMOVED', runtimeCreatedAt: '2026-09-10T00:00:00Z', runtimeUpdatedAt: '2026-09-11T00:00:00Z', removedAt: '2026-09-12T00:00:00Z' })
  expect(swarm.detail).toContain('Service created')
  expect(swarm.detail).toContain('Service updated')
  expect(swarm.detail).toContain('Removal confirmed')
  const compose = serviceLifecycle({ mode: 'compose', recordCreatedAt: '2026-09-10T00:00:00Z', recordUpdatedAt: '2026-09-11T00:00:00Z' })
  expect(compose.detail).toContain('First observed')
  expect(compose.detail).toContain('Record updated')
  expect(compose.detail).not.toContain('Service created')
})

it('uses explicit service references and Runtime ownership for images, application membership and counts', async () => {
  const original = allPages.getMockImplementation()
  tables['/runtimes'].push({ id: 'docker', runtime_type: 'docker', status: 'ONLINE', enabled: true })
  tables['/services'][0].external_id = 'shared-name'
  allPages.mockImplementation((path, params, ...rest) => {
    if (path.endsWith('/instances')) return Promise.resolve(params.id === 'rt' ? [
      { id: 'task', kind: 'task', state: 'running', service_id: 's', container_id: 'physical', image: 'owned-image', historical: false },
      { id: 'unowned', kind: 'task', state: 'running', workload_id: 'shared-name', image: 'unowned-image', historical: false },
    ] : [{ id: 'container', kind: 'container', state: 'running', service_id: 's', container_id: 'physical', image: 'foreign-image', historical: false }])
    return original(path, params, ...rest)
  })
  const { state } = await loadInventory(false, { path: '/infrastructure/runtimes' })
  expect(state.services[0].image).toBe('owned-image')
  expect(state.instances.find(i => i.id === 'unowned').service).toBe('')
  expect(state.instances.find(i => i.id === 'container')).toMatchObject({ runtime: 'docker', service: 's', serviceRuntime: 'rt', app: '', containerId: 'physical' })
  expect(state.instances.find(i => i.id === 'task')).toMatchObject({ runtime: 'rt', service: 's', app: 'a', containerId: 'physical' })
})


it('reads installation parameters from the catalog list manifest without detail metadata', async () => {
  const variables = [{ key: 'PASSWORD', secret: true, required: true, min_length: 16 }, { key: 'DATABASE', default: 'app' }]
  tables['/catalog/apps'] = [{ id: 'catalog', app_key: 'redis', name: 'Redis', services: ['redis'], compose_compatible: true, swarm_compatible: true, compose_yaml: 'services: {}', swarm_yaml: 'services: {}', manifest: JSON.stringify({ variables }) }]
  const { templates } = await loadInventory(false, { path: '/market' })
  expect(templates[0].catalogVariables).toEqual(variables)
  expect(templates[0].swarmRaw).toBe('services: {}')
})

describe('domain route dependency observations', () => {
  function dependencies() {
    tables['/runtimes'][0].pool_id = 'pool'
    tables['/services'].push({ id: 'sandbox', runtime_id: 'rt', environment: { id: 'sandbox-env' }, name: 'Validation backend', dns_status: 'READY' })
    tables['/services'].push({ id: 'unrelated', runtime_id: 'rt', environment: { id: 'sandbox-env' }, name: 'Unrelated backend' })
    const original = allPages.getMockImplementation()
    allPages.mockImplementation(async (path, params, ...rest) => path === '/projects/{id}/environments' && params.id === 'p' ? [
      { id: 'e', project_id: 'p', name: 'prod', runtime_id: 'rt', application_id: 'a' },
      { id: 'sandbox-env', project_id: 'p', name: 'sandbox', runtime_id: 'rt' },
    ] : original(path, params, ...rest))
  }
  it('loads cross-environment active and draft targets without loading unrelated instances or sources', async () => {
    dependencies()
    tables['/routes'].push({ id: 'validation', domain_id: 'domain', kind: 'proxy', path: '/__nsc_validation/', enabled: true, backend: { service_id: 'sandbox' } })
    request.mockResolvedValue({ data: { draft: { revision: 2, routes: [{ route: { id: 'draft', domain_id: 'domain', enabled: true, backend: { service_id: 'outside' } } }] } } })
    const { state } = await loadInventory(false, { path: '/projects/p/environments/e/domains/domain?tab=routes' })
    const requested = allPages.mock.calls.filter(([path]) => path === '/services/{id}/instances').map(([, params]) => params.id)
    expect(requested).toEqual(expect.arrayContaining(['s', 'sandbox', 'outside']))
    expect(requested).not.toContain('unrelated')
    expect(state.services.find(s => s.id === 'sandbox')).toMatchObject({ poolId: 'pool', endpointObservation: { state: 'loaded' }, endpoints: [['10.0.2.113', true]] })
    expect(request.mock.calls.some(([, path]) => path.startsWith('/applications/'))).toBe(false)
  })
  it('keeps failed and unrequested reads unknown while preserving successful empty observations', async () => {
    dependencies()
    tables['/routes'].push({ id: 'validation', domain_id: 'domain', kind: 'proxy', enabled: true, backend: { service_id: 'sandbox' } })
    const original = allPages.getMockImplementation()
    allPages.mockImplementation((path, params, ...rest) => {
      if (path === '/services/{id}/instances') return params.id === 'sandbox' ? Promise.reject(new Error('unavailable')) : Promise.resolve([])
      return original(path, params, ...rest)
    })
    const { state } = await loadInventory(false, { path: '/projects/p/environments/e/domains/domain' })
    expect(state.services.find(s => s.id === 's')).toMatchObject({ endpointObservation: { state: 'loaded' }, endpoints: [] })
    expect(state.services.find(s => s.id === 'sandbox')).toMatchObject({ endpointObservation: { state: 'error' }, endpoints: [] })
    expect(state.services.find(s => s.id === 'unrelated').endpointObservation.state).toBe('not-loaded')
  })
  it('loads unsaved internal targets explicitly and marks offline observations stale', async () => {
    dependencies(); tables['/runtimes'][0].status = 'OFFLINE'
    const { state } = await loadInventory(false, { path: '/projects/p/environments/e/domains/domain', endpointServiceIDs: ['sandbox', 'missing'], fresh: true })
    expect(state.services.find(s => s.id === 'sandbox').endpointObservation.state).toBe('stale')
    expect(allPages.mock.calls.some(([path, params]) => path === '/services/{id}/instances' && params.id === 'missing')).toBe(false)
  })
})

it('preserves the actual instance fetch time when reusing a cached observation', async () => {
  const path = '/projects/p/environments/e/domains/domain'
  const first = await loadInventory(false, { path, fresh: true })
  const observed = first.state.services.find(service => service.id === 's').endpointObservation.fetchedAt
  const clock = vi.spyOn(Date, 'now').mockReturnValue(observed + 60_000)
  try {
    const cached = await loadInventory(false, { path, reuseCached: true })
    expect(cached.state.services.find(service => service.id === 's').endpointObservation.fetchedAt).toBe(observed)
  } finally { clock.mockRestore() }
})
