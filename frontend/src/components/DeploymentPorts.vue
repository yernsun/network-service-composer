<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useWorkspaceStore } from '../stores/workspace'
import { translate as t } from '../i18n'
import { composeSession, composeSignature, composeSource, invalidateCompose } from '../compose/session'
import { editServicePort, parseCompose } from '../compose/validator'
import { portValue, publishedPorts } from '../compose/ports'

const store = useWorkspaceStore(), error = ref('')
const current = () => store.service?.workload.wizard
const wizard = computed(() => { void store.revision; return current() ? { ...current() } : null })
const swarm = computed(() => store.state().runtimes.some(r => r.id === wizard.value?.runtime && r.type === 'swarm'))
const source = computed(() => { void store.revision; return wizard.value ? composeSource(wizard.value) : '' })
const parsed = computed(() => parseCompose(source.value))
const services = computed(() => Object.keys(parsed.value.tree?.services || {}))
const rows = computed(() => parsed.value.schema_valid ? publishedPorts(parsed.value.tree) : [])
const draft = computed(() => { void store.revision; return wizard.value?.portDraft })
const review = computed(() => {
  if (!wizard.value) return null
  const state = composeSession(current())
  return state.review?.signature === composeSignature(current()) && !state.pending && !state.reviewPending && !state.error ? state.review : null
})
const reviewRows = computed(() => review.value ? publishedPorts(parseCompose(review.value.preview.resolved_yaml || '').tree) : [])
function begin(row) {
  const w = current()
  if (!w || draft.value) return
  w.portDraft = { ...(row || { service: services.value[0], index: -1, target: '', published: '', protocol: 'tcp', mode: 'ingress', host_ip: '' }), source: source.value }
  error.value = ''; invalidateCompose(w); store.revision++
  window.dispatchEvent(new Event('nsc:compose-change'))
}
function field(name, value) { draft.value[name] = value; store.revision++ }
function cancel() { current().portDraft = null; error.value = ''; store.revision++; window.dispatchEvent(new Event('nsc:compose-change')) }
function write(service, index, value) {
  const w = current()
  if (w.source === 'image') {
    const ports = [...(w.publishedPorts || [])]
    if (value === null) ports.splice(index, 1)
    else ports[index] = value
    w.publishedPorts = ports
  } else {
    const changed = editServicePort(source.value, service, index, value)
    if (changed.error) { error.value = t(`compose.${changed.error}`); return }
    w.raw = changed.source
  }
  w.portDraft = null; w.touched = true; error.value = ''
  invalidateCompose(w); store.revision++
  window.dispatchEvent(new Event('nsc:compose-change'))
}
function apply() {
  const d = draft.value, value = portValue(d, swarm.value)
  if (d.source !== source.value || !parsed.value.tree?.services?.[d.service]) { error.value = t('ports.stale'); return }
  if (!value) { error.value = t('ports.invalid'); return }
  // Keep optional metadata and extensions from imported long syntax.
  const next = typeof d.raw === 'object' ? { ...d.raw, ...value } : value
  if (swarm.value) delete next.host_ip
  else { delete next.mode; if (!value.host_ip) delete next.host_ip }
  const length = parsed.value.tree.services[d.service].ports?.length || 0
  write(d.service, d.index < 0 ? length : d.index, next)
}
function scope(row) { return t(!swarm.value ? 'ports.standalone' : row.mode === 'host' ? 'ports.host' : 'ports.ingress') }
function editDiagnostic(event) {
  const row = rows.value.find(row => row.service === event.detail.service && row.index === Number(event.detail.index))
  if (row) begin(row)
  document.querySelector('[data-testid=deployment-ports]')?.scrollIntoView?.({ block: 'center' })
}
onMounted(() => window.addEventListener('nsc:edit-port', editDiagnostic))
onBeforeUnmount(() => window.removeEventListener('nsc:edit-port', editDiagnostic))
</script>
<template>
  <section v-if="wizard && wizard.step >= 3 && wizard.source !== 'template'" class="deployment-ports panel" data-testid="deployment-ports">
    <div class="ports-heading"><h3>{{ t('ports.title') }}</h3><button v-if="wizard.step === 3" class="btn sm" :disabled="!!draft || !parsed.schema_valid" @click="begin()">{{ t('ports.add') }}</button></div>
    <p class="muted">{{ t('ports.help') }}</p>
    <p v-if="wizard.step === 4 && !review" role="status">{{ t(draft ? 'ports.unapplied' : 'compose.checking') }}</p>
    <template v-else>
      <p v-if="!(wizard.step === 4 ? reviewRows : rows).length" class="muted">{{ t('ports.none') }}</p>
      <div v-for="row in wizard.step === 4 ? reviewRows : rows" :key="row.service + ':' + row.index" class="port-row">
        <strong>{{ row.service }}</strong>
        <span><code>{{ row.host_ip ? row.host_ip + ':' : '' }}{{ row.published && row.published !== '0' ? row.published : t('ports.automatic') }} → {{ row.target }} / {{ row.protocol.toUpperCase() }}</code><small>{{ scope(row) }}</small></span>
        <div v-if="wizard.step === 3" class="port-actions"><button class="btn ghost sm" :disabled="!!draft" @click="begin(row)">{{ t('ports.edit') }}</button><button class="btn ghost sm" :disabled="!!draft" @click="write(row.service, row.index, null)">{{ t('ports.remove') }}</button></div>
      </div>
    </template>
    <div v-if="draft" class="ports-form" data-testid="port-form">
      <label>{{ t('ports.service') }}<select :value="draft.service" :disabled="draft.index >= 0" @change="field('service', $event.target.value)"><option v-for="name in services" :key="name" :value="name">{{ name }}</option></select></label>
      <label>{{ t('ports.published') }}<input :value="draft.published" inputmode="numeric" placeholder="3306" data-port="published" @input="field('published', $event.target.value)"></label>
      <label>{{ t('ports.target') }}<input :value="draft.target" inputmode="numeric" placeholder="3306" data-port="target" @input="field('target', $event.target.value)"></label>
      <label>{{ t('ports.protocol') }}<select :value="draft.protocol" @change="field('protocol', $event.target.value)"><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
      <label v-if="swarm">{{ t('ports.mode') }}<select :value="draft.mode" @change="field('mode', $event.target.value)"><option value="ingress">{{ t('ports.ingress') }}</option><option value="host">{{ t('ports.host') }}</option></select></label>
      <label v-else>{{ t('ports.bindAddress') }}<input :value="draft.host_ip" placeholder="0.0.0.0" @input="field('host_ip', $event.target.value)"></label>
      <p class="ports-hint">{{ t(swarm ? draft.mode === 'host' ? 'ports.hostHint' : 'ports.ingressHint' : 'ports.standaloneHint') }}</p>
      <p v-if="error" class="ports-hint error" role="alert">{{ error }}</p>
      <div class="port-actions ports-hint"><button class="btn" @click="cancel">{{ t('console.cancel') }}</button><button class="btn primary" @click="apply">{{ t('ports.apply') }}</button></div>
    </div>
    <p v-if="wizard.step === 4 && review" class="muted">{{ t('ports.reviewHint') }}</p>
  </section>
</template>
<style scoped>
.deployment-ports { margin-top: 20px; padding: 20px; }
.ports-heading, .port-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.port-row { display: grid; grid-template-columns: minmax(100px,1fr) minmax(180px,2fr) auto; align-items: center; gap: 12px; padding: 12px 0; border-bottom: 1px solid var(--line); }
.port-row small { display: block; color: var(--muted); }
.ports-form { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 16px; margin-top: 16px; }
.ports-form label { display: grid; gap: 6px; }
.ports-form input, .ports-form select { width: 100%; min-width: 0; }
.ports-hint { grid-column: 1 / -1; }
.port-actions.ports-hint { justify-content: flex-end; }
.error { color: var(--red); }
@media(max-width:700px) { .port-row,.ports-form { grid-template-columns: 1fr; } .port-actions { justify-content: flex-start; } }
</style>
