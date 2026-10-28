import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { initializeData } from '../../src/data/api'
import { loadInventory, queryClient } from '../../src/data/inventory'
import * as client from '../../src/shared/api/client'
import { i18n, setLocale } from '../../src/shared/i18n'
import { refreshFailure } from '../../src/data/resource-changes'
vi.mock('../../src/data/inventory', () => ({ loadInventory: vi.fn(), queryClient: { fetchQuery: ({ queryFn }) => queryFn(), clear: vi.fn() } }))
let data, context, inventory, request
beforeEach(async () => {
  setLocale('en')
  const state = { project: 'p', env: 'e', view: 'cloud', serviceLayout: 'grouped', projects: [{ id: 'p' }], environments: [{ id: 'e', project: 'p', name: 'dev' }], apps: [], services: [], runtimes: [], parameters13: { scopes: {}, documents: {} } }
  inventory = { state, versions: new Map([['environment:e', '"rev-4"']]), templates: [], raw: { environments: [{ id: 'e', project_id: 'p', runtime_id: 'rt', name: 'dev', project_name: 'project-dev' }], services: [] } }
  loadInventory.mockImplementation(async () => structuredClone(inventory))
  context = { ENV: {}, workspace: { varDraft: { key: 'environment:e', base: 4, rows: { LABEL: { value: '删除 {name}', type: 'string', note: '' }, PASSWORD: { value: '', mask: true, type: 'credential', note: '' } } }, varsDirty: () => !!context.workspace.varDraft }, workload: { x: {}, renderWizard: vi.fn(), collectWizard: vi.fn() }, formError: vi.fn(), render: vi.fn(), toast: vi.fn(), closeModal: vi.fn() }
  request = vi.spyOn(client, 'request').mockResolvedValue({ data: {}, response: new Response() })
  context.ui = {}
  data = await initializeData(); data.attach(context)
})
afterEach(() => { data.dispose(); document.body.innerHTML = '' })
describe('asynchronous writes behind the original controller actions', () => {
  it('refreshes wizard entry and advancement without reusing cached manager evidence', async () => {
    document.body.innerHTML = '<button data-action="x-install">Install</button><button data-action="x-wnext">Next</button>'
    loadInventory.mockClear()
    document.querySelector('[data-action=x-install]').click()
    await vi.waitFor(() => expect(loadInventory).toHaveBeenCalledWith(false, expect.objectContaining({ action: 'install', projectID: 'p', environmentID: 'e', fresh: true, reuseCached: false })))
    const wizard = { step: 2, project: 'p', env: 'e', runtime: 'rt', runtimeEnv: 'USER=unsaved' }
    context.workload.x.wizard = wizard
    loadInventory.mockClear()
    document.querySelector('[data-action=x-wnext]').click()
    await vi.waitFor(() => expect(loadInventory).toHaveBeenCalledWith(false, expect.objectContaining({ action: 'install', fresh: true, reuseCached: false })))
    expect(context.workload.x.wizard).toBe(wizard)
    expect(wizard.runtimeEnv).toBe('USER=unsaved')
  })

  it('discards an older target read and refreshes the selected environment while preserving inputs', async () => {
    const wizard = { step: 2, project: 'p', env: 'e', runtime: 'rt', name: 'Unsaved application', catalogValues: { USER: 'unsaved user' } }
    context.workload.x.wizard = wizard
    document.body.innerHTML = '<select data-wf="env"><option value="e">First</option><option value="next">Next</option></select>'
    let finish
    loadInventory.mockClear()
    loadInventory.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    document.querySelector('select').dispatchEvent(new Event('change', { bubbles: true }))
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'))
    wizard.env = 'next'
    document.querySelector('select').dispatchEvent(new Event('change', { bubbles: true }))
    const old = structuredClone(inventory)
    old.state.runtimes = [{ id: 'obsolete', managerRead: false }]
    finish(old)
    await vi.waitFor(() => expect(loadInventory).toHaveBeenCalledTimes(2))
    expect(loadInventory.mock.lastCall[1]).toMatchObject({ action: 'install', environmentID: 'next', fresh: true })
    expect(data.state.runtimes.some(r => r.id === 'obsolete')).toBe(false)
    expect(context.workload.x.wizard).toBe(wizard)
    expect(wizard.catalogValues.USER).toBe('unsaved user')
  })

  it('cancels partial bootstrap reads and clears their cache when initialization fails', async () => {
    data.dispose()
    queryClient.clear.mockClear()
    let signal
    loadInventory.mockImplementationOnce((_history, options) => { signal = options.signal; throw new client.ApiRequestError(401, 'auth.required') })
    await expect(initializeData('/projects/p/environments/e/applications')).rejects.toMatchObject({ status: 401 })
    expect(signal.aborted).toBe(true)
    expect(queryClient.clear).toHaveBeenCalledOnce()
  })
  it.each([0, 2])('previews only the current domain snippets with pool draft revision %i and no write', async revision => {
    const active = { snippets: [{ scope: 'http', content: 'gzip on;' }, { scope: 'server', resource_id: 'other', content: 'add_header X-Other yes;' }, { scope: 'server', resource_id: 'domain', content: 'add_header X-Old yes;' }] }
    const draft = { revision, snippets: [{ scope: 'http', content: 'gzip off;' }, { scope: 'server', resource_id: 'domain', content: 'add_header X-Draft yes;' }] }
    request.mockImplementation(async (method, path) => ({ data: path.endsWith('/preview') ? { candidate: {} } : { active, draft }, response: new Response() }))
    await data.previewDomainConfig({ poolID: 'pool', domainID: 'domain', serverSnippet: 'add_header X-New yes;', routes: [{ id: 'route', snippet: 'expires 1h;' }] })
    expect(request).toHaveBeenNthCalledWith(1, 'get', '/gateway-pools/{id}/nginx', expect.objectContaining({ params: { path: { id: 'pool' } } }))
    expect(request).toHaveBeenNthCalledWith(2, 'post', '/gateway-pools/{id}/nginx/preview', expect.objectContaining({ body: {
      domain_id: 'domain', access_controls: [], snippets: [
        ...(revision ? [{ scope: 'http', content: 'gzip off;' }] : active.snippets.slice(0, 2)),
        { scope: 'server', resource_id: 'domain', content: 'add_header X-New yes;' },
        { scope: 'location', resource_id: 'route', content: 'expires 1h;' },
      ],
    } }))
    expect(request.mock.calls.some(call => ['put', 'delete'].includes(call[0]))).toBe(false)
  })
  it.each(['operation', 'compose', 'modal'])('updates live operation evidence without replacing an active %s editor', async page => {
    data.dispose()
    inventory.state.appDeployments = [{ id: 'op', status: 'RUNNING', project: 'p', env: 'e', version: 4 }]
    data = await initializeData()
    context.ui.page = page === 'modal' ? 'operation' : page
    context.modal = page === 'modal' ? { kind: 'editor' } : null
    context.workspace.composeWork = 'unsaved source'
    request.mockResolvedValue({ data: { id: 'op', application_id: 'a', status: 'RUNNING', log: 'real evidence', attempt: 2 } })
    data.attach(context)
    await vi.waitFor(() => expect(data.state.appDeployments[0].attempts[0].logs).toEqual(['real evidence']))
    expect(data.state.appDeployments[0]).toMatchObject({ project: 'p', env: 'e', version: 4, status: 'RUNNING', state: 'running' })
    expect(context.workspace.composeWork).toBe('unsaved source')
    expect(context.render).toHaveBeenCalledTimes(page === 'operation' ? 1 : 0)
  })
  it('saves Compose and runtime.env without deploying or clearing newer edits', async () => {
    data.dispose()
    inventory.state.apps = [{ id: 'a', revision: 8 }]
    inventory.state.parameters13.documents.a = { source: 'active', runtime: '', draft: null, unavailable: false }
    inventory.versions.set('application-draft:a', '"rev-0"')
    data = await initializeData(); data.attach(context)
    Object.assign(context.workspace, { docApp: 'a', composeWork: 'saved source', runtimeWork: 'MODE=test', docsDirty: () => true })
    let finish
    request.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    data.command('saveDoc', [])
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1))
    expect(request).toHaveBeenCalledWith('put', '/applications/{id}/draft', expect.objectContaining({ body: { application_revision: 8, compose_yaml: 'saved source', runtime_env: 'MODE=test' }, headers: expect.objectContaining({ 'If-Match': '"rev-0"' }) }))
    context.workspace.composeWork = 'newer unsaved source'
    inventory.state.parameters13.documents.a.draft = { source: 'saved source', runtime: 'MODE=test' }
    finish({ data: { revision: 1, application_revision: 8, compose_yaml: 'saved source', runtime_env: 'MODE=test' } })
    await vi.waitFor(() => expect(context.toast).toHaveBeenCalled())
    expect(data.state.parameters13.documents.a.draft.source).toBe('saved source')
    expect(context.workspace.composeWork).toBe('newer unsaved source')
    expect(request).toHaveBeenCalledTimes(1)
  })
  it('retains source input and editor state after draft conflicts', async () => {
    data.state.apps = [{ id: 'a', revision: 8 }]
    data.state.parameters13.documents.a = { source: 'active', unavailable: false }
    Object.assign(context.workspace, { docApp: 'a', composeWork: 'unsaved', runtimeWork: 'KEY=value', docsDirty: () => true })
    request.mockRejectedValue(new client.ApiRequestError(412, 'conflict'))
    data.command('saveDoc', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalled())
    expect(context.workspace).toMatchObject({ docApp: 'a', composeWork: 'unsaved', runtimeWork: 'KEY=value' })
    expect(context.closeModal).not.toHaveBeenCalled()
  })
  it.each(['runtime-id', ''])('submits the chosen project runtime %s while preserving server automatic selection', async runtime => {
    document.body.innerHTML = '<input id="p-name" value="Literal &lt;project&gt;"><input id="p-key" value="runtime-project"><textarea id="p-description">Retained description</textarea><select id="p-runtime"><option value=""></option><option value="runtime-id">Named runtime</option></select>'
    document.querySelector('#p-runtime').value = runtime
    context.selectScope = vi.fn(); context.updateHash = vi.fn()
    request.mockResolvedValue({ data: { id: 'new-project', initial_environment_id: 'initial-env' }, response: new Response() })
    data.command('createProject', [])
    await vi.waitFor(() => expect(context.closeModal).toHaveBeenCalledWith(true))
    expect(request.mock.calls[0][2].body).toEqual({ name: 'Literal <project>', slug: 'runtime-project', description: 'Retained description', ...(runtime ? { default_runtime_id: runtime } : {}), initial_environment: { name: 'production', kind: 'production' } })
    expect(request.mock.calls[0][2].headers['Idempotency-Key']).toBeTruthy()
    expect(context.selectScope).toHaveBeenCalledWith('new-project', 'initial-env')
  })
  it('focuses the project runtime error, retains all inputs and lets the same modal retry with a chosen ID', async () => {
    document.body.innerHTML = '<input id="p-name" value="Project"><input id="p-key" value="runtime-project"><textarea id="p-description">Unchanged text</textarea><select id="p-runtime"><option value=""></option><option value="second-runtime">Swarm</option></select>'
    request.mockRejectedValue(new client.ApiRequestError(400, 'validation.project_runtime', {}, [{ pointer: '/default_runtime_id', code: 'validation.project_runtime' }]))
    data.command('createProject', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalled())
    expect(document.activeElement).toBe(document.querySelector('#p-runtime'))
    expect(document.activeElement.getAttribute('aria-invalid')).toBe('true')
    expect(document.querySelector('#p-description').value).toBe('Unchanged text')
    expect(context.closeModal).not.toHaveBeenCalled()
    const previousKey = request.mock.calls[0][2].headers['Idempotency-Key']
    document.querySelector('#p-runtime').value = 'second-runtime'
    context.selectScope = vi.fn(); context.updateHash = vi.fn()
    request.mockResolvedValue({ data: { id: 'project', initial_environment_id: 'env' }, response: new Response() })
    data.command('createProject', [])
    await vi.waitFor(() => expect(context.closeModal).toHaveBeenCalledWith(true))
    expect(request.mock.calls[1][2].body.default_runtime_id).toBe('second-runtime')
    expect(request.mock.calls[1][2].headers['Idempotency-Key']).not.toBe(previousKey)
  })
  it('locates backend field errors in the original environment form without losing input', async () => {
    document.body.innerHTML = '<input id="p-env-name" value="qa"><input id="p-env-slug" value="qa"><select id="p-env-kind"><option selected>custom</option></select>'
    request.mockRejectedValue(new client.ApiRequestError(422, 'validation.failed', {}, [{ pointer: '/name', code: 'validation.failed' }]))
    data.command('saveEnvironment', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalled())
    expect(document.activeElement).toBe(document.querySelector('#p-env-name'))
    expect(document.activeElement.getAttribute('aria-invalid')).toBe('true')
    expect(document.querySelector('#p-env-name').value).toBe('qa')
    expect(context.closeModal).not.toHaveBeenCalled()
  })
  it('preserves the original required-name and identifier validation before an API submission', async () => {
    document.body.innerHTML = '<input id="p-name"><input id="p-key" value="invalid key">'
    data.command('createProject', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalledWith(i18n.global.t('archive.message_2f634dd0ccf6')))
    document.querySelector('#p-name').value = 'Retained project'
    data.command('createProject', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalledWith(i18n.global.t('archive.message_c4241b5e5a18')))
    expect(document.querySelector('#p-name').value).toBe('Retained project')
    expect(request).not.toHaveBeenCalled()
    expect(context.closeModal).not.toHaveBeenCalled()
  })
  it('saves masked and literal values with the loaded ETag, then rereads persisted state', async () => {
    request.mockImplementation(async (_method, _path, options) => {
      inventory.state.parameters13.scopes['environment:e'] = { version: 5, rows: options.body.values }
      return { data: { revision: 5 }, response: new Response() }
    })
    expect(data.command('saveVars', [])).toBe(false)
    await vi.waitFor(() => expect(context.workspace.varDraft).toBeNull())
    expect(request).toHaveBeenCalledWith('put', '/environments/{id}/variables', expect.objectContaining({ params: { path: { id: 'e' } }, headers: expect.objectContaining({ 'If-Match': '"rev-4"', 'Idempotency-Key': expect.any(String) }), body: { values: [{ key: 'LABEL', value: '删除 {name}', secret: false, mask: false }, { key: 'PASSWORD', secret: true, mask: true }] } }))
    expect(data.state.parameters13.scopes['environment:e'].version).toBe(5)
    expect(context.formError).not.toHaveBeenCalled()
  })
  it('retains input on conflict and retains the idempotency identity for unchanged retries', async () => {
    const draft = context.workspace.varDraft
    request.mockRejectedValue(new client.ApiRequestError(412, 'conflict'))
    data.command('saveVars', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalledTimes(1))
    expect(context.workspace.varDraft).toBe(draft)
    expect(draft.rows.LABEL.value).toBe('删除 {name}')
    data.command('saveVars', [])
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2))
    expect(request.mock.calls[0][2].headers['Idempotency-Key']).toBe(request.mock.calls[1][2].headers['Idempotency-Key'])
    expect(context.toast).not.toHaveBeenCalled()
  })
  it('does not erase edits made while an earlier save is pending', async () => {
    let finish
    request.mockReturnValue(new Promise(resolve => { finish = resolve }))
    data.command('saveVars', [])
    await vi.waitFor(() => expect(request).toHaveBeenCalledOnce())
    context.workspace.varDraft.rows.LABEL.value = 'newer input'
    data.command('saveVars', [])
    expect(request).toHaveBeenCalledOnce()
    finish({ data: {}, response: new Response() })
    await vi.waitFor(() => expect(context.render).toHaveBeenCalled())
    expect(context.workspace.varDraft.rows.LABEL.value).toBe('newer input')
  })
  it.each(['application-variable-layer', 'variable-types-and-notes'])('rejects unsupported %s without a network write or lost draft', async (gap) => {
    if (gap === 'application-variable-layer') context.workspace.varDraft.key = 'application:a'
    else context.workspace.varDraft.rows.LABEL.note = 'must not be silently omitted'
    const draft = structuredClone(context.workspace.varDraft)
    data.command('saveVars', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalled())
    expect(context.formError.mock.calls[0][0]).toBe(i18n.global.t('integration.unavailable'))
    expect(context.workspace.varDraft).toEqual(draft)
    expect(request).not.toHaveBeenCalled()
  })
  it('keeps an unsupported original submit in place and reports through its original error region', () => {
    document.body.innerHTML = '<textarea id="native">keep my configuration</textarea>'
    const before = document.body.innerHTML
    expect(data.command('saveConnection', [])).toBe(false)
    expect(context.formError).toHaveBeenCalled()
    expect(context.closeModal).not.toHaveBeenCalled()
    expect(document.body.innerHTML).toBe(before)
    expect(request).not.toHaveBeenCalled()
  })
  it('allows a new service in an occupied environment without renaming its Stack', async () => {
    context.workload.x.wizard = { project: 'p', env: 'e', runtime: 'rt', source: 'file', raw: 'services:\n  web:\n    image: nginx:alpine\n', name: 'different name', key: 'different-key', access: 'internal', step: 3 }
    document.body.innerHTML = '<button data-action="x-wnext">Next</button>'
    document.querySelector('button').click()
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(context.workload.x.wizardError).toBeUndefined()
    expect(context.workload.x.wizard.key).toBe('different-key')
    expect(inventory.raw.environments[0].project_name).toBe('project-dev')
    expect(context.workload.x.wizard.step).toBe(3)
    expect(request).not.toHaveBeenCalled()
  })
  it('releases the action lock after admitting a valid review transition', async () => {
    context.workload.x.wizard = { project: 'p', env: 'e', runtime: 'rt', source: 'file', raw: 'services:\n  web:\n    image: nginx:alpine\n', name: 'dev', key: 'project-dev', access: 'internal', step: 3 }
    document.body.innerHTML = '<button data-action="x-wnext">Next</button>'
    document.querySelector('button').click()
    await new Promise(resolve => setTimeout(resolve, 0))
    context.workload.x.wizard.step = 4
    context.workload.x.wizard.reviewAck = true
    data.command('deployWizard', [])
    await vi.waitFor(() => expect(context.workload.x.wizardError).toBe(i18n.global.t('integration.preflightStale')))
    expect(request).not.toHaveBeenCalled()
  })
  it('does not accept a stale confirmation or missing acknowledgement', async () => {
    context.workload.x.wizard = { project: 'p', env: 'e', runtime: 'rt', source: 'file', raw: 'services:\n  web:\n    image: nginx:alpine\n', name: 'dev', key: 'project-dev', access: 'internal', step: 4, reviewAck: true }
    data.command('deployWizard', [])
    await vi.waitFor(() => expect(context.workload.x.wizardError).toBe(i18n.global.t('integration.preflightStale')))
    expect(request).not.toHaveBeenCalled()
    expect(context.workload.x.wizard.raw).toContain('nginx:alpine')
  })
  it('keeps the newer route snapshot when an older scoped read finishes last', async () => {
    let finish
    loadInventory.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const older = data.prepareRoute('/projects/p/environments/e/services')
    const next = structuredClone(inventory)
    next.state.services = [{ id: 'newer-service' }]
    loadInventory.mockResolvedValueOnce(next)
    expect(await data.prepareRoute('/projects/p/variables')).toBe(true)
    const stale = structuredClone(inventory)
    stale.state.services = [{ id: 'older-service' }]
    finish(stale)
    expect(await older).toBe(false)
    expect(data.state.services).toEqual([{ id: 'newer-service' }])
  })
  it('preserves the active draft and snapshot when the destination cannot be read', async () => {
    const draft = context.workspace.varDraft
    const previous = structuredClone(data.state)
    loadInventory.mockRejectedValueOnce(new client.ApiRequestError(503, 'unavailable'))
    await expect(data.prepareRoute('/projects/p/environments/e/services')).rejects.toMatchObject({ status: 503 })
    expect(data.state).toEqual(previous)
    expect(context.workspace.varDraft).toBe(draft)
  })
  it('cancels scoped reads and ignores their completion after workspace disposal', async () => {
    let finish, signal
    loadInventory.mockImplementationOnce((_history, options) => { signal = options.signal; return new Promise(resolve => { finish = resolve }) })
    const pending = data.prepareRoute('/projects/p/environments/e/services')
    const previous = structuredClone(data.state)
    data.dispose()
    expect(signal.aborted).toBe(true)
    const stale = structuredClone(inventory)
    stale.state.services = [{ id: 'late-service' }]
    finish(stale)
    expect(await pending).toBe(false)
    expect(data.state).toEqual(previous)
  })
  it('merges synchronous resource notifications and rerenders the current service list', async () => {
    context.ui.page = 'services'
    const latest = structuredClone(inventory)
    latest.state.services = [{ id: 'added', name: 'Added service' }]
    loadInventory.mockClear(); loadInventory.mockResolvedValue(latest)
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    await vi.waitFor(() => expect(data.state.services).toEqual(latest.state.services))
    expect(loadInventory).toHaveBeenCalledOnce()
    expect(context.render).toHaveBeenCalledOnce()
  })
  it('repaints resource data without synchronizing or replacing the active route', async () => {
    context.renderData = vi.fn()
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    await vi.waitFor(() => expect(context.renderData).toHaveBeenCalledOnce())
    expect(context.render).not.toHaveBeenCalled()
  })
  it('discards a pre-mutation refresh and loads a trailing batch without publishing old data', async () => {
    let finish
    const newer = structuredClone(inventory)
    newer.state.services = [{ id: 'newest' }]
    loadInventory.mockClear()
    loadInventory.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(newer)
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'))
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    const older = structuredClone(inventory)
    older.state.services = [{ id: 'obsolete' }]
    finish(older)
    await vi.waitFor(() => expect(data.state.services).toEqual([{ id: 'newest' }]))
    expect(loadInventory).toHaveBeenCalledTimes(2)
    expect(context.render).toHaveBeenCalledOnce()
  })
  it('waits for admitted navigation before a background refresh and retains the target scope', async () => {
    let finish
    const newer = structuredClone(inventory)
    newer.state.services = [{ id: 'destination' }]
    loadInventory.mockClear()
    loadInventory.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(newer)
    const navigation = data.prepareRoute('/projects/p/environments/e/services')
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    expect(context.render).not.toHaveBeenCalled()
    finish(newer)
    expect(await navigation).toBe(true)
    await vi.waitFor(() => expect(context.render).toHaveBeenCalled())
    expect(loadInventory.mock.calls.every(([, options]) => options.path === '/projects/p/environments/e/services')).toBe(true)
    expect(data.state.services).toEqual([{ id: 'destination' }])
  })
  it('reads a newer queued mutation when the older refresh fails', async () => {
    let fail
    const latest = structuredClone(inventory)
    latest.state.services = [{ id: 'recovered' }]
    loadInventory.mockClear()
    loadInventory.mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject })).mockResolvedValue(latest)
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    await vi.waitFor(() => expect(fail).toBeTypeOf('function'))
    window.dispatchEvent(new CustomEvent('nsc:resources-changed', { detail: { reason: 'mutation' } }))
    fail(new client.ApiRequestError(503, 'unavailable'))
    await vi.waitFor(() => expect(data.state.services).toEqual([{ id: 'recovered' }]))
    expect(loadInventory).toHaveBeenCalledTimes(2)
    expect(context.render).toHaveBeenCalledOnce()
    expect(refreshFailure.value).toBeUndefined()
  })
  it('refetches the current route and preserves an unsaved variable baseline and ETag', async () => {
    const key = 'environment:e'
    data.state.parameters13.scopes[key] = { version: 4, rows: { LABEL: { value: 'baseline' } } }
    const draft = context.workspace.varDraft
    const newer = structuredClone(inventory)
    newer.state.parameters13.scopes[key] = { version: 9, rows: { LABEL: { value: 'remote edit' } } }
    newer.versions.set(key, '"rev-9"')
    loadInventory.mockClear(); loadInventory.mockResolvedValue(newer)
    expect(await data.refresh()).toBe(true)
    expect(loadInventory.mock.calls[0][1].fresh).toBe(true)
    expect(context.workspace.varDraft).toBe(draft)
    expect(data.state.parameters13.scopes[key].version).toBe(4)
    request.mockRejectedValue(new client.ApiRequestError(412, 'conflict'))
    data.command('saveVars', [])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalled())
    expect(request.mock.calls[0][2].headers['If-Match']).toBe('"rev-4"')
  })
  it('uses the open environment form revision after newer metadata is refreshed', async () => {
    context.modal = { envId: 'e' }
    inventory.raw.environments[0].revision = 9
    await data.refresh()
    request.mockRejectedValue(new client.ApiRequestError(412, 'conflict'))
    data.command('saveEnvironment', [{ name: 'Unsaved title', slug: 'dev', kind: 'custom', gateways: [], revision: 4 }])
    await vi.waitFor(() => expect(context.formError).toHaveBeenCalled())
    expect(request).toHaveBeenCalledWith('patch', '/environments/{id}', expect.objectContaining({ headers: expect.objectContaining({ 'If-Match': '"rev-4"' }) }))
    expect(context.closeModal).not.toHaveBeenCalled()
  })
  it('keeps the revision of variables being edited in an open modal before they are staged', async () => {
    const key = 'project:p'
    data.state.parameters13.scopes[key] = { version: 4, rows: { LABEL: { value: 'baseline' } } }
    context.workspace.varDraft = null; context.workspace.layer = 'project'
    context.modal = { kind: 'y-variable' }
    const latest = structuredClone(inventory)
    latest.state.parameters13.scopes[key] = { version: 9, rows: { LABEL: { value: 'concurrent edit' } } }
    loadInventory.mockResolvedValue(latest)
    await data.refresh()
    expect(data.state.parameters13.scopes[key].version).toBe(4)
    expect(context.modal).toEqual({ kind: 'y-variable' })
  })
  it('updates clean Compose input and its revision after a fresh server read', async () => {
    data.state.apps = [{ id: 'a', revision: 4 }]
    data.state.parameters13.documents.a = { source: 'old source', runtime: 'MODE=old', sourceRevision: 'old-revision' }
    Object.assign(context.workspace, { docApp: 'a', composeWork: 'old source', runtimeWork: 'MODE=old', docsDirty: () => false })
    const latest = structuredClone(inventory)
    latest.state.apps = [{ id: 'a', revision: 9 }]
    latest.state.parameters13.documents.a = { source: 'new source', runtime: 'MODE=new', sourceRevision: 'new-revision' }
    loadInventory.mockResolvedValue(latest)
    await data.refresh()
    expect(context.workspace).toMatchObject({ composeWork: 'new source', runtimeWork: 'MODE=new', documentBaseRevision: 9, sourceBaseRevisionID: 'new-revision' })
  })
  it('refetches the selected service source and draft for a clean Compose editor', async () => {
    data.state.apps = [{ id: 'a', revision: 4 }]
    Object.assign(context.workspace, { docApp: 'a', sourceServiceID: 's', composeWork: 'old source', runtimeWork: '', docsDirty: () => false })
    const latest = structuredClone(inventory)
    latest.state.apps = [{ id: 'a', revision: 9 }]
    latest.state.parameters13.documents.a = { source: 'application source', runtime: '', unavailable: false }
    loadInventory.mockResolvedValue(latest)
    request.mockResolvedValue({ data: { revision: 2, application_revision: 9, revision_id: 'new-revision', compose_yaml: 'services:\n  web:\n    image: nginx:2\n', runtime_env: 'MODE=service' }, response: new Response(null, { headers: { ETag: '"rev-2"' } }) })
    await data.prepareRoute('/projects/p/environments/e/applications/a/compose')
    expect(request.mock.calls.map(call => call[1])).toEqual(['/services/{id}/source', '/services/{id}/draft'])
    expect(context.workspace).toMatchObject({ sourceServiceID: 's', runtimeWork: 'MODE=service', serviceDraftETag: '"rev-2"', sourceBaseRevisionID: 'new-revision' })
    expect(context.workspace.composeWork).toContain('nginx:2')
  })
  it.each([false, true])('updates a clean domain and preserves its unsaved editor when dirty=%s', async dirty => {
    const active = { serverSnippet: 'old snippet', routes: [] }
    data.state.domains = [{ id: 'd', rev: 4, active }]
    context.ui = { page: 'domain', domain: 'd' }
    context.work = { ...active, serverSnippet: dirty ? 'unsaved snippet' : active.serverSnippet }
    context.refreshDomainEditor = vi.fn()
    const latest = structuredClone(inventory)
    latest.state.domains = [{ id: 'd', rev: 9, active: { serverSnippet: 'new snippet', routes: [] } }]
    loadInventory.mockResolvedValue(latest)
    await data.refresh()
    expect(data.state.domains[0].rev).toBe(dirty ? 4 : 9)
    expect(context.refreshDomainEditor).toHaveBeenCalledTimes(dirty ? 0 : 1)
    if (dirty) expect(context.work.serverSnippet).toBe('unsaved snippet')
  })
  it('reports a saved write separately from a failed refresh and retries only reads', async () => {
    const draft = context.workspace.varDraft
    request.mockResolvedValue({ data: { revision: 5 }, response: new Response() })
    loadInventory.mockRejectedValue(new client.ApiRequestError(503, 'unavailable'))
    data.command('saveVars', [])
    await vi.waitFor(() => expect(context.toast).toHaveBeenCalled())
    expect(context.workspace.varDraft).toBeNull()
    expect(draft.base).toBe(5)
    expect(context.formError).not.toHaveBeenCalled()
    expect(refreshFailure.value).toBeInstanceOf(client.ApiRequestError)
    loadInventory.mockResolvedValue(structuredClone(inventory))
    expect(await data.refresh()).toBe(true)
    expect(refreshFailure.value).toBeUndefined()
    expect(request).toHaveBeenCalledOnce()
  })
  it('refreshes service state at operation completion while keeping another editor intact', async () => {
    data.state.appDeployments = [{ id: 'terminal-op', project: 'p', env: 'e', version: 1, status: 'RUNNING' }]
    context.ui.page = 'services'; context.modal = { kind: 'editor' }
    const draft = context.workspace.varDraft
    const latest = structuredClone(inventory)
    latest.state.services = [{ id: 'deployed' }]
    latest.state.appDeployments = [{ id: 'terminal-op', status: 'SUCCEEDED' }]
    loadInventory.mockResolvedValue(latest)
    request.mockResolvedValue({ data: { id: 'terminal-op', application_id: 'a', status: 'SUCCEEDED' } })
    data.attach(context)
    await vi.waitFor(() => expect(data.state.services).toEqual([{ id: 'deployed' }]))
    expect(context.render).toHaveBeenCalled()
    expect(context.workspace.varDraft).toBe(draft)
    expect(context.modal).toEqual({ kind: 'editor' })
  })
})

