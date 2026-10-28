<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { errorText, request, session } from '../shared/api/client'
import { bindingWriter } from '../data/service-bindings'
import { translate as t } from '../i18n'
import type { components } from '../shared/api/schema'
const props = defineProps<{ applicationId: string }>()
type Schema = components['schemas']
const query = useQuery({ queryKey: computed(() => ['application-network', props.applicationId]), queryFn: async ({ signal }) => {
  const app = (await request('get', '/applications/{id}', { signal, params: { path: { id: props.applicationId } } })).data as Schema['Application']
  const runtime = (await request('get', '/runtimes/{id}', { signal, params: { path: { id: app.runtime_id } } })).data as Schema['Runtime']
  const revisionID = app.desired_revision_id || app.active_revision_id
  const revision = revisionID ? (await request('get', '/applications/{id}/revisions/{revisionID}', { signal, params: { path: { id: app.id, revisionID } } })).data as Schema['ApplicationRevision'] : undefined
  return { app, runtime, name: revision?.configuration_source?.network_name || '' }
} })
const data = query.data, name = ref(''), editing = ref(false), busy = ref(false), error = ref(''), task = ref('')
const preview = ref<Schema['ApplicationRevisionPreview']>()
let reviewedName = '', reviewedRevision = 0
const write = bindingWriter()
const writable = computed(() => session.value.role === 'administrator' && data.value?.app.managed && !data.value.app.system && data.value.runtime.management_enabled)
watch(name, () => { preview.value = undefined })
onBeforeRouteLeave(() => !editing.value || window.confirm(t('bindingUI.discard')))
onBeforeRouteUpdate(() => !editing.value || window.confirm(t('bindingUI.discard')))
watch(() => props.applicationId, () => { editing.value = false; preview.value = undefined; error.value = ''; task.value = '' })
function beforeUnload(event: BeforeUnloadEvent) { if (editing.value) { event.preventDefault(); event.returnValue = '' } }
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
async function attempt(work: () => Promise<void>) {
  if (!writable.value || busy.value) return
  busy.value = true; error.value = ''
  try { await work() } catch (e) { error.value = errorText(e) } finally { busy.value = false }
}
function edit() { name.value = data.value?.name || data.value?.app.project_name || ''; editing.value = true; preview.value = undefined }
async function review() {
  await attempt(async () => {
    const current = await query.refetch()
    if (!current.data || current.error) throw current.error || new Error(t('bindingUI.loading'))
    reviewedRevision = current.data.app.revision; reviewedName = name.value.trim()
    preview.value = (await request('post', '/applications/{id}/revisions/preflight', { params: { path: { id: props.applicationId } }, body: { network_name: reviewedName } })).data as Schema['ApplicationRevisionPreview']
  })
}
async function deploy() {
  if (!preview.value || name.value.trim() !== reviewedName) return
  await attempt(async () => {
    const result = await write('post', '/applications/{id}/revisions', { id: props.applicationId }, { network_name: reviewedName }, reviewedRevision)
    task.value = (result.data as { operation: { id: string } }).operation.id
    editing.value = false; preview.value = undefined; await query.refetch(); window.dispatchEvent(new Event('nsc:resources-changed'))
  })
}
</script>
<template>
  <section v-if="data?.runtime.runtime_type === 'swarm'" class="card network-settings" data-testid="application-network">
    <h2>{{ t('bindingUI.networkName') }}</h2>
    <p>{{ data.name || '—' }} · Stack: {{ data.app.project_name }}</p>
    <p class="hint">{{ t('bindingUI.applicationNetworkHint') }}</p>
    <p v-if="error" role="alert" class="callout red">{{ error }}</p>
    <p v-if="task"><a :href="`/operations/${task}`">{{ task }}</a></p>
    <button v-if="!editing" class="btn" :disabled="!writable" @click="edit">{{ t('bindingUI.changeNetwork') }}</button>
    <form v-else @submit.prevent="review">
      <label for="application-network-name">{{ t('bindingUI.networkName') }}</label>
      <input id="application-network-name" v-model="name" required maxlength="128" pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]*" :disabled="busy">
      <p class="callout amber">{{ t('bindingUI.networkUpdate') }}</p>
      <button class="btn" type="submit" :disabled="busy">{{ t('bindingUI.reviewNetwork') }}</button>
      <button class="btn" type="button" :disabled="busy" @click="editing = false">{{ t('console.cancel') }}</button>
      <div v-if="preview"><p>{{ data.name || '—' }} → {{ preview.network_name }}</p><ul><li v-for="service in preview.services" :key="service.name">{{ service.name }}</li></ul><button class="btn primary" type="button" :disabled="busy" @click="deploy">{{ t('bindingUI.deployNetwork') }}</button></div>
    </form>
  </section>
</template>
<style scoped>
.network-settings { margin-top: 24px; padding: 20px; }
.network-settings input { display: block; width: 100%; margin: 8px 0; }
</style>
