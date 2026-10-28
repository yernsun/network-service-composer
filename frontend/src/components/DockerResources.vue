<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { request, session, errorText, terminal } from '../shared/api/client'
import { translate as t } from '../i18n'
import StatusBadge from './StatusBadge.vue'
import Volumes from './Volumes.vue'

type Resource = { id: string; name: string; kind: 'config' | 'secret'; owned: boolean; used_by: string[]; created_at: string }
const props = defineProps<{ runtimeId: string }>()
const router = useRouter()
const runtimeName = ref('')
const runtimeType = ref('swarm')
const section = ref('objects')
const items = ref<Resource[]>([])
const kind = ref<'config' | 'secret'>('config')
const name = ref('')
const text = ref('')
const selectedFile = ref<File | null>(null)
const busy = ref(false)
const loading = ref(true)
const error = ref('')
const operationID = ref('')
const operationStatus = ref('')
const restoreRisk = ref(false)
const controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined

function base64(bytes: Uint8Array) {
  let value = ''
  for (let offset = 0; offset < bytes.length; offset += 8192) value += String.fromCharCode(...bytes.subarray(offset, offset + 8192))
  return btoa(value)
}

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const values: Resource[] = []
    let cursor = ''
    do {
      const page = await request('get', '/runtimes/{id}/docker-resources', { signal: controller.signal,
        params: { path: { id: props.runtimeId }, query: { limit: 200, ...(cursor ? { cursor } : {}) } } })
      values.push(...(page.data as Resource[]))
      cursor = page.pagination?.next_cursor || ''
    } while (cursor)
    items.value = values
  } catch (cause) {
    if (!controller.signal.aborted) error.value = errorText(cause)
  } finally { loading.value = false }
}

async function watchOperation(id: string) {
  operationID.value = id
  const poll = async () => {
    if (controller.signal.aborted) return
    try {
      const result = await request('get', '/operations/{id}', { signal: controller.signal, params: { path: { id } } })
      operationStatus.value = String((result.data as { status: string }).status)
      if (terminal(operationStatus.value)) { busy.value = false; await refresh(); return }
      timer = setTimeout(poll, 1500)
    } catch (cause) {
      if (!controller.signal.aborted) { error.value = errorText(cause); busy.value = false }
    }
  }
  await poll()
}

async function createResource() {
  if (busy.value || !name.value.trim()) return
  busy.value = true; error.value = ''
  try {
    const bytes = selectedFile.value ? new Uint8Array(await selectedFile.value.arrayBuffer()) : new TextEncoder().encode(text.value)
    if (bytes.length === 0 || bytes.length > 500 * 1024) { error.value = t('dockerResources.sizeLimit'); busy.value = false; return }
    const content_base64 = base64(bytes)
    bytes.fill(0)
    const result = await request('post', '/runtimes/{id}/docker-resources', { signal: controller.signal,
      params: { path: { id: props.runtimeId } }, headers: { 'Idempotency-Key': crypto.randomUUID() },
      body: { kind: kind.value, name: name.value.trim(), content_base64 } })
    text.value = ''; selectedFile.value = null; name.value = ''
    await watchOperation(String((result.data as { id: string }).id))
  } catch (cause) { error.value = errorText(cause); busy.value = false }
}

async function deleteResource(item: Resource) {
  if (busy.value || !item.owned || item.used_by.length || !restoreRisk.value || !window.confirm(t('dockerResources.deleteConfirm'))) return
  busy.value = true; error.value = ''
  try {
    const result = await request('delete', '/runtimes/{id}/docker-resources/{resourceID}', { signal: controller.signal,
      params: { path: { id: props.runtimeId, resourceID: item.id }, query: { kind: item.kind } },
      headers: { 'Idempotency-Key': crypto.randomUUID() }, body: { acknowledge_restore_risk: true } })
    restoreRisk.value = false
    await watchOperation(String((result.data as { id: string }).id))
  } catch (cause) { error.value = errorText(cause); busy.value = false }
}

onMounted(async () => {
  try {
    const result = await request('get', '/runtimes/{id}', { signal: controller.signal, params: { path: { id: props.runtimeId } } })
    const runtime = result.data as { name: string; runtime_type: string }
    runtimeName.value = runtime.name
    runtimeType.value = runtime.runtime_type
    if (runtime.runtime_type === "docker") { section.value = "volumes"; loading.value = false; return }
    if (runtime.runtime_type !== 'swarm') { error.value = t('dockerResources.swarmOnly'); loading.value = false; return }
    await refresh()
  } catch (cause) { if (!controller.signal.aborted) error.value = errorText(cause); loading.value = false }
})
onBeforeUnmount(() => { controller.abort(); if (timer) clearTimeout(timer); text.value = ''; selectedFile.value = null })
function fileChanged(event: Event) { selectedFile.value = (event.target as HTMLInputElement).files?.[0] || null }
</script>

