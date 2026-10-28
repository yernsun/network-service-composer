<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { RouterLink } from 'vue-router'
import { request, errorText, session, ApiRequestError } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'

type Policy = components['schemas']['ImageUpdatePolicy']
type Candidate = components['schemas']['ImageUpdateCandidate']
type Capabilities = components['schemas']['EnvironmentCapabilities']
type Hook = components['schemas']['ImageWebhook']
type Check = components['schemas']['ImageCheckState']
const props = defineProps<{ environmentId: string }>()
const form = reactive({ enabled: false, poll: false, webhook: false, automatic: false, interval_seconds: 300, services: [] as string[] })
const loadedFor = ref(''), formRevision = ref(0), busy = ref(false), error = ref(''), message = ref(''), cursor = ref(''), oneTimeSecret = ref('')
const pending = ref<{ row: Candidate; action: 'deploy' | 'retry' | 'ignore' }>()
const admin = computed(() => session.value.role === 'administrator')
const policy = useQuery({
  queryKey: computed(() => ['image-policy', props.environmentId]), retry: false, staleTime: 0, refetchOnMount: 'always',
  queryFn: async ({ signal, queryKey }) => {
    const id = queryKey[1] as string
    const result = await request('get', '/environments/{id}/update-policy', { params: { path: { id } }, signal, cache: 'no-store' })
    return { id, value: result.data as Policy }
  },
})
const capabilities = useQuery({
  queryKey: computed(() => ['environment-capabilities', props.environmentId]), retry: false, staleTime: 0, refetchOnMount: 'always',
  queryFn: async ({ signal, queryKey }) => (await request('get', '/environments/{id}/capabilities', { params: { path: { id: queryKey[1] as string } }, signal, cache: 'no-store' })).data as Capabilities,
})
const check = useQuery({
  queryKey: computed(() => ['image-check', props.environmentId]), retry: false, staleTime: 0, refetchOnMount: 'always', refetchInterval: 5000,
  queryFn: async ({ signal, queryKey }) => (await request('get', '/environments/{id}/image-checks', { params: { path: { id: queryKey[1] as string } }, signal, cache: 'no-store' })).data as Check,
})
const candidates = useQuery({
  queryKey: computed(() => ['image-candidates', props.environmentId, cursor.value]), retry: false, staleTime: 0, refetchOnMount: 'always', refetchInterval: 5000,
  queryFn: async ({ signal, queryKey }) => {
    const result = await request('get', '/environments/{id}/image-updates', { params: { path: { id: queryKey[1] as string }, query: { limit: 20, cursor: queryKey[2] } }, signal, cache: 'no-store' })
    return { rows: result.data as Candidate[], next: result.pagination?.next_cursor || '' }
  },
})
const hook = useQuery({
  queryKey: computed(() => ['image-hook', props.environmentId]), retry: false, staleTime: 0, refetchOnMount: 'always',
  queryFn: async ({ signal, queryKey }) => (await request('get', '/environments/{id}/webhook', { params: { path: { id: queryKey[1] as string } }, signal, cache: 'no-store' })).data as Hook,
})
const saved = computed(() => policy.data.value?.id === props.environmentId ? policy.data.value.value : undefined)
const cap = computed(() => capabilities.data.value?.environment_id === props.environmentId ? capabilities.data.value : undefined)
function loadSaved(value: Policy) {
  Object.assign(form, { enabled: value.enabled, poll: value.poll, webhook: value.webhook, automatic: value.revision === 0 ? false : value.automatic, interval_seconds: value.interval_seconds, services: [...value.services] })
  formRevision.value = value.revision; loadedFor.value = props.environmentId
}
watch(saved, value => { if (value && loadedFor.value !== props.environmentId) loadSaved(value) }, { immediate: true })
watch(() => props.environmentId, () => { cursor.value = ''; oneTimeSecret.value = ''; pending.value = undefined; error.value = ''; message.value = '' })
const changed = computed(() => !!saved.value && loadedFor.value === props.environmentId && saved.value.revision !== formRevision.value)
const inaccessible = computed(() => [policy, capabilities, check, candidates, hook].some(query => query.error.value instanceof ApiRequestError && [401, 403].includes(query.error.value.status)))
const failed = computed(() => [policy, capabilities, check, candidates, hook].some(query => query.isError.value))
watch([admin, inaccessible], ([allowed, denied]) => { if (!allowed || denied) { oneTimeSecret.value = ''; pending.value = undefined } })
const freshPolicy = computed(() => !!saved.value && !policy.isError.value && !policy.isFetching.value)
const baseline = computed(() => !!cap.value && !capabilities.isError.value && !capabilities.isFetching.value && cap.value.image_updates.state === 'BASELINE_READY')
const canWrite = computed(() => admin.value && !busy.value && !inaccessible.value && freshPolicy.value && !changed.value)
const canSave = computed(() => canWrite.value && cap.value?.management.state === 'CONFIGURED' && !capabilities.isError.value && Number.isInteger(form.interval_seconds) && form.interval_seconds >= 60 && form.interval_seconds <= 86400 && (!form.enabled || baseline.value && (form.poll || form.webhook) && form.services.length > 0 && form.services.every(name => cap.value!.services.some(service => service.name === name)) && (!form.webhook || !!hook.data.value?.hook_id && !hook.isError.value)))
const serviceNames = computed(() => [...new Set([...(cap.value?.services.map(service => service.name) || []), ...form.services])].sort())
const latestRows = computed(() => inaccessible.value ? [] : candidates.data.value?.rows || [])
function actionAllowed(row: Candidate, action: 'deploy' | 'retry' | 'ignore') {
  const current = candidates.data.value?.rows.find(item => item.id === row.id)
  if (!current || current.revision !== row.revision || current.status !== row.status || current.operation_id !== row.operation_id) return false
  if (!canWrite.value || candidates.isError.value || candidates.isFetching.value) return false
  if (action === 'ignore') return cap.value?.management.state === 'CONFIGURED' && !capabilities.isError.value && !row.operation_id && ['AVAILABLE', 'FAILED'].includes(row.status)
  if (!baseline.value || row.base_revision_id !== cap.value?.base_revision_id) return false
  if (action === 'deploy') return row.status === 'AVAILABLE'
  return ['FAILED', 'ROLLED_BACK', 'ROLLBACK_FAILED', 'CANCELLED'].includes(row.status)
}
const path = () => ({ path: { id: props.environmentId } })
const headers = (revision: number) => ({ 'If-Match': `"rev-${revision}"` })
async function refresh() { await Promise.all([policy.refetch(), capabilities.refetch(), check.refetch(), candidates.refetch(), hook.refetch()]) }
async function action(work: () => Promise<unknown>, success: string) {
  if (!canWrite.value) return
  busy.value = true; error.value = ''; message.value = ''
  try { await work(); message.value = success } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false; pending.value = undefined; await refresh() }
}
async function save() {
  if (!canSave.value) return
  await action(async () => { const result = await request('put', '/environments/{id}/update-policy', { params: path(), headers: headers(formRevision.value), body: { ...form, services: [...form.services] } }); loadSaved(result.data as Policy) }, 'imageUpdates.saved')
}
async function checkNow() {
  if (!saved.value?.enabled || !baseline.value || check.isError.value || check.isFetching.value || check.data.value?.requested) return
  await action(() => request('post', '/environments/{id}/image-checks', { params: path() }), 'imageUpdates.checkQueued')
}
async function confirmCandidate() {
  const choice = pending.value
  if (!choice || !actionAllowed(choice.row, choice.action)) return
  await action(() => request('post', '/environments/{id}/image-updates/{updateID}', { params: { path: { id: props.environmentId, updateID: choice.row.id } }, headers: headers(choice.row.revision), body: { action: choice.action } }), 'imageUpdates.actionQueued')
}
async function rotateHook() {
  if (hook.isError.value || hook.isFetching.value || !hook.data.value) return
  oneTimeSecret.value = ''
  await action(async () => { const result = await request('post', '/environments/{id}/webhook', { params: path(), headers: headers(hook.data.value?.revision || 0) }); oneTimeSecret.value = (result.data as { secret: string }).secret }, 'imageUpdates.hookRotated')
}
function changedResource() { void refresh() }
onMounted(() => window.addEventListener('nsc:resources-changed', changedResource))
onUnmounted(() => { window.removeEventListener('nsc:resources-changed', changedResource); oneTimeSecret.value = '' })
const states = new Set(['AVAILABLE', 'SUBMITTING', 'QUEUED', 'RUNNING', 'SUCCEEDED', 'FAILED', 'ROLLED_BACK', 'ROLLBACK_FAILED', 'CANCELLED', 'IGNORED'])
function status(value: string) { return t(`imageUpdates.status.${states.has(value) ? value : 'UNKNOWN'}`) }
function time(value: string | undefined) { return value ? formatDate(value) : t('imageUpdates.never') }
</script>

