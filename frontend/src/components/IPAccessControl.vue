<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWorkspaceStore } from '../stores/workspace'
import { errorText, session } from '../shared/api/client'
const store = useWorkspaceStore()
const { t } = useI18n()
const editor = computed(() => { void store.revision; return store.ipAccess?.ipAccessEditorState() })
const forms = ref<Record<string, { access: string; forbidden: string }>>({})
const source = ref({ mode: 'connection', header: '', trusted: '' })
const preview = ref('')
const error = ref('')
const busy = ref(false)
const writable = computed(() => session.value.role === 'administrator')
let previewGeneration = 0
const targets = computed(() => editor.value ? [{ id: '', name: t('ipAccess.domain') }, ...editor.value.work.routes.map(route => ({ id: route.id, name: route.path }))] : [])
watch([() => editor.value?.domainID, () => editor.value?.work, () => targets.value.map(target => target.id).join(',')], () => {
  const work = editor.value?.work
  const next: Record<string, { access: string; forbidden: string }> = {}
  for (const target of targets.value) {
    const lists = target.id ? work?.routes.find(route => route.id === target.id)?.ipAccess : work?.ipAccess
    next[target.id] = { access: lists?.access.join('\n') || '', forbidden: lists?.forbidden.join('\n') || '' }
  }
  forms.value = next; preview.value = ''; error.value = ''; previewGeneration++; busy.value = false
  const saved = work?.ipAccess?.clientIP
  source.value = { mode: !saved ? 'connection' : ['X-Forwarded-For', 'X-Real-IP'].includes(saved.header) ? saved.header : 'custom', header: saved?.header || '', trusted: saved?.trusted_proxies.join('\n') || '' }
}, { immediate: true })
function invalidatePreview() {
  preview.value = ''; error.value = ''; previewGeneration++; busy.value = false
}
function update(id: string, list: 'access' | 'forbidden', event: Event) {
  const input = event.target
  if (!writable.value || !(input instanceof HTMLTextAreaElement) || !forms.value[id]) return
  forms.value[id][list] = input.value
  invalidatePreview()
  store.ipAccess?.updateIPAccess(id, list, input.value)
}
function updateSource(field: 'mode' | 'header' | 'trusted', event: Event) {
  const input = event.target
  if (!writable.value || !(input instanceof HTMLInputElement || input instanceof HTMLSelectElement || input instanceof HTMLTextAreaElement)) return
  source.value[field] = input.value
  if (field === 'mode' && ['X-Forwarded-For', 'X-Real-IP'].includes(input.value)) source.value.header = input.value
  invalidatePreview()
  store.ipAccess?.updateClientIP(source.value.mode === 'connection' ? null : { header: source.value.header, trusted_proxies: source.value.trusted.split('\n').filter(line => line.trim() !== '') })
}
async function showPreview() {
  if (busy.value) return
  const generation = ++previewGeneration
  busy.value = true; error.value = ''
  try {
    const content = await store.ipAccess?.previewIPAccess()
    if (generation === previewGeneration) preview.value = content || ''
  } catch (e) { if (generation === previewGeneration) error.value = errorText(e) }
  finally { if (generation === previewGeneration) busy.value = false }
}
</script>

<template>
  <section v-if="editor" class="card ip-access" data-testid="ip-access-control">
    <h3>{{ t('ipAccess.title') }}</h3>
    <p>{{ t('ipAccess.policy') }}</p>
    <p class="muted">{{ t('ipAccess.format') }}</p>
    <p class="muted">{{ t('ipAccess.source') }}</p>
    <fieldset>
      <legend>{{ t('ipAccess.clientSource') }}</legend>
      <label>
        <span>{{ t('ipAccess.clientSource') }}</span>
        <select name="ip-client-source" :value="source.mode" :disabled="!writable" @change="updateSource('mode', $event)">
          <option value="connection">{{ t('ipAccess.connection') }}</option>
          <option value="X-Forwarded-For">X-Forwarded-For</option>
          <option value="X-Real-IP">X-Real-IP</option>
          <option value="custom">{{ t('ipAccess.customHeader') }}</option>
        </select>
      </label>
      <label v-if="source.mode === 'custom'">
        <span>{{ t('ipAccess.headerName') }}</span>
        <input name="ip-client-header" :value="source.header" :readonly="!writable" maxlength="64" spellcheck="false" @input="updateSource('header', $event)">
      </label>
      <label v-if="source.mode !== 'connection'">
        <span>{{ t('ipAccess.trustedProxies') }}</span>
        <textarea name="ip-trusted-proxies" :value="source.trusted" :readonly="!writable" rows="4" spellcheck="false" @input="updateSource('trusted', $event)" />
      </label>
      <p class="muted">{{ t('ipAccess.proxyPolicy') }}</p>
    </fieldset>
    <fieldset v-for="target in targets" :key="target.id">
      <legend>{{ target.name }}</legend>
      <div class="ip-access-lists">
        <label>
          <span>{{ t('ipAccess.access') }}</span>
          <textarea :readonly="!writable" :value="forms[target.id]?.access" :name="`ip-access-${target.id || 'domain'}`" rows="4" spellcheck="false" @input="update(target.id, 'access', $event)" />
        </label>
        <label>
          <span>{{ t('ipAccess.forbidden') }}</span>
          <textarea :readonly="!writable" :value="forms[target.id]?.forbidden" :name="`ip-forbidden-${target.id || 'domain'}`" rows="4" spellcheck="false" @input="update(target.id, 'forbidden', $event)" />
        </label>
      </div>
    </fieldset>
    <p>{{ t('ipAccess.draft') }}</p>
    <button type="button" class="btn" :disabled="busy || !writable" @click="showPreview">{{ t('ipAccess.preview') }}</button>
    <p v-if="error" role="alert">{{ error }}</p>
    <pre v-if="preview" data-testid="ip-access-preview">{{ preview }}</pre>
  </section>
</template>

<style scoped>
.ip-access { margin-top: 16px; padding: 20px; }
.ip-access fieldset { margin: 16px 0; border: 1px solid var(--line); padding: 12px; }
.ip-access-lists { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.ip-access label { display: grid; gap: 8px; }
.ip-access textarea { width: 100%; font-family: monospace; }
.ip-access pre { overflow: auto; max-height: 400px; }
@media (max-width: 700px) { .ip-access-lists { grid-template-columns: 1fr; } }
</style>