<template>
  <section class="docker-resources">
    <button class="btn" @click="router.push({ path: `/infrastructure/runtimes/${runtimeId}`, query: { tab: 'overview' } })">{{ t('dockerResources.back') }}</button>
    <header><h1>{{ t(section === 'volumes' ? 'volumes.title' : 'dockerResources.title') }} · {{ runtimeName }}</h1><p v-if="section === 'objects'">{{ t('dockerResources.notice') }}</p></header>
    <nav><button class="btn" :aria-pressed="section === 'volumes'" @click="section = 'volumes'">{{ t('volumes.title') }}</button> <button v-if="runtimeType === 'swarm'" class="btn" :aria-pressed="section === 'objects'" @click="section = 'objects'">Config / Secret</button></nav>
    <Volumes v-if="section === 'volumes'" :key="runtimeId" :runtime-id="runtimeId" :runtime-type="runtimeType" />
    <template v-else>
      <p v-if="error" class="callout red" role="alert">{{ error }}</p>
      <p v-if="operationID" class="callout blue" role="status">{{ t('dockerResources.operation') }}: <a :href="`/operations/${operationID}`">{{ operationID }}</a> · <StatusBadge :status="operationStatus" context="operation" /></p>
      <section v-if="session.role === 'administrator'" class="card resource-create">
        <h2>{{ t('dockerResources.create') }}</h2>
        <div class="resource-fields"><label>{{ t('dockerResources.kind') }}<select v-model="kind"><option value="config">Config</option><option value="secret">Secret</option></select></label><label>{{ t('dockerResources.name') }}<input v-model="name" maxlength="255" autocomplete="off"></label></div>
        <label>{{ t('dockerResources.text') }}<textarea v-model="text" :disabled="Boolean(selectedFile)" autocomplete="off"></textarea></label>
        <label>{{ t('dockerResources.file') }}<input type="file" @change="fileChanged"></label>
        <p class="muted">{{ t('dockerResources.contentNotice') }}</p>
        <button class="btn primary" :disabled="busy || !name.trim() || (!text && !selectedFile)" @click="createResource">{{ t('dockerResources.create') }}</button>
      </section>
      <section class="card resource-list">
        <h2>{{ t('dockerResources.list') }}</h2>
        <p v-if="loading">{{ t('dockerResources.loading') }}</p><p v-else-if="!items.length">{{ t('dockerResources.empty') }}</p>
        <ul v-else><li v-for="item in items" :key="item.id"><div><strong>{{ item.name }}</strong> <span class="badge">{{ item.kind }}</span> <span class="badge" :class="item.owned ? 'green' : 'amber'">{{ t(item.owned ? 'dockerResources.owned' : 'dockerResources.external') }}</span><small>{{ item.id }}</small><p>{{ t('dockerResources.usedBy') }}: {{ item.used_by.length ? item.used_by.join(', ') : t('dockerResources.none') }}</p></div><button v-if="item.owned" class="btn danger" :disabled="busy || Boolean(item.used_by.length) || !restoreRisk" @click="deleteResource(item)">{{ t('dockerResources.delete') }}</button></li></ul>
        <label v-if="items.some(item => item.owned)" class="restore-warning"><input v-model="restoreRisk" type="checkbox">{{ t('dockerResources.restoreRisk') }}</label>
      </section>
    </template>
  </section>
</template>

<style scoped>
.docker-resources { padding: 24px; display: grid; gap: 18px; }
.docker-resources header p { color: var(--muted); }
.docker-resources .card { padding: 20px; }
.resource-fields { display: flex; gap: 16px; flex-wrap: wrap; }
.resource-fields label { flex: 1; min-width: 180px; }
.docker-resources label { display: grid; gap: 6px; margin: 12px 0; }
.docker-resources input, .docker-resources select, .docker-resources textarea { width: 100%; padding: 8px; }
.docker-resources textarea { min-height: 100px; }
.resource-list ul { list-style: none; padding: 0; }
.resource-list li { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 0; border-top: 1px solid var(--line); overflow-wrap: anywhere; }
.resource-list small { display: block; color: var(--muted); }
.restore-warning { display: flex !important; align-items: center; }
.restore-warning input { width: auto; }
</style>
