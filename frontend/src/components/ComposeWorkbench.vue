<script setup>
import { computed, ref, watch, onBeforeUnmount, onMounted } from 'vue'
import { translate as t } from '../i18n'
import { readableCompose, diffSource, lineDiff, serviceChanges } from '../compose/source'
import { parseCompose } from '../compose/validator'
import { errorText } from '../shared/api/client'
import { request, session } from '../shared/api/client'
import { parseDocument } from 'yaml'

const props = defineProps({ editor: { type: Object, required: true }, application: { type: Object, required: true }, document: { type: Object, required: true }, save: { type: Function, required: true }, review: { type: Function, required: true }, analyze: { type: Function, required: true }, merge: { type: Function, required: true }, deploy: { type: Function, required: true }, refreshBaseline: { type: Function, default: null }, selectService: { type: Function, default: null }, services: { type: Array, default: () => [] } })
const emit = defineEmits(['edit', 'view'])
const source = ref(props.editor.composeWork ?? props.document.source)
const runtime = ref(props.editor.runtimeWork ?? props.document.runtime)
const sourceRevision = ref(props.document.draft?.sourceRevision || props.document.sourceRevision)
const deployedSource = ref(props.document.source)
const tab = ref(props.editor.view || 'source'), baseline = ref('active'), expanded = ref(false)
const validation = ref(parseCompose(source.value)), pending = ref(false), busy = ref(false), error = ref(''), reviewed = ref(null), acknowledge = ref(false)
const analysis = ref(null), rendered = ref(null), warningsAccepted = ref(false)
const gutter = ref(null)
const codeInput = ref(null)
const dockerResources = ref([])
const selectedResource = ref('')
const selectedService = ref('')
const dockerResourceError = ref('')
const lineNumbers = computed(() => Array.from({ length: source.value.split('\n').length }, (_, index) => index + 1))
const errorLines = computed(() => new Set([...validation.value.diagnostics, ...(analysis.value?.diagnostics || [])].filter(issue => issue.severity === 'error' || issue.severity === 'required').map(issue => issuePosition(issue).line).filter(Boolean)))
const pendingService = ref(null)
const plan = computed(() => ({ storage: props.document.sourcePlan?.storage || [], acknowledged: warningsAccepted.value ? (analysis.value?.diagnostics || []).filter(d => d.severity === 'warning' && d.code !== 'STORAGE_CAPABILITY_UNKNOWN').map(d => d.id) : [] }))
let timer, generation = 0
async function analyzeSource() {
 const current = generation
 if (!validation.value.schema_valid) { pending.value = false; return }
 try {
  const result = await props.analyze(source.value, runtime.value, plan.value)
  if (current === generation) { analysis.value = result.validation; rendered.value = result.rendered_yaml }
 } catch (e) {
  if (current === generation) { error.value = errorText(e); analysis.value = null; rendered.value = null }
 } finally { if (current === generation) pending.value = false }
}
onMounted(() => { pending.value = true; void analyzeSource(); void loadDockerResources() })
async function loadDockerResources() {
 if (!props.application?.runtime_id || session.value.role !== 'administrator') return
 try {
  const rt = await request('get', '/runtimes/{id}', { params: { path: { id: props.application.runtime_id } } })
  if (rt.data?.runtime_type !== 'swarm') return
  const values = []; let cursor = ''
  do {
   const page = await request('get', '/runtimes/{id}/docker-resources', { params: { path: { id: props.application.runtime_id }, query: { limit: 200, ...(cursor ? { cursor } : {}) } } })
   values.push(...(page.data || []).filter(item => item.owned))
   cursor = page.pagination?.next_cursor || ''
  } while (cursor)
  dockerResources.value = values
 } catch (cause) { dockerResourceError.value = errorText(cause) }
}
function insertDockerResource() {
 const item = dockerResources.value.find(value => `${value.kind}:${value.id}` === selectedResource.value)
 if (!item || !selectedService.value) return
 const doc = parseDocument(source.value, { uniqueKeys: true })
 if (doc.errors.length || !doc.hasIn(['services', selectedService.value])) return
 const plural = item.kind === 'config' ? 'configs' : 'secrets'
 doc.setIn([plural, item.name], { external: true })
 const selected = doc.toJS()?.services?.[selectedService.value]?.[plural] || []
 if (!Array.isArray(selected) || selected.some(value => (typeof value === 'string' ? value : value?.source) === item.name)) return
 doc.setIn(['services', selectedService.value, plural], [...selected, item.name])
 source.value = String(doc)
 selectedResource.value = ''
}
const base = computed(() => baseline.value === 'draft' ? props.document.draft?.source ?? deployedSource.value : deployedSource.value)
const changes = computed(() => serviceChanges(base.value, source.value))
const rows = computed(() => lineDiff(reviewed.value?.preview.before_resolved_yaml ?? diffSource(base.value), reviewed.value?.preview.resolved_yaml ?? diffSource(source.value)))
const pairedRows = computed(() => {
 const paired = []; let removed = [], added = []
 const flush = () => { for (let i = 0; i < Math.max(removed.length, added.length); i++) paired.push({ left: removed[i], right: added[i] }); removed = []; added = [] }
 for (const row of visibleRows.value) { if (row.type === 'same') { flush(); paired.push({ left: row, right: row }) } else if (row.type === 'remove') removed.push(row); else added.push(row) }
 flush(); return paired
})
const visibleRows = computed(() => {
 if (expanded.value) return rows.value
 const visible = new Set()
 rows.value.forEach((row, index) => { if (row.type !== 'same') for (let j = Math.max(0, index - 3); j <= Math.min(rows.value.length - 1, index + 3); j++) visible.add(j) })
 return rows.value.filter((_, i) => visible.has(i))
})
const canReview = computed(() => !busy.value && !pending.value && validation.value.schema_valid && analysis.value && !analysis.value.diagnostics.some(d => ['error', 'required'].includes(d.severity) || d.code === 'STORAGE_CAPABILITY_UNKNOWN') && (!analysis.value.diagnostics.some(d => d.severity === 'warning') || warningsAccepted.value) && props.application.managed)
watch([source, runtime], () => {
 emit('edit', source.value, runtime.value)
 generation++; warningsAccepted.value = false; analysis.value = null; rendered.value = null; reviewed.value = null; acknowledge.value = false; error.value = ''
 validation.value = parseCompose(source.value)
 pending.value = validation.value.schema_valid
 clearTimeout(timer)
 if (pending.value) timer = setTimeout(() => { void analyzeSource() }, 400)
}, { flush: 'sync' })
watch(tab, value => { emit('view', value) })
watch(() => props.document, (latest, previous) => {
 // Clean editors follow fresh server data. A dirty editor keeps its source and
 // comparison revision until the user explicitly refreshes its baseline.
 if (source.value !== (previous.draft?.source ?? previous.source) || runtime.value !== (previous.draft?.runtime ?? previous.runtime ?? '')) return
 sourceRevision.value = latest.draft?.sourceRevision || latest.sourceRevision
 deployedSource.value = latest.source
 source.value = latest.draft?.source ?? latest.source
 runtime.value = latest.draft?.runtime ?? latest.runtime ?? ''
})
onBeforeUnmount(() => { clearTimeout(timer); generation++ })
async function attempt(work) { if (busy.value) return; busy.value = true; error.value = ''; try { await work() } catch (e) { error.value = errorText(e) } finally { busy.value = false } }
async function reloadBaseline() { return attempt(async () => { const latest = await props.refreshBaseline(); sourceRevision.value = latest.sourceRevision; deployedSource.value = latest.source; reviewed.value = null; acknowledge.value = false; baseline.value = 'active'; tab.value = 'diff' }) }
async function changeService(event) {
 const id = event.target.value
 if (props.editor.docsDirty?.()) { pendingService.value = id; event.target.value = props.editor.sourceServiceID || ''; return }
 return attempt(() => props.selectService(id))
}
function leaveService(save) { return attempt(async () => {
 if (save) await props.save()
 const id = pendingService.value
 await props.selectService(id)
 pendingService.value = null
}) }
function saveDraft() { return attempt(() => props.save()) }
function reviewChanges() { return attempt(async () => {
 clearTimeout(timer); validation.value = parseCompose(source.value); pending.value = false
 if (!validation.value.schema_valid) return
 const current = generation
 const result = await props.review(source.value, runtime.value, sourceRevision.value, plan.value)
 if (current !== generation) return
 reviewed.value = result; tab.value = 'diff'; acknowledge.value = false
}) }
function confirmDeployment() { if (!reviewed.value || !acknowledge.value || pending.value) return; return attempt(() => props.deploy(reviewed.value)) }
function upload(event) { const files = [...event.target.files]; return attempt(async () => {
 const inputs = await Promise.all(files.map(file => file.text()))
 source.value = readableCompose(inputs.length === 1 ? inputs[0] : await props.merge(inputs))
 clearTimeout(timer)
 await analyzeSource()
}) }
function download() {
 const url = URL.createObjectURL(new Blob([props.document.rawSource ?? props.document.source], { type: 'text/plain;charset=utf-8' }))
 const link = document.createElement('a'); link.href = url; link.download = 'compose.source.yaml'; link.click(); URL.revokeObjectURL(url)
}
function issuePosition(issue) {
 const line = Number(issue.line) || 0
 if (line > 0) {
  const lineStart = line > 1 ? source.value.split('\n').slice(0, line - 1).join('\n').length + 1 : 0
  return { line, offset: issue.offset ?? Math.min(source.value.length, lineStart + Math.max(0, (issue.col ?? issue.column ?? 1) - 1)) }
 }
 const path = Array.isArray(issue.path) && issue.path.length ? [...issue.path] : typeof issue.pointer === 'string' && issue.pointer.startsWith('/') ? issue.pointer.slice(1).split('/').map(part => part.replaceAll('~1', '/').replaceAll('~0', '~')) : []
 let node
 while (path.length && !node) { node = validation.value.doc?.getIn(path, true); path.pop() }
 const offset = node?.range?.[0]
 return typeof offset === 'number' ? { line: source.value.slice(0, offset).split('\n').length, offset } : { line: 0, offset: 0 }
}
function locate(issue) {
 tab.value = 'source'
 queueMicrotask(() => {
  const el = codeInput.value
  const position = issuePosition(issue)
  el?.focus(); el?.setSelectionRange(position.offset, issue.end ?? position.offset)
  if (el && position.line) {
   const lineHeight = Number.parseFloat(window.getComputedStyle(el).lineHeight) || 22
   el.scrollTop = Math.max(0, (position.line - 3) * lineHeight)
   syncScroll({ target: el })
  }
 })
}
function syncScroll(event) { if (gutter.value) gutter.value.scrollTop = event.target.scrollTop }
function insertIndent(event) {
 if (event.key !== 'Tab' || event.ctrlKey || event.metaKey || event.altKey) return
 event.preventDefault()
 const target = event.target
 target.setRangeText('  ', target.selectionStart, target.selectionEnd, 'end')
 target.dispatchEvent(new Event('input', { bubbles: true }))
}
</script>
<template>
  <section class="compose-workbench">
    <header class="flex"><div><h1>{{ application.name }} · {{ t('composeEditor.title') }}</h1><p>{{ t('composeEditor.baseline') }}: {{ sourceRevision || '—' }}</p></div><span class="spacer"></span><button class="btn" @click="download">{{ t('composeEditor.download') }}</button><button class="btn" :disabled="busy || !application.managed" @click="saveDraft">{{ t('composeEditor.save') }}</button><button class="btn primary" :disabled="!canReview" @click="reviewChanges">{{ t('composeEditor.review') }}</button></header>
    <p v-if="error" role="alert" class="callout red">{{ error }} <button v-if="props.refreshBaseline" class="btn" :disabled="busy" @click="reloadBaseline">{{ t('composeEditor.refreshBaseline') }}</button></p>
    <label v-if="props.selectService">{{ t('nav.services') }}<select :value="editor.sourceServiceID || ''" :disabled="busy" @change="changeService"><option value="">{{ application.name }} · Stack</option><option v-for="service in services" :key="service.id" :value="service.id">{{ service.name }}</option></select></label>
    <section v-if="pendingService !== null" class="callout amber" role="alertdialog" :aria-label="t('dirty.title')"><p>{{ t('dirty.message') }}</p><button class="btn" :disabled="busy" @click="pendingService = null">{{ t('dirty.continue') }}</button><button class="btn" :disabled="busy" @click="leaveService(false)">{{ t('dirty.discard') }}</button><button class="btn primary" :disabled="busy" @click="leaveService(true)">{{ t('dirty.save_leave') }}</button></section>
    <nav class="y-tabs"><button v-for="value in ['source', 'runtime', 'resolved', 'diff']" :key="value" :class="{ active: tab === value }" @click="tab = value">{{ t(`composeEditor.${value}`) }}</button></nav>
    <label v-if="tab === 'source'">{{ t('composeEditor.import') }}<input type="file" accept=".yaml,.yml,.json" multiple :disabled="busy || !application.managed" @change="upload"></label>
    <div v-if="tab === 'source' && dockerResources.length" class="docker-picker"><label>{{ t('dockerResources.selectReference') }}<select v-model="selectedResource"><option value="">—</option><option v-for="item in dockerResources" :key="item.id" :value="`${item.kind}:${item.id}`">{{ item.kind }} · {{ item.name }}</option></select></label><label>{{ t('dockerResources.service') }}<select v-model="selectedService"><option value="">—</option><option v-for="service in Object.keys(validation.tree?.services || {})" :key="service" :value="service">{{ service }}</option></select></label><button class="btn" :disabled="!selectedResource || !selectedService" @click="insertDockerResource">{{ t('dockerResources.insert') }}</button></div>
    <p v-if="tab === 'source' && dockerResourceError" class="callout amber">{{ dockerResourceError }}</p>
    <div v-if="tab === 'source'" class="compose-code-wrap">
      <div ref="gutter" class="compose-code-gutter" aria-hidden="true"><span v-for="line in lineNumbers" :key="line" :class="{ invalid: errorLines.has(line) }">{{ line }}</span></div>
      <textarea id="y-compose-code" ref="codeInput" v-model="source" class="compose-source compose-code-input" spellcheck="false" :readonly="!application.managed" :aria-invalid="!validation.schema_valid" :aria-label="t('composeEditor.source')" @scroll="syncScroll" @keydown="insertIndent"></textarea>
    </div>
    <textarea v-if="tab === 'runtime'" v-model="runtime" class="compose-source" spellcheck="false" :readonly="!application.managed" :aria-label="t('composeEditor.runtime')"></textarea>
    <section v-if="tab === 'resolved'" class="compose-preview"><p>{{ t('composeEditor.previewNotice') }}</p><pre class="compose-source">{{ rendered ?? t(pending ? 'composeEditor.checking' : 'composeEditor.previewUnavailable') }}</pre></section>
    <section v-if="tab === 'diff'">
      <label>{{ t('composeEditor.compare') }} <select v-model="baseline"><option value="active">{{ t('composeEditor.baseline') }}</option><option value="draft">{{ t('composeEditor.savedDraft') }}</option></select></label>
      <ul><li v-for="change in reviewed?.preview.service_changes || changes" :key="change.name"><strong>{{ change.name }}</strong> — {{ t(`composeEditor.${change.kind}`) }} <small v-if="change.fields?.length"> · {{ t('composeEditor.fields') }}: {{ change.fields.join(', ') }}</small></li></ul>
      <p v-if="!changes.some(c => ['added', 'updated'].includes(c.kind))">{{ t(base !== source ? 'composeEditor.formatOnly' : 'composeEditor.noSemanticChanges') }}</p>
      <button class="btn" @click="expanded = !expanded">{{ t('composeEditor.context') }}</button>
      <div class="compose-diff split-diff"><div v-for="(row, index) in pairedRows" :key="index" class="split-row"><div v-for="side in ['left', 'right']" :key="side" :class="row[side]?.type"><span>{{ side === 'left' ? row[side]?.old : row[side]?.next }}</span><code>{{ row[side]?.text }}</code></div></div></div>
      <div class="compose-diff unified-diff" role="region" :aria-label="t('composeEditor.diff')"><div v-for="(row, index) in visibleRows" :key="index" class="diff-line" :class="row.type"><span>{{ row.old }}</span><span>{{ row.next }}</span><code>{{ row.type === 'add' ? '+' : row.type === 'remove' ? '−' : ' ' }} {{ row.text }}</code></div></div>
    </section>
    <p v-if="pending" role="status">{{ t('composeEditor.checking') }}</p>
    <ul v-if="validation.diagnostics.length"><li v-for="issue in validation.diagnostics" :key="issue.id"><button class="row-link" @click="locate(issue)">{{ issue.code }} · {{ issue.pointer }} · {{ issue.line }}:{{ issue.col }}</button></li></ul>
    <section v-if="analysis?.diagnostics.length" class="callout amber"><ul><li v-for="issue in analysis.diagnostics" :key="issue.id"><button class="row-link" @click="locate(issue)">{{ issue.code }} · {{ issue.pointer }}{{ issuePosition(issue).line ? ` · ${issuePosition(issue).line}:${issue.column || 1}` : '' }}</button></li></ul><label v-if="analysis.diagnostics.some(d => d.severity === 'warning' && d.code !== 'STORAGE_CAPABILITY_UNKNOWN')"><input v-model="warningsAccepted" type="checkbox"> {{ t('composeEditor.acknowledge') }}</label></section>
    <section v-if="reviewed" class="callout blue"><p>{{ t('composeEditor.frozen') }}</p><pre>{{ readableCompose(reviewed.preview.resolved_yaml) }}</pre><label><input v-model="acknowledge" type="checkbox"> {{ t('composeEditor.acknowledge') }}</label><button class="btn primary" :disabled="!acknowledge || pending || busy" @click="confirmDeployment">{{ t('composeEditor.deploy') }}</button></section>
  </section>
