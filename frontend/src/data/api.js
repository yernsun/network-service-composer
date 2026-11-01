import { domainAccessControls, emptyIPAccess, ipAccessFor } from './ip-access'
import { readableCompose } from '../compose/source'
import { h } from 'vue'
import { View } from '../components/view'
import ComposeWorkbench from '../components/ComposeWorkbench.vue'
import RuntimeTimeline from '../components/RuntimeTimeline.vue'
import ServiceLogs from '../components/ServiceLogs.vue'
import DomainHistory from '../components/DomainHistory.vue'
import { request, errorText, ApiRequestError } from '../shared/api/client'
import { idempotencyKey } from '../shared/browser/random'
import { i18n } from '../shared/i18n'
import { loadInventory, queryClient } from './inventory'
import { invalidateWorkloadInventory, refreshComponentQueries } from './inventory-cache'
import { resourcesChanged as notifyResourcesChanged, refreshFailure, retryResourceRefresh } from './resource-changes'
import { parse as parseYAML } from 'yaml'
import { composeSource, checkCompose, composeSession, composeSignature, deploymentReady, planFor } from '../compose/session'
import { operationTracker } from './operation-tracker'
import { operationView } from './operation-view'
import { watchRuntimeObservations } from './runtime-observations'
import { domainRouteChanges, saveDomainDraft, validatePoolDraft, readValidation, publishPoolDraft, readRelease } from './domain-publication'

const input = id => document.getElementById(id)?.value?.trim() || ''
const checked = id => document.getElementById(id)?.checked === true
const fingerprint = value => JSON.stringify(value)
const unsupportedError = action => new ApiRequestError(422, 'frontend_capability_unavailable', { action })
let refreshActive
export async function refreshProjectData() { await refreshActive?.() }

