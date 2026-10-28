<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { allPages } from '../data/pagination'
import { useWorkspaceStore } from '../stores/workspace'
import { request, session } from '../shared/api/client'
import { translate as t } from '../i18n'
import { checkCompose, reviewCompose, composeSession, composeSource, composeSignature, composeDiagnostics, invalidateCompose, planFor, deploymentReady, composeFailure } from '../compose/session'
import { editPlacement, parseCompose } from '../compose/validator'

const emit = defineEmits(['editing'])
const store = useWorkspaceStore()
const wizard = computed(() => { void store.revision; return store.service?.workload.wizard })
const state = computed(() => wizard.value ? composeSession(wizard.value) : null)
const statefulTemplate = computed(() => { void store.revision; const w = wizard.value; return w?.source === 'template' && Object.values(parseCompose(w.swarmRaw || '').tree?.services || {}).some(s => s.volumes?.length) })
const swarm = computed(() => store.state().runtimes.some(r => r.id === wizard.value?.runtime && r.type === 'swarm'))
const diagnostics = computed(() => wizard.value ? composeDiagnostics(wizard.value) : [])
const services = computed(() => state.value?.result?.services || state.value?.local?.services || [])
const required = computed(() => new Set(diagnostics.value.filter(d => ['required', 'error'].includes(d.severity) || d.code === 'STORAGE_CAPABILITY_UNKNOWN').map(d => d.service || d.code)).size)
const warnings = computed(() => diagnostics.value.filter(d => d.severity === 'warning' && d.code !== 'STORAGE_CAPABILITY_UNKNOWN'))
const pendingWarnings = computed(() => warnings.value.filter(d => !planFor(wizard.value).acknowledged?.includes(d.id)).length)
const mounts = computed(() => services.value.reduce((n, s) => n + s.mounts.length, 0))
const active = ref(''), strategy = ref('keep'), node = ref(''), labelKey = ref(''), labelValue = ref(''), declarations = ref([]), replace = ref(false), dirty = ref(false), busy = ref(false), error = ref(''), pendingNavigation = ref(null), labelConfirm = ref(false), target = ref(null), summaryTarget = ref(null), headerTarget = ref(null)
let timer, previousFocus, previousScroll = 0
const selected = computed(() => services.value.find(s => s.name === active.value))
const runtimeID = computed(() => { void store.revision; return wizard.value?.runtime || '' })
const nodeQuery = useQuery({ queryKey: computed(() => ['deployment-nodes', runtimeID.value]),
  enabled: computed(() => swarm.value && !!runtimeID.value),
  queryFn: ({ signal }) => allPages('/runtimes/{id}/cluster/nodes', { id: runtimeID.value }, signal),
  refetchInterval: 10000, retry: false })