</template>
<style scoped>
.compose-workbench { padding: 24px; }
.docker-picker { display: flex; align-items: end; flex-wrap: wrap; gap: 10px; margin: 12px 0; }
.docker-picker label { display: grid; gap: 4px; min-width: 180px; }
.compose-source { display: block; width: 100%; min-height: 55vh; padding: 18px; font: 12px/1.8 ui-monospace, SFMono-Regular, Consolas, monospace; white-space: pre; overflow: auto; tab-size: 2; background: #142532; color: #d5e4ec; border: 0; caret-color: #79d9ba; }
.compose-code-wrap { display: flex; height: 55vh; min-height: 360px; background: #142532; overflow: hidden; border-radius: 6px; }
.compose-code-gutter { flex: none; width: 48px; padding: 18px 10px 18px 0; overflow: hidden; text-align: right; color: #72899a; font: 12px/1.8 ui-monospace, SFMono-Regular, Consolas, monospace; user-select: none; }
.compose-code-gutter span { display: block; height: 1.8em; }
.compose-code-gutter .invalid { color: #ff9c8d; background: #6a312d66; }
.compose-code-input { flex: 1; width: 0; height: 100%; min-height: 0; padding: 18px 18px 18px 0; resize: vertical; outline-offset: -3px; }
.compose-preview p { margin: 10px 0; color: var(--muted); }
.compose-preview pre { margin: 0; }
.compose-workbench pre { max-height: 65vh; overflow: auto; }
.compose-diff { overflow: auto; font-family: monospace; margin-top: 16px; }
.unified-diff { display: none; }
.split-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
.split-row > div { display: flex; overflow: auto; white-space: pre; min-height: 1.5em; }
.split-row span { min-width: 4rem; opacity: .65; text-align: right; padding-right: 1rem; }
.diff-line { display: grid; grid-template-columns: 4rem 4rem 1fr; min-width: max-content; }
.diff-line > span { opacity: .65; text-align: right; padding-right: 1rem; user-select: none; }
.diff-line code { white-space: pre; }
.add { background: #009b6820; }
.remove { background: #dc354520; }
@media(max-width: 700px) { .split-diff { display: none; } .unified-diff { display: block; } .compose-workbench { padding: 12px; } header { flex-wrap: wrap; } .diff-line { grid-template-columns: 2.5rem 2.5rem 1fr; } }
</style>
