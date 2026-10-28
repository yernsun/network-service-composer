<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, errorText, session, ApiRequestError } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'

type Settings = components['schemas']['NotificationSettings']
type Notice = components['schemas']['Notification']
const groups = ['images', 'deployments', 'certificates', 'gateways', 'runtimes'] as const
const form = reactive({ enabled: false, events: [] as string[], endpoint: '', secret: '', password: '' })
const busy = ref(false), message = ref(''), error = ref(''), initialized = ref(false), formRevision = ref(0), cursor = ref('')
const receiverFilter = ref('')
const admin = computed(() => session.value.role === 'administrator')
const settings = useQuery({
  queryKey: ['notification-settings'], enabled: admin, retry: false, staleTime: 0, refetchOnMount: 'always',
  queryFn: async ({ signal }) => {
    const result = await request('get', '/notification-settings', { signal, cache: 'no-store' })
    const value = result.data as Settings
    if (!initialized.value) loadSaved(value)
    return value
  },
})
const history = useQuery({
  queryKey: computed(() => ['notifications', cursor.value, receiverFilter.value]), enabled: admin, retry: false, refetchOnMount: 'always', staleTime: 0, refetchInterval: 5000,
  queryFn: async ({ signal, queryKey }) => {
    const result = await request('get', '/notifications', { signal, cache: 'no-store', params: { query: { limit: 20, cursor: queryKey[1], receiver_id: queryKey[2] } } })
    return { rows: result.data as Notice[], next: result.pagination?.next_cursor || '' }
  },
})
const receivers = useQuery({ queryKey: ['notification-receivers'], enabled: admin, retry: false, staleTime: 0, queryFn: async ({ signal }) => (await request('get', '/notification-receivers', { signal, cache: 'no-store' })).data as components['schemas']['NotificationReceiver'][] })
watch(receiverFilter, () => { cursor.value = '' })
const inaccessible = computed(() => [settings.error.value, history.error.value, receivers.error.value].some(e => e instanceof ApiRequestError && [401, 403].includes(e.status)))
const changed = computed(() => initialized.value && settings.data.value && settings.data.value.revision !== formRevision.value)
function loadSaved(value: Settings) { form.enabled = value.enabled; form.events = [...value.events]; formRevision.value = value.revision; initialized.value = true; form.endpoint = ''; form.secret = ''; form.password = '' }
const canWrite = computed(() => !changed.value && admin.value && !busy.value && !settings.isFetching.value && !settings.isError.value && !inaccessible.value && !!settings.data.value)
async function refresh() { await Promise.all([settings.refetch(), history.refetch(), receivers.refetch()]) }
function receiverEnabled(row: Notice) { return !row.receiver_id || row.receiver_id === 'default' ? settings.data.value?.enabled : !receivers.isError.value && !!receivers.data.value?.find(receiver => receiver.id === row.receiver_id)?.enabled }
function receiverName(row: Notice) { return !row.receiver_id || row.receiver_id === 'default' ? t('routing.defaultReceiver') : receivers.data.value?.find(receiver => receiver.id === row.receiver_id)?.name || row.receiver_id }
async function action(work: () => Promise<unknown>, success: string) {
  if (!canWrite.value) return
  busy.value = true; message.value = ''; error.value = ''
  try { await work(); message.value = success } catch (cause) { error.value = errorText(cause) }
  finally { form.password = ''; form.secret = ''; busy.value = false; await refresh() }
}
async function save() {
  const body = { enabled: form.enabled, events: form.events, ...(form.endpoint ? { endpoint: form.endpoint } : {}), ...(form.secret ? { secret: form.secret } : {}) }
  await action(async () => {
    const result = await request('put', '/notification-settings', { headers: headers(settings.data.value!.revision), body })
    loadSaved(result.data as Settings)
  }, 'notifications.saved')
}
function headers(revision: number) { return { 'If-Match': `"rev-${revision}"`, 'X-NSC-Reauthenticate': form.password } }
async function test() { await action(() => request('post', '/notifications/test', { headers: headers(settings.data.value!.revision) }), 'notifications.queued') }
async function retry(row: Notice) { await action(() => request('post', '/notifications/{id}/retry', { params: { path: { id: row.id } }, headers: headers(row.revision) }), 'notifications.queued') }
</script>

