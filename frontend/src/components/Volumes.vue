<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { stringify } from 'yaml'
import { allPages } from '../data/pagination'
import { request, session, errorText, terminal, ApiRequestError, type RequestOptions } from '../shared/api/client'
import type { components, paths } from '../shared/api/schema'
import { translate as t } from '../i18n'

type Volume = components['schemas']['Volume']
const props = defineProps<{ runtimeId: string; runtimeType: string }>()
const node = ref(''), name = ref(''), search = ref(''), kind = ref(''), usage = ref(''), error = ref('')
const operationID = ref('')
const selected = ref<Volume>(), etag = ref(''), acknowledge = ref(false), deleting = ref(false)
const submission = ref<{ method: 'post' | 'delete'; path: keyof paths; options: RequestOptions }>()
const sending = ref(false)
const nodes = useQuery({ queryKey: computed(() => ['volume-nodes', props.runtimeId]), enabled: computed(() => props.runtimeType === 'swarm'),
  queryFn: ({ signal }) => allPages('/runtimes/{id}/cluster/nodes', { id: props.runtimeId }, signal), retry: false, refetchInterval: 10000 })
watch(nodes.data, list => { if (!node.value && list?.length === 1) node.value = String(list[0]?.id) })
const query = computed(() => props.runtimeType === 'swarm' ? { node_id: node.value } : {})
const inventory = useQuery({ queryKey: computed(() => ['volumes', props.runtimeId, node.value]),
  enabled: computed(() => props.runtimeType !== 'swarm' || !!node.value), retry: false, refetchInterval: 10000,
  queryFn: async ({ signal }) => {
    const items: Volume[] = [], seen = new Set<string>()
    let cursor = '', observation
    do {
      const page = await request('get', '/runtimes/{id}/volumes', { signal, params: { path: { id: props.runtimeId }, query: { ...query.value, limit: 200, ...(cursor ? { cursor } : {}) } } })
      items.push(...page.data as Volume[])
      observation = page.observation
      cursor = page.pagination?.next_cursor || ''
      if (cursor && seen.has(cursor)) throw new Error('Repeated pagination cursor')
      seen.add(cursor)
    } while (cursor)
    return { items, observation }
  } })
const operation = useQuery({ queryKey: computed(() => ['volume-operation', operationID.value]), enabled: computed(() => !!operationID.value), retry: false,
  queryFn: async ({ signal }) => (await request('get', '/operations/{id}', { signal, params: { path: { id: operationID.value } } })).data as { status: string; failure_code?: string },
  refetchInterval: q => terminal(q.state.data?.status) ? false : 1500 })
