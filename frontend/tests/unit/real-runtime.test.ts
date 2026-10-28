import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from '../../src/App.vue'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { connectRouter, router } from '../../src/router'
import { createMockService as createReferenceService } from '../../src/mock/service.js'
import { i18n, languages, setLocale } from '../../src/shared/i18n'
vi.mock('@workspace-runtime', () => import('../../src/runtime/real.js'))
let wrapper: VueWrapper
let store: ReturnType<typeof useWorkspaceStore>
const command = vi.fn()
const previewDomainConfig = vi.fn()
const publishPoolDraft = vi.fn()
let attached: { readonly work: unknown; ui: { validation: unknown } } | null = null
async function click(action: string, extra = '') {
  const el = document.querySelector<HTMLElement>(`[data-action="${action}"]${extra}`)
  expect(el, action).not.toBeNull(); el!.click(); await flushPromises(); await nextTick()
}
async function fill(id: string, value: string) {
  const el = document.querySelector<HTMLInputElement | HTMLTextAreaElement>(id)!
  expect(el, id).not.toBeNull(); el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true })); el.dispatchEvent(new Event('change', { bubbles: true })); await nextTick()
}
async function go(path: string) { await router.push(path); await flushPromises(); await nextTick() }
beforeEach(async ({ task }) => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {}); localStorage.clear(); setLocale('zh-CN')
  // The unmodified archive seed is a deterministic UI fixture only. This suite
  // exercises the real controller, whose executor is separately API-tested.
  const reference = createReferenceService({ navigate: async () => {}, syncRoute() {}, render() {}, toast() {} })
  const state = structuredClone(reference.state)
  if (task.name.startsWith('renders zero visible projects')) {
    state.projects = []; state.environments = []; state.apps = []; state.services = []; state.domains = []; state.project = ''; state.env = ''
  }
  if (task.name.startsWith('renders valid NGINX defaults')) {
    const domain = state.domains.find((value: { id: string }) => value.id === 'd1')
    for (const config of [domain.active, domain.draft?.config].filter(Boolean)) {
      const route = config.routes.find((value: { kind: string }) => value.kind === 'proxy')
      config.body = null
      route.connect = null
      route.timeout = undefined
    }
  }
  reference.dispose(); localStorage.clear(); command.mockReset(); previewDomainConfig.mockReset().mockResolvedValue({})
  publishPoolDraft.mockReset().mockResolvedValue({ id: 'release-1', status: 'SUCCEEDED', targets: [
    { gateway_id: 'gw1', name: 'gateway-main', status: 'SUCCEEDED' },
    { gateway_id: 'gw2', name: 'gateway-pve', status: 'SUCCEEDED' },
  ] }); attached = null
  const pinia = createPinia(); setActivePinia(pinia); store = useWorkspaceStore()
  store.initialize(connectRouter(store), { state, templates: [], command, previewDomainConfig,
    composePage: () => '',
    unsupported(_action: string, show: (message: string) => void) { show('API capability unavailable; input retained.') },
    ...(task.name.startsWith('shows structured Gateway release') ? { publishPoolDraft, refresh: vi.fn() } : {}),
    attach(value: { readonly work: unknown; ui: { validation: unknown } }) { attached = value }, applicationSpec(app: { id: string }) { return { app: app.id, source_unavailable: true, services: [] } },
    template() { return { id: '', name: '', services: [], modes: [] } },
  })
  await router.push('/projects'); await router.isReady()
  wrapper = mount(App, { attachTo: document.body, global: { plugins: [pinia, router, i18n, [VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { enabled: false, retry: false, gcTime: 0 } } }) }]] } })
  await flushPromises()
})
it('shows structured Gateway release results after publishing', async () => {
  const state = store.state()
  const domain = state.domains.find(value => value.id === 'd1')!
  const gateways = state.gateways as { id: string; available: boolean }[]
  domain.gateways.forEach(id => { gateways.find(gateway => gateway.id === id)!.available = true })
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  if (!attached) throw new Error('runtime context was not attached')
  attached.ui.validation = { validation: { id: 'validation-1', draft_revision: 9 }, hash: JSON.stringify(attached.work), errors: [] }
  await click('publish')
  const acknowledgement = document.querySelector<HTMLInputElement>('#publish-ack')!
  expect(acknowledgement).not.toBeNull()
  acknowledgement.checked = true
  acknowledgement.dispatchEvent(new Event('change', { bubbles: true }))
  await click('release-start')
  expect(publishPoolDraft).toHaveBeenCalledTimes(1)
  const rows = [...document.querySelectorAll('#overlay .pool-release-targets li')]
  expect(rows).toHaveLength(2)
  expect(rows.map(row => row.querySelector('.pool-release-gateway')?.textContent)).toEqual(['gateway-main', 'gateway-pve'])
  expect(rows.every(row => row.querySelector('.badge.green'))).toBe(true)
  expect(document.querySelector('#overlay .pool-release-overall .badge.green')).not.toBeNull()
})
afterEach(() => { store.reset(); wrapper?.unmount(); document.body.innerHTML = '' })
it('keeps only the current sidebar section active after leaving resource details', async () => {
  const app = store.state().apps.find(item => item.project === 'portal' && item.env === 'prod')!
  const runtime = store.state().runtimes.find(item => item.id === 'swarm-prod')!
  const instance = store.state().instances.find(item => item.runtime === runtime.id)!
  const paths = [
    ['/projects/portal/environments/prod/applications', 'applications'],
    [`/projects/portal/environments/prod/applications/${app.id}`, 'applications'],
    [`/projects/portal/environments/prod/applications/${app.id}/compose`, 'applications'],
    ['/projects/portal/environments/prod/domains/d1?tab=routes', 'domains'],
    ['/projects/portal/environments/prod/routes', 'routes'],
    ['/projects/portal/environments/prod/services/api-service', 'services'],
    ['/projects/portal/environments/prod/certificates', 'certificates'],
    ['/infrastructure/runtimes/swarm-prod', 'runtimes'],
    [`/infrastructure/runtimes/${runtime.id}/nodes/${runtime.nodes[0]!.id}`, 'runtimes'],
    [`/infrastructure/runtimes/${runtime.id}/instances/${instance.id}`, 'runtimes'],
    ['/market', 'market'],
  ] as const
  for (const [path, page] of paths) {
    await go(path)
    expect(store.service!.ui.page).not.toBe('notfound')
    expect([...document.querySelectorAll('.sidebar .nav-item.active')].map(item => item.getAttribute('data-page')), path).toEqual([page])
    expect(document.querySelector('.sidebar .nav-item.active')?.getAttribute('aria-current'), path).toBe('page')
    store.service!.refresh(); await flushPromises(); await nextTick()
    expect([...document.querySelectorAll('.sidebar .nav-item.active')].map(item => item.getAttribute('data-page')), path + ' after refresh').toEqual([page])
  }
})
it('clears detail highlights when navigating to another sidebar section', async () => {
  await go('/projects/portal/environments/prod/services/api-service')
  store.service!.refresh(); await flushPromises(); await nextTick()
  await click('nav', '[data-page="routes"]')
  expect([...document.querySelectorAll('.sidebar .nav-item.active')].map(item => item.getAttribute('data-page'))).toEqual(['routes'])
})
it('retains a newly opened environment form when a local service filter finishes URL synchronization', async () => {
  await go('/projects/portal/environments/prod/services')
  let release!: () => void
  const gate = new Promise<boolean>(resolve => { release = () => resolve(true) })
  const stop = router.beforeResolve(to => to.query.q === 'web' ? gate : true)
  try {
    await fill('#x-service-search', 'web')
    await click('p-env-settings')
    await fill('#p-env-name', 'Keep my new title')
    const input = document.querySelector<HTMLInputElement>('#p-env-name')!
    input.focus()
    release(); await flushPromises(); await nextTick()
    expect(router.currentRoute.value.query.q).toBe('web')
    expect(document.querySelector<HTMLInputElement>('#p-env-name')?.value).toBe('Keep my new title')
    expect(document.activeElement).toBe(input)
    expect(store.service!.modal).not.toBeNull()
  } finally { release(); stop() }
})
function addEmptyProject() {
  store.state().projects.push({ id: 'prj-empty', key: 'test', name: 'test', description: 'Legacy project without environments', created: '' })
  store.service!.refresh()
}
it('offers eligible named runtimes in the original project form and preserves IDs and entered values across all locales', async () => {
  const runtimes = store.state().runtimes
  const runtime = runtimes[0]!
  runtime.name = 'Runtime <b>{name}</b> & host'; runtime.canWrite = true
  runtimes[1]!.canWrite = false
  Object.assign(runtimes[2]!, { enabled: false })
  await click('p-project-add')
  await fill('#p-name', 'Literal <project>'); await fill('#p-key', 'chosen-runtime')
  await fill('#p-description', 'Untranslated {name}'); await fill('#p-runtime', runtime.id)
  expect(document.querySelector(`#p-runtime option[value="${runtimes[1]!.id}"]`)).toBeNull()
  expect(document.querySelector(`#p-runtime option[value="${runtimes[2]!.id}"]`)).toBeNull()
  for (const { code } of languages) {
    setLocale(code); await flushPromises(); await nextTick()
    expect(document.querySelector<HTMLSelectElement>('#p-runtime')!.value).toBe(runtime.id)
    expect(document.querySelector(`#p-runtime option[value="${runtime.id}"]`)?.textContent).toBe(runtime.name)
    expect(document.querySelector('#p-runtime b')).toBeNull()
    expect(document.querySelector<HTMLInputElement>('#p-name')!.value).toBe('Literal <project>')
    expect(document.querySelector<HTMLTextAreaElement>('#p-description')!.value).toBe('Untranslated {name}')
  }
  await click('p-project-create'); expect(command).toHaveBeenCalledWith('createProject', expect.anything())
})
it('opens an empty project from its card and keeps project pages and empty resource lists scoped to it', async () => {
  addEmptyProject()
  await go('/projects/portal/environments/prod/routes')
  await click('nav', '[data-page="projects"]')
  await click('p-project', '[data-id="prj-empty"]')
  expect(router.currentRoute.value.path).toBe('/projects/prj-empty')
  expect(document.querySelector('#main h1')?.textContent).toBe('test')
  expect(store.state()).toMatchObject({ project: 'prj-empty', env: '' })
  expect(store.toasts.some(t => t.error)).toBe(false)
  for (const [page, path] of [['environments', '/environments'], ['variables', '/variables'], ['applications', '/environments/all/applications'], ['routes', '/environments/all/routes']]) {
    await click('nav', `[data-page="${page}"]`)
    expect(router.currentRoute.value.path).toBe('/projects/prj-empty' + path)
    expect(document.querySelector('.sidebar .active')?.getAttribute('data-page')).toBe(page)
    expect(store.state()).toMatchObject({ project: 'prj-empty', env: '' })
    expect(store.service!.ui.page).not.toBe('notfound')
    expect(document.querySelector('#main [data-action=service-detail]')).toBeNull()
  }
  await click('nav', '[data-page="projects"]')
  expect(document.querySelector('[data-action=p-project-settings][data-id="prj-empty"]')).not.toBeNull()
  expect(store.state().environments.some(e => e.project === 'prj-empty')).toBe(false)
  expect(command).not.toHaveBeenCalled()
})
it('accepts empty project deep links but still rejects a foreign or nonexistent explicit environment', async () => {
  addEmptyProject()
  for (const suffix of ['', '/environments', '/variables', '/environments/all/services']) {
    await go('/projects/prj-empty' + suffix)
    expect(store.service!.ui.page).not.toBe('notfound')
    expect(store.state()).toMatchObject({ project: 'prj-empty', env: '' })
  }
  for (const path of ['/projects/prj-empty/environments/prod/routes', '/projects/prj-empty/environments/missing/services', '/projects/missing', '/projects/portal/environments/missing/routes']) {
    await go(path)
    expect(store.service!.ui.page).toBe('notfound')
  }
  await go('/projects/prj-empty')
  await fill('#scope-project', 'portal'); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/portal')
  expect(store.state()).toMatchObject({ project: 'portal', env: 'prod' })
})
it('keeps unsaved input and the old scope when entering an empty project is cancelled', async () => {
  addEmptyProject()
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  await click('route-edit', '[data-id="r-api"]'); await fill('#rf-timeout', '91'); await click('route-apply')
  const path = router.currentRoute.value.fullPath
  await fill('#scope-project', 'prj-empty'); await flushPromises()
  await click('y-guard-stay')
  expect(router.currentRoute.value.fullPath).toBe(path)
  expect(store.state()).toMatchObject({ project: 'portal', env: 'prod' })
  await click('route-edit', '[data-id="r-api"]')
  expect(document.querySelector<HTMLInputElement>('#rf-timeout')!.value).toBe('91')
  await click('close')
  await fill('#scope-project', 'prj-empty'); await flushPromises(); await click('y-guard-discard')
  expect(router.currentRoute.value.path).toBe('/projects/prj-empty')
  expect(store.state()).toMatchObject({ project: 'prj-empty', env: '' })
  expect(store.service!.dirty).toBe(false)
  expect(command).not.toHaveBeenCalled()
})
it('validates an unsaved managed snippet with the server preview without saving the domain', async () => {
  await go('/projects/portal/environments/prod/domains/d2?tab=config&editor=code&file=server')
  const domain = store.state().domains.find(d => d.id === 'd2')!
  const active = domain.active.serverSnippet
  await fill('#code-input', 'add_header X-Preview yes;')
  await click('validate')
  expect(previewDomainConfig, document.querySelector('[role=dialog]')?.textContent || '').toHaveBeenCalledWith(expect.objectContaining({ domainID: 'd2', serverSnippet: 'add_header X-Preview yes;' }))
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('托管片段预览通过')
  expect(domain.active.serverSnippet).toBe(active)
  expect(command).not.toHaveBeenCalled()
  expect(store.toasts.some(t => t.message.includes('Gateway Pool'))).toBe(false)
})
it('reports local draft errors and native-only scope without server claims', async () => {
  await go('/projects/portal/environments/prod/domains/d2?tab=config&editor=code&file=server')
  previewDomainConfig.mockRejectedValueOnce({ params: { line: 1 } })
  await fill('#code-input', 'add_header X-Broken yes')
  await click('validate')
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('托管片段预览失败')
  expect(previewDomainConfig).toHaveBeenCalledTimes(1)
  await click('close')
  await click('native-mode')
  const ack = document.querySelector<HTMLInputElement>('#p-mode-ack')!
  ack.checked = true; ack.dispatchEvent(new Event('change', { bubbles: true })); await nextTick()
  await click('p-mode-file')
  await fill('#code-input', 'server {')
  await click('validate')
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('仅完成本地结构检查')
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('未在 Gateway 执行 nginx -t')
  expect(previewDomainConfig).toHaveBeenCalledTimes(1)
})
it('shows literal service names in domain and environment route references while links retain IDs', async () => {
  const state = store.state(), domain = state.domains.find(d => d.id === 'd1')!
  const route = domain.active.routes.find(r => r.kind === 'proxy')!
  const service = state.services.find(s => s.id === route.service)!
  const name = '删除 {id} <b>&lt;name&gt;</b> "nginx"'
  service.name = name
  const prefix = `/projects/${domain.project}/environments/${domain.env}`
  for (const path of [`${prefix}/routes`, `${prefix}/domains/${domain.id}?tab=overview`, `${prefix}/domains/${domain.id}?tab=routes`, `/projects/${domain.project}/environments/all/routes`, '/projects/all/environments/all/routes']) {
    await go(path)
    const links = [...document.querySelectorAll<HTMLAnchorElement>(`#main [data-action=service-detail][data-id="${service.id}"]`)]
    expect(links.length, path).toBeGreaterThan(0)
    expect(links.some(link => link.textContent?.includes(name)), path).toBe(true)
    for (const link of links) {
      expect(link.dataset.id).toBe(service.id)
      if (link.tagName === 'A') expect(link.href).toContain(`/services/${service.id}`)
      expect(link.querySelector('b')).toBeNull()
    }
  }
  await click('service-detail', `[data-id="${service.id}"]`)
  expect(document.querySelector('#main h1')?.textContent).toBe(name)
  expect(router.currentRoute.value.path).toContain(`/services/${service.id}`)
  expect(command).not.toHaveBeenCalled()
})
it('renders valid NGINX defaults when persisted managed fields are null or invalid', async () => {
  const domain = store.state().domains.find(d => d.id === 'd1')!
  await go(`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}?tab=config&editor=code&file=generated`)
  const output = document.querySelector('#main')?.textContent || ''
  expect(output).toContain('client_max_body_size 1m;')
  expect(output).toContain('proxy_connect_timeout 60s;')
  expect(output).toContain('proxy_read_timeout 60s;')
  expect(output).toContain('NSC default applied')
  expect(output).not.toMatch(/(?:client_max_body_size\s*;|nulls;|undefineds;)/)
})
it('renders confirmed per-Gateway application evidence instead of a fixed unknown state', async () => {
  const state = store.state()
  const domain = state.domains.find(d => d.id === 'd1')!
  const gateways = state.gateways as { id: string; available: boolean }[]
  domain.gateways.forEach(id => { gateways.find(gateway => gateway.id === id)!.available = true })
  await go(`/projects/${domain.project}/environments/${domain.env}/domains`)
  const row = document.querySelector(`[data-action="open-domain"][data-id="${domain.id}"]`)?.closest('tr')
  expect(row?.textContent).toContain(`已确认 · v${domain.rev}`)
  expect(row?.textContent).toContain(`${domain.gateways.length} / ${domain.gateways.length} Gateway`)
  expect(row?.textContent).not.toContain('生效状态未知')

  delete domain.loaded[domain.gateways[0]!]
  store.service!.refresh()
  await nextTick()
  const refreshed = document.querySelector(`[data-action="open-domain"][data-id="${domain.id}"]`)?.closest('tr')
  expect(refreshed?.textContent).toContain('生效状态未知')
  expect(refreshed?.textContent).toContain(`${domain.gateways.length - 1} / ${domain.gateways.length} Gateway`)
})
it('keeps route choice IDs and edit protection when two service labels are identical', async () => {
  const state = store.state(), domain = state.domains.find(d => d.id === 'd1')!
  const route = domain.active.routes.find(r => r.kind === 'proxy')!
  const current = state.services.find(s => s.id === route.service)!
  const other = state.services.find(s => s.project === current.project && s.env === current.env && s.id !== current.id)!
  current.name = other.name = 'Same Service'
  await go(`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}?tab=routes`)
  await click('route-edit', `[data-id="${route.id}"]`)
  const select = document.querySelector<HTMLSelectElement>('#rf-service')!
  expect(select.value).toBe(current.id)
  expect(select.querySelector(`option[value="${current.id}"]`)?.textContent).toContain('Same Service')
  expect(select.querySelector(`option[value="${other.id}"]`)?.textContent).toContain('Same Service')
  await fill('#rf-service', other.id)
  await click('route-apply')
  expect(store.service?.dirty).toBe(true)
  expect(domain.active.routes.find(r => r.id === route.id)!.service).toBe(current.id)
  expect(document.querySelector(`#main [data-action=service-detail][data-id="${other.id}"]`)).not.toBeNull()
  await click('nav', '[data-page="services"]'); await click('y-guard-stay')
  expect([...document.querySelectorAll('.sidebar .nav-item.active')].map(item => item.getAttribute('data-page'))).toEqual(['domains'])
  await click('route-edit', `[data-id="${route.id}"]`)
  expect(document.querySelector<HTMLSelectElement>('#rf-service')!.value).toBe(other.id)
  expect(command).not.toHaveBeenCalled()
})
it('switches proxy targets in place and requires a custom URL before applying', async () => {
  const domain = store.state().domains.find(value => value.id === 'd1')!
  const route = domain.active.routes.find(value => value.kind === 'proxy')!
  await go(`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}?tab=routes`)
  await click('route-edit', `[data-id="${route.id}"]`)
  const mode = document.querySelector<HTMLElement>('#rf-target-mode-label')!
  expect(mode).not.toBeNull()
  expect(mode.compareDocumentPosition(document.querySelector('#rf-service')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(document.querySelector('#rf-service')).not.toBeNull()
  expect(document.querySelector('#rf-external-url')).toBeNull()
  await click('route-target-mode', '[data-value="custom"]')
  const url = document.querySelector<HTMLInputElement>('#rf-external-url')!
  expect(url).not.toBeNull()
  expect(mode.compareDocumentPosition(url) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(document.querySelector('#rf-service')).toBeNull()
  expect(url.closest('.field')?.nextElementSibling?.querySelector('#rf-timeout')).not.toBeNull()
  await click('route-apply')
  expect(document.querySelector('#rf-external-url')).not.toBeNull()
  expect(document.querySelector('#overlay')?.textContent).toContain(i18n.global.t('domainPublication.targetRequired'))
  await fill('#rf-external-url', 'https://upstream.example/health')
  await click('route-apply')
  expect(document.querySelector('#rf-external-url')).toBeNull()
  await click('route-edit', `[data-id="${route.id}"]`)
  expect(document.querySelector<HTMLInputElement>('#rf-external-url')?.value).toBe('https://upstream.example/health')
  await click('route-target-mode', '[data-value="service"]')
  expect(document.querySelector('#rf-service')).not.toBeNull()
  expect(document.querySelector('#rf-external-url')).toBeNull()
  await click('route-apply')
  await click('route-edit', `[data-id="${route.id}"]`)
  expect(document.querySelector('#rf-service')).not.toBeNull()
  expect(document.querySelector('#rf-external-url')).toBeNull()
})
it('resolves missing services and searches names or IDs without changing project scope', async () => {
  const state = store.state(), domain = state.domains.find(d => d.id === 'd1')!
  const route = domain.active.routes.find(r => r.kind === 'proxy')!
  const service = state.services.find(s => s.id === route.service)!
  service.name = 'Distinct Search Service'
  await go(`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}?tab=routes`)
  await fill('#route-search', service.name)
  expect(document.querySelector(`#main [data-action=service-detail][data-id="${service.id}"]`)).not.toBeNull()
  await fill('#route-search', service.id)
  expect(document.querySelector(`#main [data-action=service-detail][data-id="${service.id}"]`)).not.toBeNull()
  await click('palette'); await fill('#palette-input', service.id)
  expect(document.querySelector(`#palette-results [data-id="${service.id}"]`)?.textContent).toContain(service.name)
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  await flushPromises()
  service.name = ''
  for (const { code } of languages) {
    setLocale(code)
    await go(`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}?tab=overview`)
    await flushPromises(); await nextTick()
    expect(document.querySelector('#main')?.textContent).toContain(i18n.global.t('references.service_unavailable', { id: service.id }))
  }
})
it('opens domain draft entry and uses names in cross-environment mapping and change summaries while retaining identity fields', async () => {
  const state = store.state()
  state.services.forEach((s, i) => { s.name = `Readable service ${i + 1}` })
  await go('/projects/portal/environments/prod/domains')
  await click('domain-add')
  expect(document.querySelector('.domain-draft-modal')).not.toBeNull()
  document.querySelector<HTMLButtonElement>('.domain-draft-modal .iconbtn')!.click()
  await flushPromises()
  await go('/projects/portal')
  await click('p-copy')
  await fill('#p-copy-source', 'd1'); await fill('#p-copy-target', 'dev'); await fill('#p-copy-name', 'names-copy.example.test')
  await click('p-copy-next')
  const mappings = [...document.querySelectorAll<HTMLSelectElement>('[data-copy-service]')]
  expect(mappings.length).toBeGreaterThan(0)
  for (const select of mappings) {
    const source = state.services.find(s => s.id === select.dataset.copyService)!
    expect(select.closest('.copy-mapping')?.querySelector('strong')?.textContent).toBe(source.name)
    expect(select.getAttribute('aria-label')).toContain(source.name)
    expect(select.closest('.copy-mapping')?.querySelector('small')?.textContent).toBe(source.id)
    for (const option of [...select.options].filter(o => o.value)) {
      const target = state.services.find(s => s.id === option.value)!
      expect(target.env).toBe('dev'); expect(option.textContent).toContain(target.name)
    }
  }
  await click('close')
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  const current = state.services.find(s => s.id === 'api-service')!
  const other = state.services.find(s => s.id === 'web-frontend')!
  await click('route-edit', '[data-id="r-api"]'); await fill('#rf-service', other.id); await click('route-apply')
  await click('tab', '[data-tab="overview"]')
  const summary = [...document.querySelectorAll('.change-item')].find(el => el.querySelector('.new')?.textContent === other.name)
  expect(summary?.querySelector('.old')?.textContent).toBe(current.name)
  expect(state.domains.find(d => d.id === 'd1')!.active.routes.find(r => r.id === 'r-api')!.service).toBe(current.id)
  expect(command).not.toHaveBeenCalled()
})
it('retains the original section while switching environments and the real controller stores no browser draft', async () => {
  await go('/projects/portal/environments/prod/services')
  await click('p-env', '[data-id="test"]')
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments/test/services')
  expect(document.querySelector<HTMLSelectElement>('#scope-env')?.value).toBe('test')
  await fill('#scope-env', 'prod')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments/prod/services')
  expect(document.querySelector('.sidebar .active')?.getAttribute('data-page')).toBe('services')
  expect(localStorage.length).toBe(0)
})
it('delegates the original project submission without inventing a successful create or closing its form', async () => {
  const before = store.state().projects.length
  await click('p-project-add'); await fill('#p-name', 'API project'); await fill('#p-key', 'api-project'); await click('p-project-create')
  expect(command).toHaveBeenCalledWith('createProject', expect.anything())
  expect(document.querySelector<HTMLInputElement>('#p-name')?.value).toBe('API project')
  expect(store.state().projects).toHaveLength(before)
  expect(localStorage.length).toBe(0)
})
it('retains route input, active configuration and the original leave confirmation when a domain draft cannot be saved', async () => {
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  await click('route-edit', '[data-id="r-api"]'); await fill('#rf-timeout', '90'); await click('route-apply'); await click('save')
  expect([...document.querySelectorAll('.toast')].at(-1)?.textContent).toContain('API capability unavailable')
  expect(store.state().domains.find(d => d.id === 'd1')!.active.routes.find((r: { id: string }) => r.id === 'r-api')!.timeout).toBe(30)
  await click('nav', '[data-page="services"]'); await click('y-guard-stay')
  await click('route-edit', '[data-id="r-api"]')
  expect(document.querySelector<HTMLInputElement>('#rf-timeout')?.value).toBe('90')
  expect(localStorage.length).toBe(0)
})
it('renders every original resource detail from supplied inventory with its literal resource identity', async () => {
  const state = store.state()
  const cases: [string, string][] = []
  for (const app of state.apps) for (const tab of ['overview', 'config', 'history', 'events'])
    cases.push([`/projects/${app.project}/environments/${app.env}/applications/${app.id}?tab=${tab}`, app.name])
  for (const service of state.services) for (const tab of ['instances', 'events', 'config', 'routes'])
    cases.push([service.project ? `/projects/${service.project}/environments/${service.env}/services/${service.id}?tab=${tab}` : `/services/${service.id}?tab=${tab}`, service.name])
  for (const domain of state.domains) for (const tab of ['overview', 'routes', 'config', 'tls', 'history'])
    cases.push([`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}?tab=${tab}`, domain.name])
  for (const [path, name] of cases) {
    await go(path)
    expect(document.querySelector('#main')?.textContent, path).toContain(name)
    expect(document.querySelectorAll('#main h1'), path).toHaveLength(1)
    expect(document.querySelector('#main')?.textContent, path).not.toContain('[object Object]')
  }
  expect(command).not.toHaveBeenCalled()
})

it('renders zero visible projects and aggregate routes without fabricating resource scope or undefined labels', async () => {
  for (const { code } of languages) {
    setLocale(code)
    for (const path of ['/projects', '/projects/all/environments/all/routes', '/projects/all/environments/all/services', '/projects/all/environments/all/applications']) {
      await go(path); store.service!.refresh(); await flushPromises()
      expect(store.service!.ui.page).not.toBe('notfound')
      expect(wrapper.text()).not.toContain('undefined')
      expect(router.currentRoute.value.path).toBe(path)
      expect(document.querySelector('[href*="undefined"]')).toBeNull()
    }
    await go('/projects/missing/environments/missing/routes')
    expect(store.service!.ui.page).toBe('notfound')
    expect(wrapper.text()).not.toContain('undefined')
    expect(router.currentRoute.value.path).toBe('/projects/missing/environments/missing/routes')
  }
  expect(command).not.toHaveBeenCalled()
})

it('hides deleted services by default and preserves the history switch in the URL without inflating counters', async () => {
  await go('/projects/portal/environments/prod/services')
  const current = store.state().services.find(s => s.env === 'prod')!
  const retired = { ...current, id: 'svc-retired-history', name: 'Retired history fixture', status: 'REMOVED', instances: [] }
  store.state().services.push(retired)
  store.service!.refresh(); await nextTick()
  const count = document.querySelector('.sidebar [data-page="services"]')?.textContent
  expect(document.querySelector('#main')?.textContent).not.toContain(retired.name)
  await click('x-removed-toggle')
  expect(router.currentRoute.value.query.include_removed).toBe('true')
  expect(document.querySelector('#main')?.textContent).toContain(retired.name)
  expect(document.querySelector('#main')?.textContent).toContain('已删除')
  expect(document.querySelector('.sidebar [data-page="services"]')?.textContent).toBe(count)
  await go('/projects/portal/environments/prod/services')
  expect(document.querySelector('#main')?.textContent).not.toContain(retired.name)
  await go('/projects/portal/environments/prod/services?include_removed=true')
  expect(document.querySelector('#main')?.textContent).toContain(retired.name)
  await click('x-removed-toggle')
  expect(router.currentRoute.value.query.include_removed).toBeUndefined()
  expect(document.querySelector('#main')?.textContent).not.toContain(retired.name)
})

it('uses the same four owned tasks in the service list and detail while ignoring foreign and historical unhealthy containers', async () => {
  await go('/projects/portal/environments/prod/services')
  const state = store.state()
  const service = state.services.find(s => s.env === 'prod' && s.project === 'portal')!
  Object.assign(service, { desired: 4, health: 'healthy', specVersion: null, workloadKind: 'replicated' })
  const runtime = state.runtimes.find(r => r.id === service.runtimeId)!
  Object.assign(runtime, { type: 'swarm', connection: 'online' })
  const base = state.instances.find(i => i.service === service.id)!
  const tasks: typeof state.instances = Array.from({ length: 4 }, (_, n) => ({ ...base, id: `owned-${n}`, runtime: runtime.id, service: service.id, kind: 'task', current: true, state: 'RUNNING', health: 'healthy', created: '2026-09-10T00:00:00Z', updated: '2026-09-14T21:00:00Z', stateChanged: '2026-09-14T22:00:00Z', containerCreated: '2026-09-14T22:01:00Z', started: '2026-09-14T22:02:00Z', observed: '2026-09-15T00:01:00Z' }))
  state.instances = [...tasks, ...tasks.map(i => ({ ...i, id: 'container-' + i.id, runtime: 'foreign-runtime', kind: 'container' as const })), { ...tasks[0]!, id: 'old-unhealthy', current: false, state: 'EXITED', health: 'unhealthy' }]
  store.service!.refresh(); await nextTick()
  const row = document.querySelector(`[data-service-id="${service.id}"]`)!
  expect(row.textContent).toMatch(/4\s*\/\s*4/)
  expect(row.textContent).not.toContain('健康检查失败')
  await click('x-service', `[data-id="${service.id}"]`)
  expect(document.querySelector('#main')?.textContent).toMatch(/4\s*\/\s*4/)
  expect(document.querySelector('#main')?.textContent).toContain('Task 创建')
  expect(document.querySelector('#main')?.textContent).toContain('Task 更新')
  expect(document.querySelector('#main')?.textContent).toContain('Container 创建')
  expect(document.querySelector('#main')?.textContent).toContain('Task 状态变化')
  expect(new Set([...document.querySelectorAll('#main [data-action="x-instance"]')].map(el => el.getAttribute('data-id')))).toEqual(new Set(tasks.map(t => t.id)))
})