const nodes = computed(() => [...(nodeQuery.data.value || [])].sort((a, b) => Number(available(b)) - Number(available(a)) || Number(b.role === 'worker') - Number(a.role === 'worker') || a.name.localeCompare(b.name)))
const availableNodes = computed(() => nodes.value.filter(available))
const dataNodeReady = computed(() => !swarm.value || !statefulTemplate.value || !nodeQuery.isError.value && availableNodes.value.some(n => n.id === planFor(wizard.value).data_node_id))
const candidate = computed(() => {
  if (!wizard.value) return { source: '', conflicts: [] }
  const raw = composeSource(wizard.value)
  if (strategy.value === 'keep') return { source: raw, conflicts: [] }
  const constraint = strategy.value === 'node' ? `node.hostname == ${nodes.value.find(n => n.id === node.value)?.name || ''}` : `node.labels.${labelKey.value.trim()} == ${labelValue.value.trim()}`
  return editPlacement(raw, active.value, constraint, { replace: replace.value })
})
const draftPlan = computed(() => ({ ...planFor(wizard.value || {}), storage: declarations.value, acknowledged: [] }))
const preview = ref(null)
let previewSequence = 0
function available(n) { return n.state === 'ready' && n.availability === 'active' && !n.stale && !n.virtual }
function message(d) { return [t(`compose.${d.code}`), d.pointer || d.path?.join('.'), d.reason].filter(Boolean).join(' · ') }
function serviceIssues(name) { return diagnostics.value.filter(d => d.service === name) }
function portIssue(name) { return serviceIssues(name).find(d => d.path?.[2] === 'ports') }
function configureService(name) { if (wizard.value.source === 'template' && wizard.value.swarmRaw) return; const issue = portIssue(name); if (issue) locate(issue); else open(name) }
function declaration(m) { return declarations.value.find(d => d.resource === m.key && d.fingerprint === m.fingerprint) }
function chooseCapability(m, capability) {
  declarations.value = declarations.value.filter(d => d.resource !== m.key)
  declarations.value.push({ resource: m.key, fingerprint: m.fingerprint, capability, confirmed: false })
  dirty.value = true
}
function confirmed(m, value) { const d = declaration(m); if (d) d.confirmed = value; dirty.value = true }
function changed() { dirty.value = true; replace.value = false }
function syncButtons() {
  const w = wizard.value
  if (!w) return
  const button = document.getElementById('x-w-primary')
  const issues = w.step === 4 ? store.service.deploymentWizard.issues(w) : null
  if (button) button.disabled = !dataNodeReady.value || state.value.reviewPending || state.value.pending || !state.value.local?.schema_valid || w.step === 4 && (!deploymentReady(w) || state.value.review?.signature !== composeSignature(w) || !w.reviewAck || !!issues.errors.length || !!issues.warnings.length && !w.warningAck)
}
function selectDataNode(event) {
 const w=wizard.value
 w.deploymentPlan={...planFor(w),data_node_id:event.target.value}
 w.touched=true
 schedule(true)
}
watch(runtimeID, (current, previous) => {
  if (previous && current !== previous && wizard.value?.deploymentPlan?.data_node_id) {
    wizard.value.deploymentPlan = { ...planFor(wizard.value), data_node_id: '' }
    schedule(true)
  }
})
watch([availableNodes, statefulTemplate, () => nodeQuery.isError.value], () => {
  const w = wizard.value
  if (w && swarm.value && statefulTemplate.value && !nodeQuery.isError.value && w.step < 4 && !planFor(w).data_node_id && availableNodes.value.length === 1) {
    w.deploymentPlan = { ...planFor(w), data_node_id: availableNodes.value[0].id }
    schedule(true)
  }
  syncButtons()
})
function selectTemplate() {
 const w=wizard.value
 if (!w || w.source!=='template') return
 const template=store.service?.marketTemplates?.()?.find(t=>t.id===w.template)
 if (template) {if (w.selectedTemplate !== w.template) {w.raw=template.raw || '';w.selectedTemplate=w.template} w.swarmRaw=template.swarmRaw || '';w.catalogVariables=template.catalogVariables || []}
 w.swarmTemplate=swarm.value
 if (!swarm.value || !Object.values(parseCompose(w.swarmRaw || '').tree?.services || {}).some(s => s.volumes?.length)) w.deploymentPlan={...planFor(w),data_node_id:''}
}
async function validate() {
 selectTemplate()
  const w = wizard.value
  if (!w) return
  await checkCompose(w, swarm.value)
  if (wizard.value !== w) return
  if (!state.value.editing) await reviewCompose(w)
  if (wizard.value !== w) return
  // Validation may finish while a field is being edited. Capture live controls
  // before replacing the wizard's frozen VNode content.
  store.service?.deploymentWizard.collect()
  const focused = document.activeElement
  const cursor = focused?.selectionStart, end = focused?.selectionEnd
  const controlID = focused?.id
  store.service?.deploymentWizard.render()
  await nextTick(); destinations(); syncButtons()
  if (controlID && focused?.matches?.('[data-wf]')) {
    const control = document.getElementById(controlID)
    control?.focus()
    if (typeof cursor === 'number') control?.setSelectionRange(cursor, end)
  }
}
function changeTarget() {
  wizard.value.step = 2
  store.service.deploymentWizard.render()
  nextTick(() => { destinations(); document.getElementById('x-w-project')?.focus() })
}
function schedule(immediate = false) {
  if (!wizard.value) return
  clearTimeout(timer); invalidateCompose(wizard.value); syncButtons()
  timer = setTimeout(() => { timer = null; void validate() }, immediate ? 0 : 400)
}
function destinations() {
  target.value = document.querySelector('.deployment-wizard-analysis') || document.querySelector('.x-wizard-grid > div:first-child')
  summaryTarget.value = document.querySelector('.x-summary')
  headerTarget.value = document.querySelector('.modal-head')
}
function input(event) {
  const el = event.target
  if (!wizard.value || !el.matches?.('[data-wf], #deployment-network-name, #deployment-entry-certificate')) return
  selectTemplate()
  if (['reviewAck', 'warningAck'].includes(el.dataset.wf)) return
  store.service?.deploymentWizard.collect()
  if (event.type === 'change' && state.value.signature === composeSignature(wizard.value)) return
  schedule(event.type === 'change')
}
function locate(d) {
  if (active.value) return
  if (d.path?.[2] === 'ports' && state.value.local?.schema_valid) {
    wizard.value.step = 3; store.service.deploymentWizard.render()
    nextTick(() => window.dispatchEvent(new CustomEvent('nsc:edit-port', { detail: { service: d.service, index: d.path[3] } })))
    return
  }
  wizard.value.step = 1; store.service.deploymentWizard.render()
  nextTick(() => {
    const editor = document.getElementById('x-w-raw')
    const doc = parseCompose(wizard.value.raw).doc
    const n = doc.getIn(d.path || [], true)
    const offset = d.offset ?? n?.range?.[0] ?? 0
    editor?.focus(); editor?.setSelectionRange(offset, d.end ?? n?.range?.[1] ?? offset)
    editor?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  })
}
function open(name) {
 if (wizard.value.source === 'template' && wizard.value.swarmRaw) return;
  if (busy.value) return
  if (dirty.value) { pendingNavigation.value = name; return }
  enter(name)
}
function enter(name) {
  if (!active.value) { previousFocus = document.activeElement; previousScroll = document.querySelector('.modal-body')?.scrollTop || 0 }
  active.value = name; strategy.value = 'keep'; node.value = ''; labelKey.value = ''; labelValue.value = ''; replace.value = false; error.value = ''; preview.value = null; labelConfirm.value = false
  declarations.value = JSON.parse(JSON.stringify(planFor(wizard.value).storage || []))
  dirty.value = false; pendingNavigation.value = null
  state.value.editing = !!name; emit('editing', !!name)
  if (!name && wizard.value.step === 4) void reviewCompose(wizard.value).then(() => { store.service?.deploymentWizard.render(); syncButtons() })
  if (!name) nextTick(() => {
    const body = document.querySelector('.modal-body'); if (body) body.scrollTop = previousScroll
    const focus = previousFocus?.isConnected ? previousFocus : previousFocus?.id ? document.getElementById(previousFocus.id) : [...document.querySelectorAll('[data-compose-service]')].find(el => el.dataset.composeService === previousFocus?.dataset.composeService)
    focus?.focus()
  })
}
function close() { if (busy.value) return; if (dirty.value) pendingNavigation.value = ''; else enter('') }
async function previewDraft() {
  const sequence = ++previewSequence
  if (!active.value || candidate.value.error) { preview.value = null; return }
  try {
    const response = await request('post', '/environments/{id}/compose-validation', { params: { path: { id: wizard.value.env } }, body: { compose_yaml: candidate.value.source, network_name: swarm.value ? wizard.value.networkName || '' : '', deployment_plan: draftPlan.value } })
    if (sequence === previewSequence) preview.value = response.data
  } catch (failure) { if (sequence === previewSequence) { preview.value = null; const problem = composeFailure(failure); error.value = t(problem.key, problem.params) } }
}
watch([candidate, draftPlan], () => { previewSequence++; preview.value = null; clearTimeout(previewTimer); previewTimer = setTimeout(() => void previewDraft(), 400) }, { deep: true })
let previewTimer
async function apply(next = false) {
  if (busy.value || candidate.value.error) return
  busy.value = true; error.value = ''
  try {
    await previewDraft()
    if (!preview.value || preview.value.diagnostics.some(d => d.service === active.value && (d.severity === 'error' || d.severity === 'required' || d.code === 'STORAGE_CAPABILITY_UNKNOWN'))) { error.value = t('compose.resolveFirst'); return }
    const w = wizard.value, name = active.value
    w.raw = candidate.value.source; w.deploymentPlan = JSON.parse(JSON.stringify(draftPlan.value)); w.touched = true
    invalidateCompose(w)
    dirty.value = false
    const destination = pendingNavigation.value
    await validate()
    if (destination !== null) enter(destination)
    else if (next) enter(services.value.find(s => s.name !== name && serviceIssues(s.name).some(d => d.severity === 'required' || d.code === 'STORAGE_CAPABILITY_UNKNOWN'))?.name || '')
    else enter('')
  } finally { busy.value = false }
}
async function acknowledge(event) {
  if (busy.value) return
  busy.value = true
  const w = wizard.value
  w.deploymentPlan = { ...planFor(w), acknowledged: event.target.checked ? warnings.value.map(d => d.id) : [] }
  invalidateCompose(w, { keepAcknowledged: true })
  try { await validate() } finally { busy.value = false }
}
async function addLabel() {
  const n = nodes.value.find(n => n.id === node.value)
  if (!n || !labelKey.value.trim() || !labelValue.value.trim() || busy.value) return
  busy.value = true
  try {
    await request('put', '/runtimes/{id}/cluster/nodes/{nodeId}/labels', { params: { path: { id: wizard.value.runtime, nodeId: n.id } }, headers: { 'If-Match': `"rev-${n.version}"` }, body: { labels: Object.entries({ ...n.labels, [labelKey.value.trim()]: labelValue.value.trim() }).map(([key, value]) => ({ key, value })) } })
    labelConfirm.value = false; await validate(); await previewDraft()
  } catch { error.value = t('compose.labelFailed') } finally { busy.value = false }
}
watch([wizard, () => { void store.revision; const w = wizard.value; return w ? composeSignature(w) + ':' + w.step : '' }], () => { destinations(); if (wizard.value && state.value.signature !== composeSignature(wizard.value) && !busy.value && !timer) schedule(true); else if (wizard.value?.step === 4 && !state.value.editing) void reviewCompose(wizard.value).then(() => { store.service?.deploymentWizard.render(); syncButtons() }) }, { flush: 'post' })
function composeChanged() { schedule(true) }
onMounted(() => { destinations(); window.addEventListener('nsc:compose-change', composeChanged); window.addEventListener('nsc:close-compose-workspace', close); document.addEventListener('input', input); document.addEventListener('change', input); schedule(true) })
onBeforeUnmount(() => { clearTimeout(timer); clearTimeout(previewTimer); previewSequence++; window.removeEventListener('nsc:compose-change', composeChanged); window.removeEventListener('nsc:close-compose-workspace', close); document.removeEventListener('input', input); document.removeEventListener('change', input); if (state.value) state.value.sequence++ })
</script>