<template>
  <section class="card image-updates" data-testid="image-updates">
    <div class="update-heading"><h2>{{ t('imageUpdates.title') }}</h2><button class="btn" type="button" :disabled="busy" data-testid="image-refresh" @click="refresh">{{ t('notifications.refresh') }}</button></div>
    <p>{{ t('imageUpdates.boundary') }}</p>
    <p v-if="failed" class="callout amber" role="alert">{{ t('imageUpdates.readFailed') }}</p>
    <p v-if="error" class="callout red" role="alert">{{ error }}</p>
    <p v-if="message" class="callout green" role="status">{{ t(message) }}</p>
    <template v-if="saved && !inaccessible">
      <p v-if="changed" class="callout amber" role="alert">{{ t('notifications.changed') }}<button class="btn" type="button" @click="loadSaved(saved)">{{ t('notifications.reload') }}</button></p>
      <p v-if="cap && !capabilities.isError.value && cap.image_updates.state !== 'BASELINE_READY'" class="callout amber">{{ t('imageUpdates.baselineRequired') }}</p>
      <form @submit.prevent="save">
        <fieldset :disabled="!admin || busy || !freshPolicy || changed">
          <label><input v-model="form.enabled" type="checkbox" data-testid="image-enabled" />{{ t('imageUpdates.enabled') }}</label>
          <label><input v-model="form.poll" type="checkbox" data-testid="image-poll" />{{ t('imageUpdates.poll') }}</label>
          <label>{{ t('imageUpdates.interval') }}<input v-model.number="form.interval_seconds" type="number" min="60" max="86400" required /></label>
          <label><input v-model="form.webhook" type="checkbox" />{{ t('imageUpdates.webhook') }}</label>
          <label><input v-model="form.automatic" type="checkbox" data-testid="image-automatic" />{{ t('imageUpdates.automatic') }}</label>
          <p class="callout amber">{{ t('imageUpdates.automaticWarning') }}</p>
          <fieldset><legend>{{ t('imageUpdates.services') }}</legend><label v-for="name in serviceNames" :key="name"><input v-model="form.services" type="checkbox" :value="name" />{{ name }}<code>{{ cap?.services.find(service => service.name === name)?.reference || t('imageUpdates.unavailable') }}</code></label></fieldset>
          <button class="btn primary" type="submit" :disabled="!canSave">{{ t('notifications.save') }}</button>
        </fieldset>
      </form>
      <dl class="check-state"><div><dt>{{ t('imageUpdates.lastCheck') }}</dt><dd>{{ check.isError.value ? t('imageUpdates.status.UNKNOWN') : time(check.data.value?.last_checked) }}</dd></div><div><dt>{{ t('imageUpdates.lastSuccess') }}</dt><dd>{{ time(check.data.value?.last_success) }}</dd></div></dl>
      <p v-if="check.data.value?.error && !check.isError.value" class="callout amber">{{ t('imageUpdates.checkFailed') }}</p>
      <button v-if="admin" class="btn" type="button" data-testid="image-check" :disabled="!canWrite || !baseline || !saved.enabled || check.isError.value || check.isFetching.value || check.data.value?.requested" @click="checkNow">{{ check.data.value?.requested ? t('imageUpdates.checkQueued') : t('imageUpdates.checkNow') }}</button>
      <details class="update-hook"><summary>{{ t('imageUpdates.webhook') }}</summary><p>{{ t('imageUpdates.hookWarning') }}</p><code v-if="hook.data.value?.hook_id">/api/v1/hooks/image-updates/{{ hook.data.value.hook_id }}</code><button v-if="admin" class="btn" type="button" data-testid="image-hook" :disabled="!canWrite || hook.isError.value || hook.isFetching.value" @click="rotateHook">{{ t('imageUpdates.rotateHook') }}</button><label v-if="oneTimeSecret">{{ t('imageUpdates.oneTimeSecret') }}<input :value="oneTimeSecret" type="password" readonly autocomplete="off" /><button class="btn" type="button" @click="oneTimeSecret = ''">{{ t('imageUpdates.clearSecret') }}</button></label></details>
      <h3>{{ t('imageUpdates.candidates') }}</h3>
      <p v-if="candidates.isError.value">{{ t('imageUpdates.historical') }}</p>
      <article v-for="row in latestRows" :key="row.id" class="update-candidate" :data-candidate="row.id">
        <div class="update-heading"><strong>{{ row.id }}</strong><span>{{ candidates.isError.value ? t('imageUpdates.status.UNKNOWN') : status(row.status) }}</span></div>
        <p>{{ t('imageUpdates.baseRevision') }} <code>{{ row.base_revision_id }}</code></p>
        <p v-if="row.base_revision_id !== cap?.base_revision_id" class="callout amber">{{ t('imageUpdates.superseded') }}</p>
        <ul><li v-for="name in row.changed" :key="name"><strong>{{ name }}</strong><code v-if="row.base_revision_id === cap?.base_revision_id">{{ cap?.services.find(service => service.name === name)?.image }}</code><span>→</span><code>{{ row.images[name] }}</code></li></ul>
        <p v-if="row.error">{{ t(row.operation_id ? 'imageUpdates.candidateFailed' : 'imageUpdates.submissionFailed') }}</p>
        <RouterLink v-if="row.operation_id" class="btn" :to="`/operations/${encodeURIComponent(row.operation_id)}`">{{ t('imageUpdates.operation') }}</RouterLink>
        <div v-if="admin" class="update-actions"><button v-for="kind in (['deploy', 'retry', 'ignore'] as const)" :key="kind" class="btn" type="button" :disabled="!actionAllowed(row, kind)" @click="pending = { row, action: kind }">{{ t(`imageUpdates.${kind}`) }}</button></div>
      </article>
      <p v-if="!latestRows.length && !candidates.isError.value">{{ t('imageUpdates.empty') }}</p>
      <div v-if="pending" class="callout amber" role="alert" data-testid="image-confirm"><p>{{ t('imageUpdates.confirm') }} <strong>{{ pending.row.id }}</strong> · {{ t(`imageUpdates.${pending.action}`) }}</p><button class="btn primary" type="button" :disabled="!actionAllowed(pending.row, pending.action)" @click="confirmCandidate">{{ t('imageUpdates.confirmAction') }}</button><button class="btn" type="button" @click="pending = undefined">{{ t('imageUpdates.cancel') }}</button></div>
      <div class="update-actions"><button v-if="cursor" class="btn" type="button" @click="cursor = ''">{{ t('notifications.first') }}</button><button v-if="candidates.data.value?.next && !candidates.isError.value" class="btn" type="button" @click="cursor = candidates.data.value!.next">{{ t('notifications.next') }}</button></div>
    </template>
  </section>
</template>

<style scoped>
.image-updates { padding: 22px; margin: 20px 0; min-width: 0; }
.update-heading, .update-actions { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; }
form { max-width: 740px; margin: 18px 0; }
fieldset { display: grid; gap: 14px; border: 1px solid var(--border); padding: 14px; min-width: 0; }
label { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
input[type=number], input[type=password] { min-width: 0; max-width: 100%; }
code, .update-candidate strong { overflow-wrap: anywhere; }
.update-hook { margin: 20px 0; }
.update-hook > code, .update-hook > button { display: block; margin: 12px 0; }
.check-state > div { display: flex; flex-wrap: wrap; gap: 12px; }
.check-state dd { margin: 0; }
.update-candidate { border-top: 1px solid var(--border); padding: 18px 0; }
.update-candidate li { display: grid; gap: 6px; margin: 10px 0; }
</style>
