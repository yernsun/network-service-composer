import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from '../../src/App.vue'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { connectRouter, router } from '../../src/router'
import { i18n, setLocale } from '../../src/shared/i18n'
vi.mock('@workspace-runtime', () => import('../../src/mock/service.js'))
let wrapper: VueWrapper
let store: ReturnType<typeof useWorkspaceStore>
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
  if (task.name.startsWith('uses the new product brand')) localStorage.setItem(['nginx', 'service', 'composer-vue-v1.3'].join('-'), JSON.stringify({ projects: [] }))
  const pinia = createPinia(); setActivePinia(pinia)
  store = useWorkspaceStore(); store.initialize(connectRouter(store))
  await router.push('/projects'); await router.isReady()
  wrapper = mount(App, { attachTo: document.body, global: { plugins: [pinia, router, i18n, [VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { enabled: false, retry: false, gcTime: 0 } } }) }]] } })
  await flushPromises()
})
afterEach(() => { store.reset(); wrapper?.unmount(); document.body.innerHTML = '' })
it('keeps a single sidebar highlight after refreshing demo details and switching sections', async () => {
  await go('/projects/portal/environments/prod/services/api-service')
  expect(document.querySelector('.sidebar .nav-item.active')?.getAttribute('data-page')).toBe('services')
  store.service!.refresh(); await flushPromises(); await nextTick()
  await click('nav', '[data-page="routes"]')
  expect([...document.querySelectorAll('.sidebar .nav-item.active')].map(item => item.getAttribute('data-page'))).toEqual(['routes'])
  await go('/infrastructure/runtimes/swarm-prod')
  store.service!.refresh(); await flushPromises(); await nextTick()
  await click('nav', '[data-page="market"]')
  expect([...document.querySelectorAll('.sidebar .nav-item.active')].map(item => item.getAttribute('data-page'))).toEqual(['market'])
})
it('uses the new product brand and ignores the old demo storage key', async () => {
  expect(store.state().projects.length).toBeGreaterThan(0)
  expect(document.querySelector('.brand-name')?.textContent).toBe('Network Service Composer')
  expect(document.title).toContain(' · Network Service Composer')
  await click('about')
  expect(document.querySelector('#modal-title')?.textContent).toBe('Network Service Composer for Nginx')
})
it('handles topbar and modal close interactions through Vue in demo mode', async () => {
  const menu = document.querySelector<HTMLElement>('.topbar .mobile-menu')!
  menu.click(); await nextTick()
  expect(store.service!.ui.menu).toBe(true)
  menu.click(); await nextTick()
  expect(store.service!.ui.menu).toBe(false)
  document.querySelector<HTMLElement>('.topbar .search-btn')!.click(); await nextTick()
  expect(document.querySelector('#overlay [role="dialog"]')).not.toBeNull()
  store.service!.closeModal(); await nextTick()
  expect(document.querySelector('#overlay [role="dialog"]')).toBeNull()
  document.querySelector<HTMLElement>('.topbar .avatar')!.click(); await nextTick()
  expect(document.querySelector('#modal-title')?.textContent).toContain('Network Service Composer')
  document.querySelector<HTMLElement>('.modal-head [data-action="close"]')!.click(); await nextTick()
  expect(document.querySelector('#overlay [role="dialog"]')).toBeNull()
  document.querySelector<HTMLElement>('.topbar .avatar')!.click(); await nextTick()
  document.querySelector<HTMLElement>('.backdrop')!.click(); await nextTick()
  expect(document.querySelector('#overlay [role="dialog"]')).toBeNull()
  document.querySelector<HTMLElement>('.topbar [data-page="events"]')!.click()
  await flushPromises()
  expect(router.currentRoute.value.path).toContain('/events')
})
it('searches and opens a domain from the Vue command palette', async () => {
  await go('/projects/portal')
  const domain = store.state().domains.find(item => item.project === 'portal')!
  await click('palette')
  await fill('#palette-input', domain.name)
  expect(document.querySelector(`#palette-results [data-action="resource-domain"][data-id="${domain.id}"]`)).not.toBeNull()
  await click('resource-domain', `[data-id="${domain.id}"]`)
  expect(router.currentRoute.value.path).toBe(`/projects/${domain.project}/environments/${domain.env}/domains/${domain.id}`)
  expect(document.querySelector('#overlay [role="dialog"]')).toBeNull()
})
it('uses the same empty-project navigation in demo without borrowing another project environment', async () => {
  store.state().projects.push({ id: 'empty-demo', key: 'empty-demo', name: 'Empty demo', description: '', created: '' })
  store.service!.refresh()
  await flushPromises(); await nextTick()
  await click('p-project', '[data-id="empty-demo"]')
  expect(router.currentRoute.value.path).toBe('/projects/empty-demo')
  expect(document.querySelector('#main h1')?.textContent).toBe('Empty demo')
  expect(store.state()).toMatchObject({ project: 'empty-demo', env: '' })
  await click('nav', '[data-page="services"]')
  expect(router.currentRoute.value.path).toBe('/projects/empty-demo/environments/all/services')
  expect(document.querySelector('#main [data-action=x-service]')).toBeNull()
  await click('nav', '[data-page="environments"]')
  await click('p-env-add'); await fill('#p-env-name', 'First environment'); await fill('#p-env-slug', 'first'); await click('p-env-save')
  const created = store.state().environments.find(e => e.project === 'empty-demo')!
  expect(created).toMatchObject({ name: 'First environment', slug: 'first' })
  expect(store.state().env).toBe(created.id)
  expect(router.currentRoute.value.path).toBe(`/projects/empty-demo/environments/${created.id}/overview`)
})
it('keeps project/environment creation and saved demo state in the original modal flow', async () => {
  await click('p-project-add'); await click('p-project-create')
  expect(document.querySelector('#p-form-error')?.textContent).toContain('填写项目名称')
  await fill('#p-name', 'Test Project'); await fill('#p-key', 'test-project')
  await click('p-project-create')
  expect(document.querySelector('#main h1')?.textContent).toBe('Test Project')
  expect(store.state().projects.some(p => p.key === 'test-project')).toBe(true)
  await click('p-env-add'); await fill('#p-env-name', 'QA'); await fill('#p-env-slug', 'qa'); await click('p-env-save')
  expect(document.querySelector('#main h1')?.textContent).toBe('QA')
  expect(JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3')!).environments.some((e: { slug: string }) => e.slug === 'qa')).toBe(true)
})
it('edits an environment with Vue bound fields while retaining its gateway choices', async () => {
  await go('/projects/portal/environments/prod/overview')
  const environment = store.state().environments.find(item => item.id === 'prod')!
  const originalGateways = [...environment.gateways]
  await click('p-env-settings')
  expect(document.querySelector<HTMLInputElement>('#p-env-name')?.value).toBe(environment.name)
  expect(document.querySelector<HTMLInputElement>('#p-env-slug')?.disabled).toBe(true)
  expect([...document.querySelectorAll<HTMLInputElement>('[name="p-gateway"]:checked')].map(input => input.value)).toEqual(originalGateways)
  await fill('#p-env-name', 'Production renamed')
  await click('p-env-save')
  expect(environment.name).toBe('Production renamed')
  expect(environment.gateways).toEqual(originalGateways)
  expect(document.querySelector('#modal-title')).toBeNull()
})
it('navigates from the Vue environment list to its environment and domain', async () => {
  await go('/projects/portal/environments')
  expect(document.querySelector('.environment-grid .environment-card')).not.toBeNull()
  await click('p-env-settings', '[data-id="prod"]')
  expect(document.querySelector<HTMLInputElement>('#p-env-name')?.value).toBe(store.state().environments.find(item => item.id === 'prod')?.name)
  await click('close')
  await click('open-domain', '[data-id="d1"]')
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments/prod/domains/d1')
  await go('/projects/portal/environments')
  await click('p-env', '[data-id="prod"]')
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments/prod/overview')
})
it('switches certificate and domain views to all environments through the Vue context control', async () => {
  for (const section of ['certificates', 'domains']) {
    await go(`/projects/portal/environments/prod/${section}`)
    const all = document.querySelector<HTMLElement>('.project-context .env-tabs [data-id="all"]')
    expect(all?.dataset.vueControl).toBe('true')
    all!.click(); await flushPromises(); await nextTick()
    expect(router.currentRoute.value.path).toBe(`/projects/portal/environments/all/${section}`)
    expect(document.querySelector('#overlay [role="dialog"]')).toBeNull()
  }
})
it('uses Vue project overview controls for environment navigation and configuration copy', async () => {
  await go('/projects/portal')
  expect(document.querySelector('.project-hero h1')?.textContent).toBe(store.state().projects.find(item => item.id === 'portal')?.name)
  await click('p-copy')
  expect(document.querySelector('#modal-title')?.textContent).toBeTruthy()
  await click('close')
  document.querySelector<HTMLElement>('.project-metrics [data-page="environments"]')!.click()
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments')
})
it('filters domains in the Vue environment overview and opens the selected domain', async () => {
  await go('/projects/portal/environments/prod/overview')
  expect(document.querySelector('#domain-search')).not.toBeNull()
  await fill('#domain-search', 'missing-domain')
  expect(document.querySelector('.domain-list-name')).toBeNull()
  await fill('#domain-search', store.state().domains.find(item => item.id === 'd1')!.name)
  expect(document.querySelector('.domain-list-name')).not.toBeNull()
  await click('open-domain', '[data-id="d1"]')
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments/prod/domains/d1')
})
it('keeps the selected scope when opening a domain from the Vue domain list', async () => {
  await go('/projects/portal/environments/prod/domains')
  expect(document.querySelector('.domain-list-name')).not.toBeNull()
  await click('open-domain', '[data-id="d1"]')
  expect(router.currentRoute.value.path).toBe('/projects/portal/environments/prod/domains/d1')
  await go('/projects/all/environments/all/domains')
  expect(document.querySelectorAll('.domain-list-name').length).toBe(store.state().domains.length)
})
it('validates variable input, imports literal values and protects unsaved navigation', async () => {
  await go('/projects/portal/variables')
  await click('y-var-add'); await fill('#y-key', 'QA_PORT'); await fill('#y-type', 'integer'); await fill('#y-value', 'invalid'); await click('y-var-apply')
  expect(document.querySelector('#y-var-error')?.textContent).toContain('整数')
  await fill('#y-value', '9090'); await click('y-var-apply'); await click('y-vars-save')
  await click('y-env-import'); await fill('#y-import', 'LITERAL=删除 {name}\nSECOND=local'); await click('y-import-analyze'); await click('y-import-apply')
  await click('nav', '[data-page="environments"]')
  expect(document.querySelector('[data-action=y-guard-stay]')).not.toBeNull()
  await click('y-guard-stay')
  expect(document.querySelector('#main')?.textContent).toContain('删除 {name}')
  await click('y-vars-save')
  expect((store.state().parameters13 as { scopes: Record<string, { rows: Record<string, { value: string }> }> }).scopes['project:portal']!.rows.LITERAL!.value).toBe('删除 {name}')
})
it('keeps variable scope, search and editor validation in Vue controls', async () => {
  await go('/projects/portal/variables')
  expect(document.querySelector('[data-action="y-layer"][data-id="project"]')?.getAttribute('aria-pressed')).toBe('true')
  await fill('#y-var-search', 'IMAGE_TAG')
  expect(document.querySelectorAll('.y-var-table tbody tr')).toHaveLength(1)
  await click('y-layer', '[data-id="environment"]')
  expect(document.querySelector('[data-action="y-layer"][data-id="environment"]')?.getAttribute('aria-pressed')).toBe('true')
  await fill('#y-var-search', '')
  await click('y-var-add')
  await fill('#y-key', 'COMPOSE_INVALID')
  await fill('#y-value', 'value')
  await click('y-var-apply')
  expect(document.querySelector('#y-var-error')?.textContent).toBeTruthy()
  expect(document.querySelector('#overlay [role="dialog"]')).not.toBeNull()
})
it('analyzes variable imports and stages removal in Vue dialogs without saving automatically', async () => {
  await go('/projects/portal/variables')
  await click('y-env-import')
  await fill('#y-import', 'TOKEN=raw-secret')
  await click('y-import-analyze')
  expect(document.querySelector('#y-import-result .red')?.textContent).toBeTruthy()
  expect(document.querySelector('[data-action="y-import-apply"]')).toBeNull()
  await fill('#y-import', 'QA_PORT=9090')
  await click('y-import-analyze')
  expect(document.querySelector('[data-action="y-import-apply"]')).not.toBeNull()
  await fill('#y-import', 'QA_PORT=9091')
  expect(document.querySelector('[data-action="y-import-apply"]')).toBeNull()
  await click('y-import-analyze'); await click('y-import-apply')
  expect(store.service?.variableState().rows.find(row => row.name === 'QA_PORT')?.value).toBe('9091')
  expect((store.state().parameters13 as { scopes: Record<string, { rows: Record<string, unknown> }> }).scopes['project:portal']!.rows.QA_PORT).toBeUndefined()
  await click('y-var-remove', '[data-key="QA_PORT"]')
  expect(document.querySelector('[data-action="y-var-remove-confirm"]')).not.toBeNull()
  await click('y-var-remove-confirm')
  expect(store.service?.variableState().rows.some(row => row.name === 'QA_PORT')).toBe(false)
})
it('preserves Compose source edits while switching Vue files and views', async () => {
  await go('/projects/portal/environments/prod/applications/app-portal-prod/compose')
  const source = document.querySelector<HTMLTextAreaElement>('#y-compose-code')!.value
  await fill('#y-compose-code', `${source}\n# vue editor`)
  expect(store.service?.composeEditorState()?.dirty).toBe(true)
  await click('y-view', '[data-id="resolved"]')
  expect(document.querySelector('.y-rendered')).not.toBeNull()
  await click('y-view', '[data-id="source"]')
  expect(document.querySelector<HTMLTextAreaElement>('#y-compose-code')?.value).toContain('# vue editor')
  await click('y-file', '[data-id="runtime"]')
  await fill('#y-compose-code', 'LOG_LEVEL=warn\n')
  await click('y-file', '[data-id="compose"]')
  expect(document.querySelector<HTMLTextAreaElement>('#y-compose-code')?.value).toContain('# vue editor')
  await click('y-file', '[data-id="runtime"]')
  expect(document.querySelector<HTMLTextAreaElement>('#y-compose-code')?.value).toBe('LOG_LEVEL=warn\n')
})
it('preserves domain drafts and original route validation without mutating the active configuration', async () => {
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  await click('route-edit', '[data-id="r-api"]'); await fill('#rf-timeout', '90'); await click('route-apply'); await click('save')
  const domain = store.state().domains.find(d => d.id === 'd1')!
  expect(domain.draft!.config.routes.find((r: { id: string }) => r.id === 'r-api')!.timeout).toBe(90)
  expect(domain.active.routes.find((r: { id: string }) => r.id === 'r-api')!.timeout).toBe(30)
  await click('validate'); expect(document.querySelector('#modal-title')?.textContent).toContain('本地规则检查完成'); await click('close')
  await click('route-edit', '[data-id="r-api"]'); await fill('#rf-path', 'invalid'); await click('route-apply')
  expect(document.querySelector('.field-error')?.textContent).toBeTruthy()
})
it('keeps a Vue route form edit through the unsaved exit guard and kind switch', async () => {
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  await click('route-edit', '[data-id="r-api"]')
  await fill('#rf-path', '/new-path/')
  await click('close')
  expect(document.querySelector('[data-action="route-resume"]')).not.toBeNull()
  await click('route-resume')
  expect(document.querySelector<HTMLInputElement>('#rf-path')?.value).toBe('/new-path/')
  await click('route-kind', '[data-value="return"]')
  expect(document.querySelector('#rf-service')).toBeNull()
  expect(document.querySelector('#rf-code')).not.toBeNull()
  await fill('#rf-value', 'ok')
  await click('route-apply')
  expect(document.querySelector('#modal-title')).toBeNull()
  expect(store.service?.dirty).toBe(true)
})
it('requires confirmation before deleting a route from the Vue editor', async () => {
  await go('/projects/portal/environments/prod/domains/d1?tab=routes')
  const active = store.state().domains.find(item => item.id === 'd1')!.active.routes.length
  await click('route-edit', '[data-id="r-api"]')
  await click('route-delete')
  expect(document.querySelector('[data-action="route-delete-confirm"]')).not.toBeNull()
  await click('route-resume')
  expect(document.querySelector('#rf-path')).not.toBeNull()
  await click('route-delete')
  await click('route-delete-confirm')
  expect(store.state().domains.find(item => item.id === 'd1')!.active.routes.length).toBe(active)
  expect(store.service?.dirty).toBe(true)
})
it('renders original aggregate, infrastructure and detail views with their resource identities', async () => {
  for (const path of ['/projects/portal', '/projects/portal/environments', '/projects/portal/environments/all/services', '/projects/portal/environments/prod/overview', '/projects/portal/environments/prod/applications', '/projects/portal/environments/prod/applications/app-portal-prod', '/projects/portal/environments/prod/applications/app-portal-prod?tab=history', '/projects/portal/environments/prod/applications/app-portal-prod/compose', '/projects/portal/environments/prod/domains', '/projects/portal/environments/prod/configs', '/projects/portal/environments/prod/certificates', '/market', '/infrastructure/runtimes', '/infrastructure/events']) {
    await go(path)
    expect(router.currentRoute.value.fullPath).toBe(path)
    expect(document.querySelector('#main h1')?.textContent).toBeTruthy()
  }
})
it.each(['en', 'ja', 'de', 'fr', 'ru', 'es', 'zh-CN', 'zh-TW'])('keeps translated attribute snippets valid while creating a single-environment project (%s)', async locale => {
  setLocale(locale)
  await click('p-project-add'); await fill('#p-name', 'Literal "quotes" 删除 {name}'); await fill('#p-key', 'one-env'); await click('p-project-create')
  expect(document.querySelector('#main h1')?.textContent).toBe('Literal "quotes" 删除 {name}')
  const copy = document.querySelector<HTMLButtonElement>('[data-action=p-copy]')!
  expect(copy.disabled).toBe(true)
  expect(copy.title).toBeTruthy()
})

it.each(['success', 'partial', 'unknown'])('preserves the original demo deployment evidence and recovery flow (%s)', async scenario => {
  await go('/projects/portal/environments/prod/applications/app-portal-prod/compose')
  if (scenario !== 'success') {
    await click('x-demo'); await fill('#x-demo-scenario', scenario); await click('x-demo-save')
  }
  const jobs = store.state().instances.filter(i => i.service === 'db-migrate' && i.current).map(i => i.id)
  const editor = document.querySelector<HTMLTextAreaElement>('#y-compose-code')!
  await fill('#y-compose-code', editor.value + '\n# original deployment acceptance\n')
  await click('y-compose-save'); await click('y-compose-review')
  const ack = document.querySelector<HTMLInputElement>('#y-review-ack')!
  expect(ack).not.toBeNull(); ack.checked = true; ack.dispatchEvent(new Event('change', { bubbles: true }))
  await click('y-compose-deploy')
  const operations = () => store.state().appDeployments as { id: string; state: string; attempts: { targets: string[] }[] }[]
  await vi.waitFor(() => expect(operations()[0]!.state).toBe(scenario), { timeout: 7000 })
  expect(router.currentRoute.value.path).toBe('/operations/' + operations()[0]!.id)
  const id = operations()[0]!.id
  expect(store.state().instances.filter(i => i.service === 'db-migrate' && i.current).map(i => i.id)).toEqual(jobs)
  if (scenario === 'partial') {
    await click('x-retry')
    await vi.waitFor(() => expect(operations()[0]!.state).toBe('success'), { timeout: 7000 })
    expect(operations()[0]!.attempts).toHaveLength(2)
    expect(operations()[0]!.attempts[1]!.targets).toHaveLength(1)
  } else if (scenario === 'unknown') {
    await click('x-verify'); await click('x-verify-result', '[data-result="success"]')
    expect(operations()[0]!.state).toBe('success')
    expect(operations()[0]!.attempts).toHaveLength(1)
  }
  expect(operations()[0]!.id).toBe(id)
  const saved = JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3')!)
  expect(saved.appDeployments[0].id).toBe(id)
  expect(saved.appDeployments[0].state).toBe('success')
}, 20000)

it('keeps the original native configuration switch, acknowledgement and retained file contents', async () => {
  await go('/projects/portal/environments/prod/domains/d1?tab=config&editor=semantic')
  await click('native-mode')
  const ack = document.querySelector<HTMLInputElement>('#p-mode-ack')!
  expect(ack).not.toBeNull(); ack.checked = true; ack.dispatchEvent(new Event('change', { bubbles: true })); await nextTick()
  await click('p-mode-file')
  const code = document.querySelector<HTMLTextAreaElement>('#code-input')!
  expect(code.value).toContain('server_name')
  await fill('#code-input', code.value + '\n# literal retained configuration\n'); await click('save')
  const domain = store.state().domains.find(d => d.id === 'd1')!
  expect(domain.draft!.config.mode).toBe('native')
  expect(domain.draft!.config.native).toContain('# literal retained configuration')
  await click('managed-mode')
  const managedAck = document.querySelector<HTMLInputElement>('#p-mode-ack')!
  managedAck.checked = true; managedAck.dispatchEvent(new Event('change', { bubbles: true })); await nextTick()
  await click('p-mode-form'); await click('save')
  expect(domain.draft!.config.mode).toBe('managed')
  expect(domain.active.mode).toBe('managed')
})

it('updates an open modal language while preserving literal edits, focus and acknowledgement', async () => {
  await click('p-project-add')
  await fill('#p-name', '删除 {name} Docker $HOME')
  await fill('#p-key', 'literal-project')
  const name = document.querySelector<HTMLInputElement>('#p-name')!
  name.focus(); name.setSelectionRange(2, 8)
  const title = document.querySelector('#modal-title')!.textContent
  setLocale('en'); await nextTick(); await flushPromises()
  expect(document.querySelector('#modal-title')!.textContent).not.toBe(title)
  expect(name.value).toBe('删除 {name} Docker $HOME')
  expect(document.querySelector<HTMLInputElement>('#p-key')!.value).toBe('literal-project')
  expect(document.activeElement).toBe(name)
  expect([name.selectionStart, name.selectionEnd]).toEqual([2, 8])
  await click('close')
  await go('/projects/portal/environments/prod/domains/d1?tab=config&editor=semantic')
  await click('native-mode')
  const ack = document.querySelector<HTMLInputElement>('#p-mode-ack')!
  ack.checked = true; ack.dispatchEvent(new Event('change', { bubbles: true })); await nextTick()
  expect(document.querySelector<HTMLButtonElement>('[data-action=p-mode-file]')!.disabled).toBe(false)
  setLocale('ja'); await nextTick(); await flushPromises()
  expect(ack.checked).toBe(true)
  expect(document.querySelector<HTMLButtonElement>('[data-action=p-mode-file]')!.disabled).toBe(false)
  await click('p-mode-file')
  expect(document.querySelector<HTMLTextAreaElement>('#code-input')!.value).toContain('server_name')
})