<template>
  <template v-if="wizard">
    <Teleport v-if="headerTarget" :to="headerTarget"><div class="compose-mobile-risk">{{ t('compose.required') }}: {{ required }} · {{ t('compose.warnings') }}: {{ pendingWarnings }}<span v-if="state.error"> · {{ t('compose.checkFailed') }}</span><span v-else-if="state.pending"> · {{ t('compose.checking') }}</span></div></Teleport>
    <Teleport v-if="summaryTarget && !active" :to="summaryTarget">
      <div class="compose-summary"><p>{{ t('compose.mounts') }}: {{ mounts }}</p><button class="btn ghost sm" @click="services.find(s => serviceIssues(s.name).length) && configureService(services.find(s => serviceIssues(s.name).length).name)">{{ t('compose.required') }}: {{ required }}</button><p>{{ t('compose.warnings') }}: {{ pendingWarnings }}</p><p v-if="state.error">{{ t('compose.checkFailed') }}</p><p v-else-if="state.pending || state.reviewPending">{{ t('compose.checking') }}</p></div>
    </Teleport>
    <Teleport v-if="!active" :to="target || 'body'" :disabled="!target">
      <section class="compose-analysis" data-testid="compose-analysis" aria-live="polite">
        <div v-if="swarm && statefulTemplate" class="callout blue" data-testid="market-data-node">
          <label for="market-data-node">{{ t('compose.dataNode') }}<select id="market-data-node" :value="planFor(wizard).data_node_id || ''" :disabled="wizard.step === 4 || nodeQuery.isPending.value || nodeQuery.isError.value" @change="selectDataNode"><option value="">{{ t('compose.chooseNode') }}</option><option v-if="planFor(wizard).data_node_id && !nodes.some(n => n.id === planFor(wizard).data_node_id)" :value="planFor(wizard).data_node_id" disabled>{{ planFor(wizard).data_node_id }} · {{ t('compose.nodeUnavailable') }}</option><option v-for="n in nodes" :key="n.id" :value="n.id" :disabled="!available(n)">{{ n.name }} · {{ n.state }} · {{ n.availability }}{{ n.stale ? ` · ${t('compose.nodeStale')}` : '' }}{{ n.id === availableNodes[0]?.id && availableNodes.length > 1 ? ` · ${t('compose.nodeRecommended')}` : '' }}</option></select></label>
          <p v-if="nodeQuery.isPending.value">{{ t('compose.nodesLoading') }}</p>
          <p v-else-if="nodeQuery.isError.value" role="alert">{{ t('compose.nodesFailed') }} <button class="btn sm" @click="nodeQuery.refetch()">{{ t('compose.retry') }}</button></p>
          <p v-else-if="!availableNodes.length">{{ t('compose.nodesEmpty') }}</p>
          <p v-else-if="planFor(wizard).data_node_id && !dataNodeReady" role="alert">{{ t('compose.nodeUnavailable') }}</p>
          <p>{{ t('compose.dataNodeHint') }}</p>
        </div>
        <div class="between"><h3>{{ t('compose.analysis') }}</h3><span>{{ t('compose.required') }}: {{ required }} · {{ t('compose.warnings') }}: {{ pendingWarnings }}</span></div>
        <p v-if="state.pending || state.reviewPending">{{ t('compose.checking') }}</p>
        <p v-if="state.error" class="callout red">{{ t(state.error.key, state.error.params) }} <button v-if="state.error.code === 'system.project_protected'" class="btn sm" @click="changeTarget">{{ t('compose.changeTarget') }}</button><button v-else class="btn sm" @click="validate">{{ t('compose.retry') }}</button></p>
        <ul v-if="diagnostics.some(d => !d.service || !state.local?.schema_valid)"><li v-for="d in diagnostics.filter(d => !d.service || !state.local?.schema_valid)" :key="d.id"><button class="diagnostic-link" @click="locate(d)">{{ message(d) }} · {{ d.line || '—' }}:{{ d.col || d.column || '—' }}</button></li></ul>
        <div v-for="s in services.filter(s => serviceIssues(s.name).length)" :key="s.name" class="compose-service">
          <div class="between"><strong>{{ s.name }}</strong><button class="btn sm" :data-compose-service="s.name" :disabled="state.pending || !state.result" @click="configureService(s.name)">{{ t(portIssue(s.name) ? 'ports.edit' : 'compose.configure') }}</button></div>
          <p v-for="m in s.mounts" :key="m.index">{{ m.source || t('compose.anonymous') }} → {{ m.target }} · {{ m.type }}</p>
          <p v-for="d in serviceIssues(s.name)" :key="d.id" :class="['diagnostic', d.severity]"><button class="diagnostic-link" @click="locate(d)">{{ message(d) }} <small>{{ d.pointer }}</small></button></p>
        </div>
        <details v-if="services.some(s => !serviceIssues(s.name).length)"><summary>{{ t('compose.checkedServices') }}</summary><div v-for="s in services.filter(s => !serviceIssues(s.name).length)" :key="s.name" class="compose-service"><strong>{{ s.name }}</strong> · {{ s.matched_nodes?.map(n => n.name).join(', ') || (s.mounts.some(m => m.capability === 'shared') ? t('compose.sharedDeclared') : t('compose.noConstraint')) }} <button v-if="s.mounts.length || s.constraints.length" class="btn sm" :disabled="state.pending || !state.result" @click="open(s.name)">{{ t('compose.configure') }}</button></div></details>
        <label v-if="warnings.length" class="compose-ack"><input type="checkbox" :checked="warnings.every(d => planFor(wizard).acknowledged?.includes(d.id))" :disabled="state.pending" @change="acknowledge">{{ t('compose.ackWarnings') }}</label>
        <div v-if="wizard.step === 4" class="compose-stages"><p>YAML: {{ state.local?.syntax_valid ? '✓' : '…' }} · Compose Schema: {{ state.local?.schema_valid ? '✓' : '…' }}</p><p v-for="check in ['environmentCheck', 'networkCheck', 'imageCheck']" :key="check">{{ t(`compose.${check}`) }}: {{ state.review?.signature === composeSignature(wizard) ? '✓' : '…' }}</p></div>
        <p v-if="wizard.step === 4 && (required || state.pending || state.error)" class="callout amber">{{ t('compose.resolveFirst') }}</p>
      </section>
    </Teleport>
    <section v-if="active && selected" class="compose-workspace" data-testid="compose-workspace">
      <header class="between"><div><h3>{{ t('compose.constraints') }}</h3><p>{{ active }} · {{ swarm ? 'Swarm Service' : 'Docker Compose' }}</p></div><button class="btn ghost" :disabled="busy" @click="close">{{ t('compose.back') }}</button></header>
      <div class="compose-workspace-grid">
        <nav :aria-label="t('compose.services')"><button v-for="s in services" :key="s.name" :class="['compose-service-choice', { active: s.name === active }]" :disabled="busy" @click="open(s.name)">{{ s.name }} <small>{{ serviceIssues(s.name).length }}</small></button></nav>
        <div>
          <label class="compose-mobile-select">{{ t('compose.services') }}<select :value="active" :disabled="busy" @change="open($event.target.value)"><option v-for="s in services" :key="s.name" :value="s.name">{{ s.name }}</option></select></label>
          <h4>{{ t('compose.storage') }}</h4>
          <article v-for="m in selected.mounts" :key="m.index" class="compose-storage">
            <strong>{{ m.source || t('compose.anonymous') }}</strong><p>→ {{ m.target }} · {{ m.type }}</p>
            <p v-if="m.type === 'bind'" class="hint">{{ t('compose.bindHint') }}</p>
            <p v-else-if="m.capability !== 'ephemeral'" class="hint">{{ t('compose.localHint') }}</p>
            <template v-if="m.capability !== 'ephemeral'"><label>{{ t('compose.capability') }}<select :value="declaration(m)?.capability || (m.declared ? m.capability : '')" :disabled="busy" @change="chooseCapability(m, $event.target.value)"><option value="">{{ t('compose.automatic') }} · {{ t(`compose.${m.capability}`) }}</option><option value="local">{{ t('compose.local') }}</option><option value="shared">{{ t('compose.shared') }}</option></select></label><label v-if="declaration(m)?.capability === 'shared'" class="compose-ack"><input type="checkbox" :checked="declaration(m).confirmed" :disabled="busy" @change="confirmed(m, $event.target.checked)">{{ t('compose.sharedConfirm') }}</label></template>
          </article>
          <template v-if="swarm">
            <h4>{{ t('compose.scheduling') }}</h4><ul v-if="selected.constraints.length"><li v-for="c in selected.constraints" :key="c"><code>{{ c }}</code></li></ul>
            <div class="compose-strategies"><label v-for="v in ['keep', 'node', 'label']" :key="v"><input v-model="strategy" type="radio" :value="v" :disabled="busy" @change="changed">{{ t(`compose.strategy_${v}`) }}</label></div>
            <label v-if="strategy === 'node'">{{ t('compose.targetNode') }}<select v-model="node" :disabled="busy" @change="changed"><option value="">{{ t('compose.chooseNode') }}</option><option v-for="n in nodes" :key="n.id" :value="n.id" :disabled="!available(n)">{{ n.name }} · {{ n.state }} · {{ n.availability }} · {{ n.role }}</option></select></label>
            <div v-if="strategy === 'label'" class="compose-labels"><label>{{ t('compose.labelKey') }}<input v-model="labelKey" :disabled="busy" @input="changed"></label><label>{{ t('compose.labelValue') }}<input v-model="labelValue" :disabled="busy" @input="changed"></label><button v-if="session.role === 'administrator'" class="btn sm" :disabled="busy || !labelKey || !labelValue" @click="labelConfirm = !labelConfirm">{{ t('compose.addLabel') }}</button><div v-if="labelConfirm" class="callout amber"><p>{{ t('compose.labelConfirm') }} <code>{{ labelKey }}={{ labelValue }}</code></p><select v-model="node"><option value="">{{ t('compose.chooseNode') }}</option><option v-for="n in nodes" :key="n.id" :value="n.id" :disabled="!available(n)">{{ n.name }}</option></select><button class="btn" :disabled="busy || !node" @click="addLabel">{{ t('compose.confirmLabel') }}</button></div></div>
          </template>
          <div v-if="candidate.conflicts.length" class="callout amber"><p>{{ t('compose.PLACEMENT_CONSTRAINT_CONFLICT') }}</p><ul><li v-for="c in candidate.conflicts" :key="c"><code>{{ c }}</code></li></ul><button class="btn" @click="replace = true">{{ t('compose.replace') }}</button><button class="btn" @click="strategy = 'keep'">{{ t('compose.keep') }}</button><button class="btn" @click="enter(active)">{{ t('console.cancel') }}</button></div>
          <p v-if="preview?.cluster_checked">{{ t('compose.matching') }}: {{ preview.services.find(s => s.name === active)?.matched_nodes.map(n => n.name).join(', ') || '0' }}</p>
          <p v-for="d in preview?.diagnostics.filter(d => d.service === active) || []" :key="d.id" :class="['diagnostic', d.severity]">{{ message(d) }}</p>
          <details v-if="candidate.source !== composeSource(wizard)" open><summary>{{ t('compose.changes') }}</summary><div class="compose-diff"><div><h5>{{ t('compose.before') }}</h5><pre>{{ composeSource(wizard) }}</pre></div><div><h5>{{ t('compose.after') }}</h5><pre>{{ candidate.source }}</pre></div></div></details>
          <p v-for="d in declarations.filter(d => d.capability === 'shared')" :key="d.resource">{{ t('compose.sharedDeclared') }}: {{ d.resource }} · {{ d.confirmed ? '✓' : '…' }}</p>
          <p v-if="error" class="callout red" role="alert">{{ error }}</p>
        </div>
      </div>
      <div v-if="pendingNavigation !== null" class="callout amber"><p>{{ t('compose.unsavedWorkspace') }}</p><button class="btn" :disabled="busy" @click="apply(false)">{{ t('compose.save') }}</button><button class="btn" :disabled="busy" @click="enter(pendingNavigation)">{{ t('compose.discard') }}</button><button class="btn" @click="pendingNavigation = null">{{ t('compose.continueEditing') }}</button></div>
      <footer class="compose-actions"><button class="btn" :disabled="busy" @click="close">{{ t('console.cancel') }}</button><button class="btn" :disabled="busy || !!candidate.error" @click="apply(false)">{{ t('compose.apply') }}</button><button class="btn primary" :disabled="busy || !!candidate.error" @click="apply(true)">{{ t('compose.applyNext') }}</button></footer>
    </section>
  </template>