it('edits IP policy without changing active rules and previews raw ranges read only', async () => {
  const active = { mode: 'managed', serverSnippet: '', routes: [{ id: 'route', path: '/', kind: 'return', match: 'prefix', code: 200, value: 'hello', enabled: true, snippet: '', ipAccess: { access: [], forbidden: [] } }], ipAccess: { access: [], forbidden: [] } }
  data.state.domains = [{ id: 'domain', name: 'example.test', pool_id: 'pool', active }]
  context.ui = { page: 'domain', tab: 'config', domain: 'domain' }
  context.work = structuredClone(active)
  expect(data.ipAccessEditorState().domainName).toBe('example.test')
  data.updateIPAccess('', 'access', ' 10.0.0.1-10.0.0.4 \n\n')
  data.updateIPAccess('route', 'forbidden', '10.0.0.2')
  expect(context.work.ipAccess.access).toEqual([' 10.0.0.1-10.0.0.4 '])
  expect(active.ipAccess.access).toEqual([])
  const source = { header: 'X-Forwarded-For', trusted_proxies: [' 10.0.0.1-10.0.0.9 '] }
  data.updateClientIP(source)
  source.trusted_proxies.push('::1')
  expect(context.work.ipAccess.clientIP.trusted_proxies).toEqual([' 10.0.0.1-10.0.0.9 '])
  expect(active.ipAccess.clientIP).toBeUndefined()
  request.mockImplementation(async (_method, path) => ({ data: path.endsWith('/preview') ? { candidate: { content: 'CIDR preview' } } : { active: { snippets: [], access_controls: [] }, draft: { revision: 0 } }, response: new Response() }))
  expect(await data.previewIPAccess()).toBe('CIDR preview')
  expect(request.mock.calls.at(-1)[2].body).toMatchObject({ routes: [], access_controls: [
    { scope: 'server', resource_id: 'domain', access: [' 10.0.0.1-10.0.0.4 '], forbidden: [], client_ip: { header: 'X-Forwarded-For', trusted_proxies: [' 10.0.0.1-10.0.0.9 '] } },
    { scope: 'location', resource_id: 'route', access: [], forbidden: ['10.0.0.2'] },
  ] })
  data.updateClientIP(null)
  expect(context.work.ipAccess.clientIP).toBeUndefined()
  data.dispose()
  expect(data.ipAccessEditorState()).toBeNull()
  expect(await data.previewIPAccess()).toBeNull()
  data.updateIPAccess('', 'access', '::1')
  data.updateClientIP(source)
  expect(context.work.ipAccess.clientIP).toBeUndefined()
  expect(context.work.ipAccess.access).toEqual([' 10.0.0.1-10.0.0.4 '])
})