export async function initializeData(path = window.location.href) {
  const requests = new AbortController()
  let currentPath = path
  let includeRemoved = new URL(path, window.location.href).searchParams.get('include_removed') === 'true'
  let inventory
  try { inventory = await loadInventory(includeRemoved, { path, signal: requests.signal, fresh: true }) }
  catch (error) { requests.abort(); queryClient.clear(); throw error }
  const state = inventory.state
  const templates = [...inventory.templates]
  let context
  let pending = null
  let disposed = false
  let stopObservations
  let readGeneration = 0
  let changeGeneration = 0
  let navigationGeneration = 0
  let navigationTask
  let navigationController
  let refreshTask
  let refreshController
  let refreshQueued = false
  let forceQueued = false
  let refreshOptions
  let replay = false
  let review = null
  const restoreReviews = new Map()
  const keys = idempotencyKey()
  const message = error => error instanceof ApiRequestError && error.code === 'frontend_capability_unavailable'
    ? i18n.global.t(error.params.action === 'expired-preflight' ? 'integration.preflightStale' : error.params.action === 'environment-application-identity' ? 'integration.identityMismatch' : ['saveDraft', 'startRelease', 'restore-confirm', 'drift-restore-confirm'].includes(error.params.action) ? 'integration.poolScopeUnavailable' : 'integration.unavailable', error.params) : errorText(error)
  const operations = operationTracker({
    signal: requests.signal,
    read: async (id, signal) => (await request('get', '/operations/{id}', { params: { path: { id } }, signal })).data,
    observe: operation => {
      const current = state.appDeployments.find(o => o.id === operation.id)
      if (!current) return
      Object.assign(current, operationView(operation, { project_id: current.project, id: current.env }, current.version))
      renderOperation()
    },
    complete: async operation => {
      invalidateWorkloadInventory()
      notifyResourcesChanged({ reason: 'operation', operationID: operation.id })
      await refreshAfterWrite()
      renderCurrent()
    },
    error: error => context.formError(message(error)),
  })
  function renderOperation() {
    // A background observation must not recreate another page's active form.
    if (!disposed && context.ui.page === 'operation' && !context.modal) context.render()
  }
  function renderCurrent() {
    if (!disposed && context) {
      if (context.renderData) context.renderData()
      else context.render()
    }
  }
  function unsupported(action, show = context?.formError) {
    show?.(message(unsupportedError(action)))
    return false
  }
  function applicationSpec(app) {
    return { app: app.id, name: app.name, project: app.project, env: app.env, runtime: app.runtime,
      source_unavailable: true,
      services: state.services.filter(s => s.app === app.id && s.status !== 'REMOVED').map(s => ({ id: s.id, name: s.name,
        image: s.image || null, replicas: s.desired, memory: s.memory, port: s.port })),
    }
  }
  function template(id) {
    return inventory.templates.find(t => t.id === id) || inventory.templates[0] || {
      id: '', key: '', name: '', notes: '', services: [], modes: [], stateful: false,
    }
  }
  async function loadScope(path, history, fresh, scopeOptions = {}, signal = requests.signal, admitted = () => true, retryChanges = true) {
    const generation = ++readGeneration
    let next
    let selectedDocument
    while (!disposed && !signal.aborted && admitted()) {
      const changes = changeGeneration
      try {
        next = await loadInventory(history, { path, signal, reuseCached: !fresh, fresh, ...scopeOptions })
        const editor = context?.workspace
        if (editor?.sourceServiceID && new URL(path, window.location.href).pathname.endsWith(`/applications/${editor.docApp}/compose`)
          && (!editor.docsDirty?.() || scopeOptions.rebaseDocument === editor.docApp)) {
          const id = editor.sourceServiceID
          const [source, draft] = await Promise.all([
            request('get', '/services/{id}/source', { params: { path: { id } }, signal }),
            request('get', '/services/{id}/draft', { params: { path: { id } }, signal }),
          ])
          selectedDocument = { ...next.state.parameters13.documents[editor.docApp], unavailable: false, source: readableCompose(source.data.compose_yaml), rawSource: source.data.compose_yaml,
            sourceRevision: source.data.baseline_revision_id ?? source.data.revision_id, runtime: source.data.runtime_env,
            draft: draft.data.revision ? { source: readableCompose(draft.data.compose_yaml), runtime: draft.data.runtime_env, base: draft.data.application_revision, sourceRevision: draft.data.revision_id || (source.data.baseline_revision_id ?? source.data.revision_id) } : null,
            etag: draft.response.headers.get('ETag') }
        }
      }
      catch (error) {
        if (disposed || signal.aborted || !admitted()) return false
        if (error.name === 'AbortError' && changes !== changeGeneration) { if (!retryChanges) return false; fresh = true; continue }
        throw error
      }
      if (changes === changeGeneration) break
      if (!retryChanges) return false
      fresh = true
    }
    if (!next || disposed || signal.aborted || !admitted() || generation !== readGeneration) return false
    // Preserve an unsaved editor's baseline and ETag, not just its text. A newer
    // server read must not authorize overwriting somebody else's concurrent edit.
    const editor = context?.workspace
    const previousVersions = inventory.versions
    const variableModal = ['y-variable', 'y-remove', 'y-env-import'].includes(context?.modal?.kind)
    const variableKey = editor?.varDraft?.key || (editor?.layer === 'project' ? `project:${state.project}` : `environment:${state.env}`)
    if (editor && (editor.varsDirty?.() || variableModal)) {
      const key = variableKey
      if (state.parameters13?.scopes[key]) next.state.parameters13.scopes[key] = state.parameters13.scopes[key]
      if (previousVersions.has(key)) next.versions.set(key, previousVersions.get(key))
    } else if (editor?.varDraft && next.state.parameters13.scopes[variableKey]) editor.varDraft = null
    const documentDirty = editor?.docsDirty?.()
    if (editor?.docApp && documentDirty && scopeOptions.rebaseDocument !== editor.docApp) {
      const id = editor.docApp
      if (state.parameters13.documents[id]) next.state.parameters13.documents[id] = state.parameters13.documents[id]
      const key = `application-draft:${id}`
      if (previousVersions.has(key)) next.versions.set(key, previousVersions.get(key))
    }
    const editedDomain = state.domains?.find(domain => domain.id === context?.ui.domain)
    const domainDirty = editedDomain && context.work && (context.modal && context.ui.page === 'domain' || fingerprint(context.work) !== fingerprint(editedDomain.draft?.config || editedDomain.active))
    if (domainDirty) {
      next.state.domains = next.state.domains.map(domain => domain.id === editedDomain.id ? editedDomain : domain)
    }
    inventory = next
    currentPath = path
    includeRemoved = history
    templates.splice(0, templates.length, ...next.templates)
    const scope = { project: state.project, env: state.env, view: state.view, serviceLayout: state.serviceLayout }
    const drafts = new Map(state.apps.filter(a => a.draft).map(a => [a.id, a.draft]))
    Object.assign(state, next.state, scope)
    const document = selectedDocument || state.parameters13.documents[editor?.docApp]
    if (editor?.docApp && document && !document.unavailable && (!editor.sourceServiceID || selectedDocument)
      && (!documentDirty || scopeOptions.rebaseDocument === editor.docApp)) {
      if (selectedDocument) { editor.serviceSourceDocument = selectedDocument; editor.serviceDraftETag = selectedDocument.etag }
      editor.documentBaseRevision = document.draft?.base ?? state.apps.find(app => app.id === editor.docApp)?.revision
      editor.sourceBaseRevisionID = document.draft?.sourceRevision || document.sourceRevision
      if (!documentDirty) { editor.composeWork = document.draft?.source ?? document.source; editor.runtimeWork = document.draft?.runtime ?? document.runtime }
    }
    if (editedDomain && !domainDirty) context.refreshDomainEditor?.()
    for (const a of state.apps) if (drafts.has(a.id)) a.draft = drafts.get(a.id)
    for (const e of state.environments) if (context) context.ENV[e.id] = e.name
    operations.resume(state.appDeployments)
    return true
  }
  function refresh(history = includeRemoved, scopeOptions, force = false) {
    if (disposed) return Promise.resolve(false)
    refreshQueued = true
    forceQueued ||= force
    refreshOptions = { history, scopeOptions }
    if (!refreshTask) refreshTask = Promise.resolve().then(async () => {
      let applied = false
      while (refreshQueued && !disposed) {
        if (navigationTask) {
          await navigationTask.catch(() => {})
          // Let Router's admitted route reach afterEach before rendering it.
          await new Promise(resolve => setTimeout(resolve, 0))
          continue
        }
        const options = refreshOptions
        const fresh = forceQueued
        refreshQueued = false; forceQueued = false
        const route = navigationGeneration
        const wizard = context?.workload.x.wizard
        refreshController = new AbortController()
        const signal = AbortSignal.any([requests.signal, refreshController.signal])
        let result
        try {
          result = await loadScope(currentPath, options.history, fresh,
            options.scopeOptions || (wizard ? { action: 'install', projectID: wizard.project, environmentID: wizard.env } : {}), signal, () => route === navigationGeneration, false)
        } catch (error) {
          // A newer mutation already requested another batch. Try that batch
          // even when the older read failed; failures alone never queue retries.
          if (refreshQueued) continue
          throw error
        }
        if (route !== navigationGeneration) { refreshQueued = true; forceQueued ||= fresh; continue }
        applied ||= result
        if (result) { refreshFailure.value = undefined; renderCurrent() }
      }
      return applied
    }).finally(() => { refreshTask = undefined; refreshController = undefined })
    return refreshTask
  }
  function prepareRoute(path) {
    if (disposed) return Promise.resolve(false)
    const route = ++navigationGeneration
    navigationController?.abort(); refreshController?.abort()
    navigationController = new AbortController()
    const signal = AbortSignal.any([requests.signal, navigationController.signal])
    const history = new URL(path, window.location.href).searchParams.get('include_removed') === 'true'
    const task = (async () => {
      if (queryClient.cancelQueries) await queryClient.cancelQueries({ queryKey: ['workspace-read'] }, { revert: false })
      if (disposed || signal.aborted) return false
      const applied = await loadScope(path, history, true, {}, signal, () => route === navigationGeneration)
      if (applied) queryClient.removeQueries?.({ type: 'inactive', predicate: query => query.queryKey[0] !== 'workspace-read' })
      return applied
    })().finally(() => { if (navigationTask === task) navigationTask = undefined })
    navigationTask = task
    return task
  }
  async function refreshAfterWrite() {
    try { return await refresh() }
    catch (error) { if (!disposed) refreshFailure.value = error; return false }
  }
  async function write(method, path, body, params = {}, etag) {
    const result = await request(method, path, { body, params: { path: params }, signal: requests.signal, headers: {
      'Idempotency-Key': keys.forRequest(method, path, params, body), ...(etag ? { 'If-Match': etag } : {}),
    } })
    keys.reset()
    return result
  }
  async function saveDoc(silent = false) {
    const editor = context.workspace
    if (!editor.docsDirty?.()) return true
    const app = state.apps.find(a => a.id === editor.docApp)
    const doc = editor.serviceSourceDocument || state.parameters13.documents[editor.docApp]
    if (!app || !doc || doc.unavailable) throw unsupportedError('application-source')
    const saved = await write('put', editor.sourceServiceID ? '/services/{id}/draft' : '/applications/{id}/draft', {
      application_revision: editor.documentBaseRevision ?? app.revision, ...((editor.sourceBaseRevisionID || state.parameters13.documents[app.id]?.sourceRevision) ? { revision_id: editor.sourceBaseRevisionID || state.parameters13.documents[app.id]?.sourceRevision } : {}), compose_yaml: editor.composeWork, runtime_env: editor.runtimeWork,
    }, { id: editor.sourceServiceID || app.id }, editor.sourceServiceID ? editor.serviceDraftETag : inventory.versions.get(`application-draft:${app.id}`))
    const savedDraft = { source: readableCompose(saved.data.compose_yaml), runtime: saved.data.runtime_env, base: saved.data.application_revision, sourceRevision: saved.data.revision_id }
    doc.draft = savedDraft
    if (editor.sourceServiceID) { editor.serviceSourceDocument.draft = savedDraft; editor.serviceDraftETag = `"rev-${saved.data.revision}"` }
    else inventory.versions.set(`application-draft:${app.id}`, saved.response?.headers.get('ETag') || `"rev-${saved.data.revision}"`)
    await refreshAfterWrite()
    editor.review = null
    if (!silent) { context.render(); context.toast(i18n.global.t('integration.saved'), false) }
    return true
  }
  function composePage() {
    const editor = context.workspace
    const app = state.apps.find(a => a.id === editor.docApp)
    const doc = editor.serviceSourceDocument || state.parameters13.documents[editor.docApp]
    if (!app || !doc || doc.unavailable) return new View([i18n.global.t('integration.unavailable')])
    return new View([h(ComposeWorkbench, { key: `${app.id}:${editor.sourceServiceID || ''}`, editor, application: app, document: doc, save: saveDoc, services: state.services.filter(s => s.app === app.id),
      selectService: async id => {
       let selected = state.parameters13.documents[app.id]
       if (id) {
        const [source, draft] = await Promise.all([request('get', '/services/{id}/source', { params: { path: { id } } }), request('get', '/services/{id}/draft', { params: { path: { id } } })])
        selected = { ...selected, source: readableCompose(source.data.compose_yaml), rawSource: source.data.compose_yaml, sourceRevision: source.data.baseline_revision_id ?? source.data.revision_id, runtime: source.data.runtime_env, draft: draft.data.revision ? { source: readableCompose(draft.data.compose_yaml), runtime: draft.data.runtime_env, base: draft.data.application_revision, sourceRevision: draft.data.revision_id || (source.data.baseline_revision_id ?? source.data.revision_id) } : null }
        editor.serviceDraftETag = draft.response.headers.get('ETag')
       }
       editor.sourceServiceID = id; editor.serviceSourceDocument = id ? selected : null; editor.composeWork = selected.draft?.source ?? selected.source; editor.runtimeWork = selected.draft?.runtime ?? selected.runtime; editor.documentBaseRevision = selected.draft?.base ?? app.revision; editor.sourceBaseRevisionID = selected.draft?.sourceRevision || selected.sourceRevision; context.render()
      },
      onEdit: (source, runtime) => { editor.composeWork = source; editor.runtimeWork = runtime; editor.review = null },
      onView: value => { editor.view = value },
      refreshBaseline: async () => {
        await refresh(includeRemoved, { rebaseDocument: app.id }, true)
        let latest = state.parameters13.documents[app.id]
        if (editor.sourceServiceID) {
          const id = editor.sourceServiceID
          const [source, draft] = await Promise.all([request('get', '/services/{id}/source', { params: { path: { id } } }), request('get', '/services/{id}/draft', { params: { path: { id } } })])
          latest = { ...latest, source: readableCompose(source.data.compose_yaml), rawSource: source.data.compose_yaml, sourceRevision: source.data.baseline_revision_id ?? source.data.revision_id, runtime: source.data.runtime_env, draft: draft.data.revision ? { source: readableCompose(draft.data.compose_yaml), runtime: draft.data.runtime_env, base: draft.data.application_revision, sourceRevision: draft.data.revision_id } : null }
          editor.serviceSourceDocument = latest
          editor.serviceDraftETag = draft.response.headers.get('ETag')
        }
        editor.documentBaseRevision = state.apps.find(a => a.id === app.id)?.revision
        editor.sourceBaseRevisionID = latest.sourceRevision
        return latest
      },
      merge: async files => (await request('post', '/environments/{id}/compose-merge', { params: { path: { id: app.env } }, body: { compose_files: files } })).data.compose_yaml,
      analyze: async (source, runtime, plan) => (await request('post', '/environments/{id}/compose-preview', { params: { path: { id: app.env } }, body: { compose_yaml: source, runtime_env: runtime, ...(app.mode === 'stack' ? { service_mode: 'update' } : {}), deployment_plan: plan } })).data,
      review: async (source, runtime, baseRevision, plan) => {
        const environment = await request('get', '/environments/{id}', { params: { path: { id: app.env } } })
        const response = await request('post', app.mode === 'stack' ? '/environments/{id}/service-preflight' : '/environments/{id}/preflight', { params: { path: { id: app.env } }, body: { compose_yaml: source, runtime_env: runtime, ...(app.mode === 'stack' ? { service_mode: 'update' } : {}), base_revision_id: baseRevision, deployment_plan: plan } })
        return { id: response.data.preflight_id, preview: response.data, etag: environment.response.headers.get('ETag') }
      },
      deploy: async reviewed => {
        const result = await write('post', app.mode === 'stack' ? '/environments/{id}/service-deployments' : '/environments/{id}/deployments', { preflight_id: reviewed.id }, { id: app.env }, reviewed.etag)
        if (await refreshAfterWrite()) context.workspace.showOperation(result.data.id)
        void pollOperation(result.data.id)
      },
    })])
  }
  function timelinePage(filter = {}) {
    return new View([h(RuntimeTimeline, { key: `${filter.applicationID || ''}:${filter.serviceID || ''}:${state.project}:${state.env}`,
      state, projectId: state.project, environmentId: state.env, applicationId: filter.applicationID || '', serviceId: filter.serviceID || '' })])
  }
  function domainHistoryPage(domain) {
    return new View([h(DomainHistory, { key: `${domain.pool_id}:${domain.id}`, poolId: domain.pool_id, domainId: domain.id,
      routeIds: [...new Set([...(domain.active?.routes || []), ...(domain.draft?.config?.routes || [])].map(route => route.id))] })])
  }
  function serviceLogsPage(serviceId, runtimeType) {
    return new View([h(ServiceLogs, { key: serviceId, serviceId, runtimeType })])
  }
  async function saveVars(silent = false) {
    const draft = context.workspace.varDraft
    if (!draft || !context.workspace.varsDirty()) return true
    const savedFingerprint = fingerprint(draft.rows)
    const [layer, id] = draft.key.split(':')
    if (!['project', 'environment'].includes(layer)) throw unsupportedError('application-variable-layer')
    if (Object.values(draft.rows).some(v => v.note || !['string', 'credential'].includes(v.type))) throw unsupportedError('variable-types-and-notes')
    if (Object.values(draft.rows).some(v => v.type === 'credential' && !v.mask)) throw unsupportedError('versioned-variable-credential-reference')
    const values = Object.entries(draft.rows).map(([key, v]) => ({ key, ...(v.mask ? {} : { value: v.value }), secret: v.type === 'credential', mask: !!v.mask }))
    const path = layer === 'project' ? '/projects/{id}/variables' : '/environments/{id}/variables'
    const saved = await write('put', path, { values }, { id }, `"rev-${draft.base}"`)
    if (context.workspace.varDraft === draft && Number.isInteger(saved.data.revision)) {
      const rows = Object.fromEntries((saved.data.values || values).map(value => [value.key, { value: value.mask ? '' : value.value || '', type: value.secret ? 'credential' : 'string', note: '', mask: value.mask, secret: value.secret }]))
      state.parameters13.scopes[draft.key] = { ...state.parameters13.scopes[draft.key], version: saved.data.revision, rows }
      inventory.versions.set(draft.key, saved.response?.headers.get('ETag') || `"rev-${saved.data.revision}"`)
      draft.base = saved.data.revision
    }
    // An older completion never clears newer edits.
    if (context.workspace.varDraft === draft && fingerprint(draft.rows) === savedFingerprint) context.workspace.varDraft = null
    await refreshAfterWrite()
    context.workspace.review = null
    if (!silent) { context.render(); context.toast(i18n.global.t('integration.saved'), false) }
    return true
  }
  async function createProject(form) {
    const name = form?.name?.trim() ?? input('p-name'), slug = form?.key?.trim() ?? input('p-key')
    if (!name) return context.formError(i18n.global.t('archive.message_2f634dd0ccf6'))
    if (!/^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$/.test(slug)) return context.formError(i18n.global.t('archive.message_c4241b5e5a18'))
    if (state.projects.some(p => p.key === slug)) return context.formError(i18n.global.t('archive.message_855b49cbc2e7'))
    const runtime = form?.runtime ?? input('p-runtime')
    const result = await write('post', '/projects', { name, slug, description: form?.description?.trim() ?? input('p-description'), ...(runtime ? { default_runtime_id: runtime } : {}), initial_environment: { name: 'production', kind: 'production' } })
    const project = result.data
    if (!await refreshAfterWrite()) { context.closeModal(true); return }
    context.selectScope(project.id, project.initial_environment_id)
    context.ui.page = 'project'
    context.closeModal(true); context.updateHash(); context.render()
  }
  async function saveEnvironment(form) {
    const editing = context.modal?.envId
    const name = form?.name?.trim() ?? input('p-env-name'), slug = form?.slug?.trim() ?? input('p-env-slug')
    if (!name) return context.formError(i18n.global.t('archive.message_d60eb6af32aa'))
    if (!/^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/.test(slug)) return context.formError(i18n.global.t('archive.message_dd782d81d5e6'))
    if (state.environments.some(e => e.project === state.project && e.slug === slug && e.id !== editing)) return context.formError(i18n.global.t('archive.message_e83d9bb46c30'))
    const body = { display_name: name, kind: form?.kind ?? input('p-env-kind'), gateway_ids: form?.gateways ?? [...document.querySelectorAll('[name="p-gateway"]:checked')].map(el => el.value) }
    if (editing) {
      const original = inventory.raw.environments.find(e => e.id === editing && e.project_id === state.project)
      if (!original) throw new ApiRequestError(404, 'resource.not_found')
      await write('patch', '/environments/{id}', body, { id: editing }, `"rev-${form?.revision ?? original.revision}"`)
      if (!await refreshAfterWrite()) { context.closeModal(true); return }
    } else {
      const project = await request('get', '/projects/{id}', { params: { path: { id: state.project } } })
      const result = await write('post', '/projects/{id}/environments', { ...body, name: slug }, { id: state.project }, `"rev-${project.data.revision}"`)
      if (!await refreshAfterWrite()) { context.closeModal(true); return }
      context.selectScope(state.project, result.data.id); context.ui.page = 'overview'
    }
    context.closeModal(true); context.updateHash(); context.render()
  }
  async function deleteEnvironment(id) {
    const env = inventory.raw.environments.find(e => e.id === id && e.project_id === state.project)
    if (!env) throw new ApiRequestError(404, 'resource.not_found')
    await write('delete', '/environments/{id}', undefined, { id }, `"rev-${env.revision}"`)
    if (!await refreshAfterWrite()) { context.closeModal(true); return }
    if (state.env === id) context.selectScope(state.project, state.environments.find(e => e.project === state.project)?.id)
    context.closeModal(true); context.ui.page = 'environments'; context.updateHash(); context.render()
  }
  function wizardInput() {
    const w = context.workload.x.wizard
    if (!w) throw unsupportedError('missing-deployment-input')
    const env = inventory.raw.environments.find(e => e.id === w.env && e.project_id === w.project)
    if (!env || env.runtime_id !== w.runtime) throw unsupportedError('deployment-runtime-assignment')
    if (w.secretRef) throw unsupportedError('deployment-secret-reference')
    if (w.source === 'template' && w.raw !== template(w.template).raw) throw unsupportedError('catalog-content-override')
    // The archive preview represents these limits as comments. The API payload
    // must carry the reviewed values as executable Compose properties.
    const resolvedCompose = composeSource(w)
    const networkName = inventory.raw.runtimes?.find(r => r.id === w.runtime)?.runtime_type === 'swarm' ? w.networkName || '' : ''
    return { env, body: { ...(w.source === 'template' ? { catalog_app_version_id: w.template } : { compose_yaml: resolvedCompose, service_mode: 'create' }), runtime_env: w.runtimeEnv || '', network_name: networkName, deployment_plan: planFor(w) }, signature: fingerprint({ env: env.id, runtime: w.runtime, name: w.name, key: w.key, compose: resolvedCompose, runtimeEnv: w.runtimeEnv || '', networkName, deploymentPlan: planFor(w) }) }
  }
  async function saveWizard() {
    context.workload.collectWizard()
    const wizard = context.workload.x.wizard
    const env = inventory.raw.environments.find(value => value.id === wizard?.env && value.project_id === wizard?.project)
    if (!wizard || !env) throw unsupportedError('deployment-environment-assignment')
    const saved = { ...wizard, reviewAck: false, warningAck: false }
    await write('put', '/environments/{id}/deployment-draft', { data: saved }, { id: env.id }, inventory.versions.get(`deployment-draft:${env.id}`) || '"rev-0"')
    wizard.touched = false
    context.workload.x.wizard = null
    context.closeModal(true)
    await refreshAfterWrite()
    context.render()
    context.toast(i18n.global.t('integration.saved'), false)
  }
  async function discardWizard() {
    const wizard = context.workload.x.wizard
    const env = wizard?.env
    const etag = inventory.versions.get(`deployment-draft:${env}`)
    if (env && etag && etag !== '"rev-0"')
      await request('delete', '/environments/{id}/deployment-draft', { params: { path: { id: env } }, headers: { 'If-Match': etag } })
    context.workload.x.wizard = null
    context.closeModal(true)
    await refreshAfterWrite()
    context.render()
  }
  async function deployWizard() {
    const w = context.workload.x.wizard
    context.workload.collectWizard()
    if (!w?.reviewAck) throw new ApiRequestError(422, 'validation.failed')
    const savedReview = composeSession(w).review
    if (!savedReview || savedReview.signature !== composeSignature(w)) throw unsupportedError('expired-preflight')
    await checkCompose(w, state.runtimes.find(r => r.id === w.runtime)?.type === 'swarm')
    if (!deploymentReady(w)) throw new ApiRequestError(422, 'validation.failed')
    if (savedReview.signature !== composeSignature(w)) throw unsupportedError('expired-preflight')
    const candidate = wizardInput()
    review = { ...candidate, ...savedReview, signature: candidate.signature }
    if (!review || review.signature !== candidate.signature) throw unsupportedError('expired-preflight')
    const result = await write('post', composeSession(w).swarm && w.source !== 'template' ? '/environments/{id}/service-deployments' : '/environments/{id}/deployments', { preflight_id: review.id }, { id: review.env.id }, review.etag)
    const draftETag = inventory.versions.get(`deployment-draft:${review.env.id}`)
    if (draftETag && draftETag !== '"rev-0"') {
      try { await request('delete', '/environments/{id}/deployment-draft', { params: { path: { id: review.env.id } }, headers: { 'If-Match': draftETag } }) }
      catch (error) { context.toast(errorText(error), true) }
    }
    w.touched = false
    context.workload.x.wizard = null
    context.closeModal(true)
    if (await refreshAfterWrite()) context.workspace.showOperation(result.data.id)
    void pollOperation(result.data.id)
  }
  async function pollOperation(id) {
    await operations.track(id)
  }
  async function confirmOp() {
    const p = context.workload.x.pendingOp
    const service = inventory.raw.services.find(s => s.id === p?.service)
    if (!service || !checked('x-op-ack')) throw new ApiRequestError(422, 'validation.failed')
    const action = p.kind === 'stop' ? 'scale' : p.kind
    if (!['restart', 'scale', 'remove_runtime'].includes(action) || !service.lifecycle_operations?.includes(action)) throw unsupportedError(`service-${p.kind}`)
    const value = input('x-op-value')
    if (p.kind === 'restart' && !value || p.kind === 'scale' && (!Number.isInteger(Number(value)) || Number(value) < 0 || Number(value) > 10000)) throw new ApiRequestError(422, 'validation.failed')
    const replicas = p.kind === 'stop' ? 0 : Number(value)
    const persist = ['scale', 'stop'].includes(p.kind) && document.querySelector('input[name="x-op-persist"]:checked')?.value === 'source'
    const current = await request('get', '/services/{id}', { params: { path: { id: service.id } } })
    const path = action === 'restart' ? '/services/{id}/restart' : action === 'scale' ? '/services/{id}/scale' : '/services/{id}/operations'
    const body = action === 'scale' ? { replicas, persist } : action === 'remove_runtime' ? { action } : {}
    const result = await write('post', path, body, { id: service.id }, current.response.headers.get('ETag'))
    context.closeModal(true); if (await refreshAfterWrite()) context.workspace.showOperation(result.data.id)
    void pollOperation(result.data.id)
  }
  async function loadRestoreRevision(id, sequence) {
    const app = state.apps.find(a => a.id === id)
    const revision = app?.revisions.find(r => r.v === Number(sequence))
    if (!revision) throw unsupportedError('missing-application-revision')
    const result = await request('post', '/applications/{id}/revisions/{revisionID}/restore-preview', { params: { path: { id, revisionID: revision.backendId } } })
    const source = result.data.preview.resolved_yaml
    revision.spec = { resolved_yaml: source, composeBundle: { source, rendered: source }, services: parseYAML(source)?.services || {} }
    restoreReviews.set(revision.backendId, result.data)
  }
  async function saveRestore() {
    const selection = context.workload.x.restore
    const app = state.apps.find(a => a.id === selection?.app)
    const revision = app?.revisions.find(r => r.v === selection.version)
    if (!app || !revision || app.version !== selection.base) throw new ApiRequestError(412, 'conflict')
    const review = restoreReviews.get(revision.backendId)
    if (!review || Date.parse(review.expires_at) <= Date.now()) throw new ApiRequestError(412, 'conflict')
    app.draft = { base: app.version, sourceVersion: revision.v, review,
      spec: revision.spec, time: new Date().toISOString() }
    context.closeModal(true)
    context.workload.jump('application', app.id, 'history')
    context.toast(i18n.global.t('integration.saved'), false)
  }
  async function runDiagnostic() {
    const domain = state.domains.find(d => d.id === context.ui.domain)
    context.ui.diagUrl = input('diag-url') || context.ui.diagUrl
    context.ui.diagSource = input('diag-source') || context.ui.diagSource
    if (!domain || context.ui.diagSource !== 'target') throw unsupportedError('draft-or-gateway-route-trace')
    const url = new URL(context.ui.diagUrl)
    if (url.hostname !== domain.name || url.protocol !== 'http:' || url.port || domain.redirect_http_to_https) throw unsupportedError('route-trace-listener-semantics')
    const result = await request('get', '/gateway-pools/{id}/route-trace', { params: { path: { id: domain.pool_id }, query: { host: url.hostname, path: url.pathname } } })
    if (result.data.domain?.id && result.data.domain.id !== domain.id) throw unsupportedError('route-trace-domain-ownership')
    const route = domain.active.routes.find(r => r.id === result.data.route?.id)
    context.ui.diag = { error: !result.data.matched,
      title: i18n.global.t(result.data.matched ? 'integration.traceMatched' : 'integration.traceUnmatched'),
      note: i18n.global.t('integration.traceEvidence'),
      ...(route ? { route, uri: '—' } : {}),
    }
    context.redrawDiag()
  }
  async function previewDomainConfig({ poolID, domainID, serverSnippet, routes }) {
    const current = await request('get', '/gateway-pools/{id}/nginx', { params: { path: { id: poolID } }, signal: requests.signal })
    const saved = current.data.draft?.revision ? current.data.draft : current.data.active
    const selected = new Set(routes.map(route => route.id))
    const snippets = (saved.snippets || []).filter(snippet => !(snippet.scope === 'server' && snippet.resource_id === domainID) && !(snippet.scope === 'location' && selected.has(snippet.resource_id)))
    if (serverSnippet.trim()) snippets.push({ scope: 'server', resource_id: domainID, content: serverSnippet })
    for (const route of routes) if (route.snippet?.trim()) snippets.push({ scope: 'location', resource_id: route.id, content: route.snippet })
    const work = context?.ui.domain === domainID && context?.work ? context.work : { ipAccess: ipAccessFor(saved.access_controls, 'server', domainID), routes: routes.map(route => ({ ...route, ipAccess: ipAccessFor(saved.access_controls, 'location', route.id) })) }
    const changes = context?.ui.domain === domainID && context.work
      ? domainRouteChanges(domainID, state.domains.find(domain => domain.id === domainID)?.active.routes || [], work, inventory.raw?.routes || []) : undefined
    return (await request('post', '/gateway-pools/{id}/nginx/preview', { params: { path: { id: poolID } }, signal: requests.signal, body: { snippets, access_controls: domainAccessControls(saved.access_controls, domainID, state.domains?.find(d => d.id === domainID)?.active.routes || [], work), domain_id: domainID, ...(changes ? { routes: changes } : {}) } })).data
  }
  function ipAccessEditorState() {
    if (disposed || context?.ui.page !== 'domain' || context.ui.tab !== 'config' || !context.work || context.work.mode !== 'managed') return null
    const domain = state.domains.find(d => d.id === context.ui.domain)
    if (!domain) return null
    return { domainID: domain.id, domainName: domain.name, work: context.work }
  }
  function updateIPAccess(routeID, list, raw) {
    const editor = ipAccessEditorState()
    if (!editor || !['access', 'forbidden'].includes(list)) return
    const target = routeID ? editor.work.routes.find(r => r.id === routeID) : editor.work
    if (!target) return
    target.ipAccess ||= emptyIPAccess()
    target.ipAccess[list] = raw.split('\n').filter(line => line.trim() !== '')
    context.ui.validation = null
    context.render()
  }
  async function previewIPAccess() {
    const editor = ipAccessEditorState()
    if (!editor) return null
    const result = await previewDomainConfig({ poolID: state.domains.find(d => d.id === editor.domainID).pool_id, domainID: editor.domainID, serverSnippet: editor.work.serverSnippet, routes: editor.work.routes })
    return result.candidate.content
  }
  function updateClientIP(source) {
    const editor = ipAccessEditorState()
    if (!editor) return
    editor.work.ipAccess ||= emptyIPAccess()
    if (source) editor.work.ipAccess.clientIP = { header: source.header, trusted_proxies: [...source.trusted_proxies] }
    else delete editor.work.ipAccess.clientIP
    context.ui.validation = null
    context.render()
  }
  async function deployRestore(id) {
    const app = state.apps.find(a => a.id === id)
    const draft = app?.draft
    if (!draft || !checked('x-restore-ack')) throw new ApiRequestError(422, 'validation.failed')
    if (app.version !== draft.base || Date.parse(draft.review.expires_at) <= Date.now()) throw new ApiRequestError(412, 'conflict')
    const result = await write('post', '/applications/{id}/restore', { review_id: draft.review.id, acknowledge_data_boundary: true }, { id }, `"rev-${draft.review.base_revision}"`)
    app.draft = null
    context.closeModal(true); if (await refreshAfterWrite()) context.workspace.showOperation(result.data.id)
    void pollOperation(result.data.id)
  }
  const commands = { createProject, saveEnvironment, deleteEnvironment, saveVars, saveDoc, saveWizard, discardWizard, deployWizard, confirmOp, saveRestore, deployRestore, runDiagnostic }
  function locateFieldErrors(error) {
    if (!(error instanceof ApiRequestError)) return
    let first
    for (const field of error.fields) {
      const selector = ({
        '/name': '#p-env-name, #p-name, [data-wf="name"]',
        '/display_name': '#p-env-name',
        '/kind': '#p-env-kind',
        '/gateway_ids': '[name="p-gateway"]',
        '/slug': '#p-env-slug, #p-key, [data-wf="key"]',
        '/runtime_id': '[data-wf="runtime"]',
        '/default_runtime_id': '#p-runtime',
        '/compose_yaml': '[data-wf="raw"], #y-compose-code',
      })[field.pointer]
      const element = selector && document.querySelector(selector)
      if (!element) continue
      element.setAttribute('aria-invalid', 'true')
      first ||= element
    }
    first?.focus()
  }
  function command(name, args) {
    if (pending) return false
    if (!commands[name]) return unsupported(name)
    pending = Promise.resolve().then(() => commands[name](...args)).catch(error => {
      if (disposed) return
      const text = message(error)
      if (context.workload.x.wizard) { context.workload.x.wizardError = text; context.workload.renderWizard() }
      else context.formError(text)
      locateFieldErrors(error)
    }).finally(() => { pending = null; if (refreshQueued) scheduleResourceRefresh() })
    return false
  }
  async function beforeAction(event) {
    const el = event.target.closest?.('[data-action]')
    if (!context || !el || replay) return
    const action = el.dataset.action
    const needsPreflight = action === 'x-wnext' && context.workload.x.wizard?.step === 3
    const needsSave = action === 'y-guard-save' && (context.workspace.varsDirty() || context.workspace.docsDirty?.())
    const needsRevision = action === 'x-restore'
    const needsWizardData = ['x-install', 'x-install-file', 'x-install-image', 'x-template', 'x-resume-wizard'].includes(action) || action === 'x-wnext' && !!context.workload.x.wizard
    if (!needsPreflight && !needsSave && !needsRevision && !needsWizardData) return
    event.preventDefault(); event.stopImmediatePropagation()
    if (pending) return
    pending = Promise.resolve().then(async () => {
      try {
        if (needsWizardData) {
          if (context.workload.x.wizard) context.workload.collectWizard()
          const wizard = context.workload.x.wizard
          if (!await refresh(includeRemoved, { action: 'install', projectID: wizard?.project || state.project, environmentID: wizard?.env || state.env }, true)) return
        }
        if (needsRevision) await loadRestoreRevision(el.dataset.id, el.dataset.version || el.dataset.v)
        else if (needsPreflight) { context.workload.collectWizard(); wizardInput() }
        else if (needsSave) { await saveVars(true); await saveDoc(true) }
        replay = true; el.click(); replay = false
      } catch (error) {
        if (needsPreflight) { context.workload.x.wizardError = message(error); context.workload.renderWizard() }
        else context.formError(message(error))
        locateFieldErrors(error)
      } finally { pending = null; replay = false; if (refreshQueued) scheduleResourceRefresh() }
    })
  }
  window.addEventListener('click', beforeAction, true)
  const wizardTargetChanged = event => {
    if (!event.target.matches?.('[data-wf="project"], [data-wf="env"], [data-wf="runtime"]') || !context?.workload.x.wizard) return
    // The controller's document listener has already updated the in-memory
    // draft. Refresh its selected scope without replacing that draft.
    changeGeneration++
    void refresh(includeRemoved, undefined, true).catch(error => {
      if (!disposed && error.name !== 'AbortError') refreshFailure.value = error
    })
  }
  window.addEventListener('change', wizardTargetChanged)
  async function refreshDomainDependencies(domainID, endpointServiceIDs) {
    const applied = await refresh(includeRemoved, { domainID, endpointServiceIDs }, true)
    if (!applied) throw new ApiRequestError(409, 'frontend_capability_unavailable', { action: 'domain-validation-refresh' })
  }
  async function refreshThis() {
    try {
      const results = await Promise.allSettled([refresh(includeRemoved, undefined, true), refreshComponentQueries()])
      const failure = results.find(result => result.status === 'rejected')
      if (failure) throw failure.reason
      refreshFailure.value = undefined
      return results[0].value
    } catch (error) { if (!disposed) refreshFailure.value = error; return false }
  }
  function scheduleResourceRefresh() {
    if (!context || disposed || pending) return
    void refresh().catch(error => { if (!disposed && error.name !== 'AbortError') refreshFailure.value = error })
  }
  const resourcesChanged = event => {
    if (!context || disposed) return
    changeGeneration++
    const change = event.detail
    if (!change) invalidateWorkloadInventory()
    if (change?.reason === 'mutation' && change.operationID) void operations.track(change.operationID)
    refreshQueued = true
    scheduleResourceRefresh()
  }
  window.addEventListener('nsc:resources-changed', resourcesChanged)
  refreshActive = refreshThis
  retryResourceRefresh.value = refreshThis
  return { ipAccessEditorState, updateIPAccess, updateClientIP, previewIPAccess, state, composePage, timelinePage, domainHistoryPage, serviceLogsPage, previewDomainConfig, saveDomainDraft, refreshDomainDependencies, validatePoolDraft, readValidation, publishPoolDraft, readRelease, refresh: refreshThis, prepareRoute, get historyLoaded() { return includeRemoved }, async loadServiceHistory() { if (!includeRemoved) await refresh(true) }, templates, command, unsupported, applicationSpec, template, attach(value) { context = value; operations.resume(state.appDeployments); stopObservations?.(); stopObservations = watchRuntimeObservations({ state, context, signal: requests.signal }) }, dispose() {
    disposed = true; readGeneration++; navigationController?.abort(); refreshController?.abort(); stopObservations?.(); if (refreshActive === refreshThis) { refreshActive = undefined; retryResourceRefresh.value = undefined; refreshFailure.value = undefined }; requests.abort(); queryClient.clear(); window.removeEventListener('click', beforeAction, true); window.removeEventListener('change', wizardTargetChanged); window.removeEventListener('nsc:resources-changed', resourcesChanged)
  } }
}