watch(() => operation.data.value?.status, status => { if (terminal(status)) { void inventory.refetch(); if (status !== 'SUCCEEDED') error.value = t('volumes.operationFailed') } })
const busy = computed(() => sending.value || !!submission.value || !!operationID.value && !terminal(operation.data.value?.status))
const ready = computed(() => !inventory.isError.value && inventory.data.value?.observation?.complete === true && (props.runtimeType !== 'swarm' || !nodes.isError.value && nodes.data.value?.some(n => n.id === node.value && n.state === 'ready' && n.availability === 'active' && !n.stale && !n.virtual)))
const items = computed(() => (inventory.data.value?.items || []).filter(v => v.name.toLowerCase().includes(search.value.toLowerCase()) && (!kind.value || v.kind === kind.value) && (!usage.value || (usage.value === 'used' ? v.used_by.length > 0 : ready.value && v.used_by.length === 0))))
let detailSequence = 0
watch(node, () => { detailSequence++; selected.value = undefined; deleting.value = false; acknowledge.value = false })
async function submit() {
  if (!submission.value || sending.value) return
  sending.value = true; error.value = ''
  try {
    const s = submission.value
    const result = await request(s.method, s.path, s.options)
    operationID.value = String((result.data as { id: string }).id)
    submission.value = undefined; selected.value = undefined; deleting.value = false; acknowledge.value = false; name.value = ''
  } catch (cause) {
    error.value = errorText(cause)
    if (cause instanceof ApiRequestError && cause.status >= 400 && cause.status < 500) submission.value = undefined
  } finally { sending.value = false }
}
function create() {
  if (busy.value || !ready.value) return
  submission.value = { method: 'post', path: '/runtimes/{id}/volumes', options: { params: { path: { id: props.runtimeId }, query: query.value }, headers: { 'Idempotency-Key': crypto.randomUUID() }, body: { name: name.value.trim() } } }
  void submit()
}
async function detail(item: Volume, remove = false) {
  const sequence = ++detailSequence
  error.value = ''; acknowledge.value = false; deleting.value = false
  try {
    const result = await request('get', '/runtimes/{id}/volumes/{name}', { params: { path: { id: props.runtimeId, name: item.name }, query: query.value } })
    if (sequence !== detailSequence) return
    selected.value = result.data as Volume; etag.value = result.response.headers.get('ETag') || ''
    deleting.value = remove && result.observation?.complete === true && !selected.value.blocked_reasons.length && !!etag.value
  } catch (cause) { error.value = errorText(cause) }
}
function remove() {
  if (!selected.value || !deleting.value || !acknowledge.value || busy.value || !ready.value) return
  submission.value = { method: 'delete', path: '/runtimes/{id}/volumes/{name}', options: { params: { path: { id: props.runtimeId, name: selected.value.name }, query: query.value }, headers: { 'Idempotency-Key': crypto.randomUUID(), 'If-Match': etag.value }, body: { acknowledge_data_loss: true } } }
  void submit()
}
const example = computed(() => selected.value ? stringify({ services: { app: { image: 'your-image', volumes: ['data:/data'], ...(props.runtimeType === 'swarm' ? { deploy: { replicas: 1, placement: { constraints: [`node.id == ${selected.value.node_id}`] } } } : {}) } }, volumes: { data: { external: true, name: selected.value.name } } }) : '')
async function copy() { try { await navigator.clipboard.writeText(example.value) } catch { error.value = t('volumes.copyFailed') } }
</script>
<template>
  <section class="volumes">
    <h2>{{ t('volumes.title') }}</h2><p>{{ t('volumes.notice') }}</p>
    <label v-if="runtimeType === 'swarm'">{{ t('compose.dataNode') }}<select v-model="node" :disabled="busy || nodes.isPending.value"><option value="">{{ t('compose.chooseNode') }}</option><option v-for="n in nodes.data.value" :key="String(n.id)" :value="n.id">{{ n.name }} · {{ n.state }} / {{ n.availability }}</option></select></label>
    <p v-if="nodes.isError.value" role="alert">{{ errorText(nodes.error.value) }} <button class="btn" @click="nodes.refetch()">{{ t('timeline.retry') }}</button></p>
    <p v-if="error" class="callout red" role="alert">{{ error }}</p>
    <button v-if="submission && !sending" class="btn" @click="submit">{{ t('volumes.retrySubmission') }}</button>
    <p v-if="operationID" role="status"><a :href="`/operations/${operationID}`">{{ operationID }}</a> · {{ operation.data.value?.status || t('volumes.pending') }} <button v-if="operation.isError.value" class="btn" @click="operation.refetch()">{{ t('timeline.retry') }}</button></p>
    <p v-if="inventory.isError.value" role="alert">{{ errorText(inventory.error.value) }} <button class="btn" @click="inventory.refetch()">{{ t('timeline.retry') }}</button></p>
    <p v-if="inventory.isFetching.value">{{ t('dockerResources.loading') }}</p>
    <p v-if="inventory.data.value && !ready" class="callout amber">{{ t('volumes.readOnly') }}</p>
    <small v-if="inventory.data.value?.observation">{{ inventory.data.value.observation.node_name }} · {{ inventory.data.value.observation.observed_at }}</small>
    <form v-if="session.role === 'administrator'" class="card" @submit.prevent="create"><label>{{ t('volumes.name') }}<input v-model="name" maxlength="255" :disabled="busy" autocomplete="off"></label><p>{{ t('volumes.nameHint') }}</p><button class="btn primary" :disabled="busy || !ready">{{ t('volumes.create') }}</button></form>
    <div class="filters"><label>{{ t('volumes.search') }}<input v-model="search"></label><label>{{ t('dockerResources.kind') }}<select v-model="kind"><option value="">{{ t('timeline.all') }}</option><option v-for="k in ['named','anonymous','unknown']" :key="k" :value="k">{{ t(`volumes.kind.${k}`) }}</option></select></label><label>{{ t('volumes.usage') }}<select v-model="usage"><option value="">{{ t('timeline.all') }}</option><option value="used">{{ t('volumes.used') }}</option><option value="unused">{{ t('volumes.unused') }}</option></select></label></div>
    <p v-if="inventory.data.value && !items.length">{{ t('dockerResources.empty') }}</p>
    <ul><li v-for="item in items" :key="item.node_id + ':' + item.name"><div><strong>{{ item.name }}</strong> · {{ t(`volumes.kind.${item.kind}`) }} · {{ item.driver }}<small>{{ item.node_id }} · {{ item.created_at }}</small><span>{{ t(item.owned ? 'dockerResources.owned' : 'dockerResources.external') }}</span><p v-for="r in item.used_by" :key="r.kind + r.id + r.target">{{ t(`volumes.ref.${r.kind}`) }}: {{ r.name }} {{ r.target }} {{ r.state }}</p><p v-for="r in item.blocked_reasons" :key="r">{{ t(`volumes.block.${r}`) }}</p></div><div><button class="btn" :disabled="busy" @click="detail(item)">{{ t('volumes.details') }}</button><button v-if="session.role === 'administrator'" class="btn danger" :disabled="busy || !ready || !!item.blocked_reasons.length" @click="detail(item, true)">{{ t('dockerResources.delete') }}</button></div></li></ul>
    <section v-if="selected" class="card detail" role="dialog" :aria-label="t('volumes.details')"><h3>{{ selected.name }}</h3><p>{{ selected.node_id }} · {{ selected.engine_id }}</p><p v-for="r in selected.blocked_reasons" :key="r">{{ t(`volumes.block.${r}`) }}</p><template v-if="deleting"><p class="callout red">{{ t('volumes.deleteWarning') }}</p><label><input v-model="acknowledge" type="checkbox" :disabled="busy">{{ t('volumes.acknowledge') }}</label><button class="btn danger" :disabled="busy || !acknowledge || !ready" @click="remove">{{ t('dockerResources.delete') }}</button></template><template v-else><p>{{ t('volumes.reuse') }}</p><textarea :value="example" readonly rows="12"></textarea><button class="btn" @click="copy">{{ t('volumes.copy') }}</button></template><button class="btn" :disabled="busy" @click="selected = undefined">{{ t('dockerResources.back') }}</button></section>
  </section>
</template>
<style scoped>
.volumes { display:grid; gap:12px; min-width:0; }.volumes .card { padding:16px; }.volumes label { display:grid;gap:6px; }.volumes input,.volumes select,.volumes textarea { width:100%;padding:8px; }.filters { display:flex;flex-wrap:wrap;gap:12px; }.filters label { flex:1;min-width:140px; }.volumes ul { padding:0;list-style:none; }.volumes li { display:flex;justify-content:space-between;gap:12px;border-top:1px solid var(--line);padding:14px 0;overflow-wrap:anywhere; }.volumes small { display:block; }.detail { overflow-wrap:anywhere; }.detail input[type=checkbox] { width:auto; }.detail label { display:flex;align-items:center; }.detail textarea { font-family:monospace; }@media(max-width:600px){.volumes li{flex-direction:column;}}
</style>