<template>
  <section v-if="admin" class="card notification-settings" data-testid="notification-settings">
    <div class="notification-heading"><h2>{{ t('notifications.title') }}</h2><button class="btn" type="button" :disabled="busy" data-testid="notification-refresh" @click="refresh">{{ t('notifications.refresh') }}</button></div>
    <p>{{ t('notifications.description') }}</p>
    <p v-if="settings.isError.value || history.isError.value" class="callout amber" role="alert">{{ t('notifications.readFailed') }}</p>
    <p v-if="error" class="callout red" role="alert">{{ error }}</p>
    <p v-if="message" class="callout green" role="status">{{ t(message) }}</p>
    <template v-if="settings.data.value && !inaccessible">
      <p v-if="changed" class="callout amber" role="alert">{{ t('notifications.changed') }}<button class="btn" type="button" :disabled="busy" @click="loadSaved(settings.data.value!)">{{ t('notifications.reload') }}</button></p>
      <form class="notification-form" @submit.prevent="save">
        <label class="notification-toggle"><input v-model="form.enabled" type="checkbox" />{{ t('notifications.enabled') }}</label>
        <fieldset><legend>{{ t('notifications.events') }}</legend><label v-for="group in groups" :key="group" class="notification-toggle"><input v-model="form.events" type="checkbox" :value="group" />{{ t(`notifications.group.${group}`) }}</label></fieldset>
        <label>{{ t('notifications.endpoint') }}<input v-model="form.endpoint" type="password" autocomplete="off" :placeholder="settings.data.value.endpoint_host || 'https://receiver.example.test/webhook'" /></label>
        <label>{{ t('notifications.secret') }}<input v-model="form.secret" type="password" autocomplete="new-password" minlength="16" maxlength="256" /></label>
        <p class="muted">{{ t('notifications.keepSecrets') }}</p>
        <label>{{ t('notifications.password') }}<input v-model="form.password" type="password" autocomplete="current-password" /></label>
        <div class="notification-actions"><button class="btn primary" type="submit" :disabled="!canWrite">{{ t('notifications.save') }}</button><button class="btn" type="button" :disabled="!canWrite || !settings.data.value.enabled" data-testid="notification-test" @click="test">{{ t('notifications.test') }}</button></div>
      </form>
      <h3>{{ t('notifications.history') }}</h3>
      <label>{{ t('routing.receivers') }}<select v-model="receiverFilter" data-testid="notification-receiver-filter"><option value="">{{ t('routing.allReceivers') }}</option><option v-for="receiver in receivers.data.value || []" :key="receiver.id" :value="receiver.id">{{ receiver.id === 'default' ? t('routing.defaultReceiver') : receiver.name }}</option></select></label>
      <div class="notification-table"><table><thead><tr><th>{{ t('notifications.eventLabel') }}</th><th>{{ t('notifications.resource') }}</th><th>{{ t('routing.receivers') }}</th><th>{{ t('notifications.statusLabel') }}</th><th>{{ t('notifications.attempts') }}</th><th>{{ t('notifications.time') }}</th><th>{{ t('notifications.actions') }}</th></tr></thead><tbody><tr v-for="row in history.data.value?.rows || []" :key="row.id"><td>{{ t(`notifications.event.${row.event.type}`) }}</td><td><code>{{ row.event.resource_id }}</code><small>{{ row.event.scopes?.map(scope => scope.environment_id).join(', ') }}</small><small v-if="row.event.issue_id">{{ row.event.issue_id }}</small></td><td>{{ receiverName(row) }}</td><td>{{ t(`notifications.status.${row.status}`) }}<small v-if="row.error_code">{{ row.error_code }}</small></td><td>{{ row.attempts }}</td><td>{{ formatDate(row.event.occurred_at) }}</td><td><button v-if="['FAILED', 'CANCELLED'].includes(row.status)" class="btn sm" type="button" :disabled="!canWrite || !receiverEnabled(row) || history.isError.value" @click="retry(row)">{{ t('notifications.retry') }}</button></td></tr></tbody></table></div>
      <p v-if="!history.data.value?.rows.length">{{ t('notifications.empty') }}</p>
      <div class="notification-actions"><button v-if="cursor" class="btn" type="button" @click="cursor = ''">{{ t('notifications.first') }}</button><button v-if="history.data.value?.next" class="btn" type="button" @click="cursor = history.data.value!.next">{{ t('notifications.next') }}</button></div>
    </template>
  </section>
</template>

<style scoped>
.notification-settings { padding: 22px; margin-top: 22px; min-width: 0; }
.notification-heading, .notification-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.notification-heading { justify-content: space-between; }
.notification-form { display: grid; gap: 14px; max-width: 640px; margin: 20px 0; }
.notification-form > label { display: grid; gap: 6px; }
.notification-form .notification-toggle { display: flex; gap: 8px; align-items: center; }
.notification-form fieldset { display: grid; gap: 10px; border: 1px solid var(--border); padding: 14px; min-width: 0; }
.notification-form input:not([type=checkbox]) { width: 100%; min-width: 0; }
.notification-table { overflow-x: auto; }
.notification-table code { overflow-wrap: anywhere; }
.notification-table small { display: block; }
</style>