</template>

<style scoped>
.compose-mobile-risk { display: none; }
@media(max-width:760px) { .compose-mobile-risk { display:block; flex-basis:100%; font-size:12px; padding-top:8px; } :global(.modal-head:has(.compose-mobile-risk)) { flex-wrap:wrap; } }

.compose-analysis { border: 1px solid var(--line); border-radius: 9px; padding: 16px; margin-top: 18px; }
.compose-analysis h3,.compose-workspace h3 { margin: 0 0 8px; }
.compose-analysis .between { gap: 12px; flex-wrap: wrap; }
[data-testid="market-data-node"] { display: grid; grid-template-columns: minmax(0,1fr) minmax(0,1.3fr); align-items: start; gap: 10px; }
[data-testid="market-data-node"] label { display: grid; gap: 7px; min-width: 0; }
[data-testid="market-data-node"] select { width: 100%; min-width: 0; }
[data-testid="market-data-node"] p { margin: 0; overflow-wrap: anywhere; }
.compose-service { border-bottom: 1px solid var(--line); padding: 12px 0; }
.compose-service p { margin: 7px 0; overflow-wrap: anywhere; }
.diagnostic.error,.diagnostic.required { color: var(--red); }
.diagnostic.warning { color: var(--amber); }
.diagnostic-link { color: inherit; background: transparent; border: 0; padding: 0; text-align: left; cursor: pointer; }
.diagnostic-link small { display: block; overflow-wrap: anywhere; }
.compose-summary { position: sticky; top: 0; border-top: 1px solid var(--line); padding-top: 10px; }
.compose-workspace-grid { display: grid; grid-template-columns: 180px minmax(0,1fr); gap: 24px; margin-top: 20px; }
.compose-workspace h4 { margin: 20px 0 10px; }
.compose-service-choice { width: 100%; padding: 14px; margin-bottom: 6px; border: 1px solid var(--line); border-radius: 7px; background: transparent; color: var(--text); text-align: left; cursor: pointer; }
.compose-service-choice.active { border-color: var(--green); background: var(--green-soft); }
.compose-storage { border: 1px solid var(--line); border-radius: 8px; padding: 14px; margin: 10px 0; overflow-wrap: anywhere; }
.compose-storage select,.compose-workspace select,.compose-labels input { display: block; width: 100%; margin: 8px 0; }
.compose-ack { display: flex; align-items: flex-start; gap: 9px; margin: 12px 0; }
.compose-ack input,.compose-strategies input { width: auto; flex-shrink: 0; }
.compose-strategies { display: flex; flex-wrap: wrap; gap: 14px; margin: 16px 0; }
.compose-strategies label { padding: 10px; border: 1px solid var(--line); border-radius: 6px; }
.compose-actions { position: sticky; bottom: -21px; display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 8px; padding: 16px 0; margin-top: 20px; background: var(--panel, var(--bg)); border-top: 1px solid var(--line); }
.compose-diff { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.compose-diff pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 280px; overflow: auto; background: var(--code); color: #d4e1eb; padding: 12px; }
.compose-mobile-select { display: none; }
@media (max-width:800px) { .compose-workspace-grid,.compose-diff { grid-template-columns: 1fr; } .compose-workspace nav { display: none; } .compose-mobile-select { display: block; } .compose-analysis > .between { position: sticky; top: -21px; background: var(--panel, var(--bg)); padding: 10px 0; z-index: 1; } }
@media (max-width:700px) { [data-testid="market-data-node"] { grid-template-columns: minmax(0,1fr); } }
</style>
