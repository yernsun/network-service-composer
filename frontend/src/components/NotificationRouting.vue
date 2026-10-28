<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, session, errorText, ApiRequestError } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'
import { randomID } from '../shared/browser/random'

type Receiver = components['schemas']['NotificationReceiver']
type Rule = components['schemas']['NotificationRoute']
type Silence = components['schemas']['NotificationSilence']
type Issue = components['schemas']['Issue']
const admin = computed(() => session.value.authenticated && session.value.role === 'administrator')
const busy = ref(false), error = ref(''), message = ref(''), password = ref('')
const data = useQuery({ queryKey: computed(() => ['notification-routing', session.value.user_id, session.value.role]), enabled: admin, retry: false, staleTime: 0, refetchInterval: 10000,
  queryFn: async ({ signal }) => {
    const read = (path: Parameters<typeof request>[1]) => request('get', path, { signal, cache: 'no-store' })
    const all = async (path: Parameters<typeof request>[1], params?: Record<string, string>) => {
      const rows: unknown[] = []; let cursor = ''
      do { const response = await request('get', path, { signal, cache: 'no-store', params: { ...(params ? { path: params } : {}), query: { limit: 200, cursor } } }); rows.push(...response.data as unknown[]); cursor = response.pagination?.next_cursor || '' } while (cursor)
      return { data: rows }
    }
    const [receivers, routes, silences, health, issues, projects] = await Promise.all([
      read('/notification-receivers'), read('/notification-routes'), read('/notification-silences'), read('/notification-health'), all('/issues'), all('/projects'),
    ])
    const projectRows = projects.data as components['schemas']['LogicalProject'][]
    const environments = (await Promise.all(projectRows.map(project => all('/projects/{id}/environments', { id: project.id })))).flatMap(result => result.data)
    return { receivers: receivers.data as Receiver[], routes: routes.data as Rule[], silences: silences.data as Silence[], health: health.data as components['schemas']['NotificationHealth'], issues: issues.data as Issue[], projects: projectRows, environments: environments as components['schemas']['ProjectEnvironment'][] }
  },
})
const inaccessible = computed(() => !admin.value || data.error.value instanceof ApiRequestError && [401, 403].includes(data.error.value.status))
const canWrite = computed(() => !busy.value && !data.isError.value && !data.isFetching.value && !!data.data.value && !inaccessible.value)
const receiver = reactive({ id: '', revision: 0, name: '', enabled: true, endpoint: '', secret: '' })
const route = reactive({ scope_type: 'global' as Rule['scope_type'], scope_id: '', revision: 0, base_settings_revision: 0, enabled: true, receiver_ids: [] as string[], events: ['images', 'deployments', 'certificates', 'gateways', 'runtimes'], severities: [] as string[] })
const silence = reactive({ scope_type: 'environment' as Silence['scope_type'], scope_id: '', reason: '', hours: 1 })
const selectedIssue = ref(''), preview = ref<components['schemas']['NotificationRoutingPreview']>()
let previewSequence = 0
let routeInitialized = false
const groups = ['images', 'deployments', 'certificates', 'gateways', 'runtimes']
const severities = ['info', 'warning', 'critical']
const scopeOptions = computed(() => silence.scope_type === 'issue' ? data.data.value?.issues.filter(i => i.active).map(i => ({ id: i.id, name: `${t(`community.issue.${i.code}`)} · ${i.resource_id}` })) : silence.scope_type === 'project' ? data.data.value?.projects : data.data.value?.environments)
const routeOptions = computed(() => route.scope_type === 'project' ? data.data.value?.projects : data.data.value?.environments)
const receiverStale = computed(() => receiver.id && data.data.value?.receivers.find(r => r.id === receiver.id)?.revision !== receiver.revision)
const routeStale = computed(() => {
  const saved = data.data.value?.routes.find(r => r.scope_type === route.scope_type && r.scope_id === route.scope_id)
  return (saved?.revision || 0) !== route.revision || route.scope_type === 'global' && route.revision === 0 && (saved?.base_settings_revision || 0) !== route.base_settings_revision
})
function resetReceiver() { Object.assign(receiver, { id: '', revision: 0, name: '', enabled: true, endpoint: '', secret: '' }) }
function editReceiver(value: Receiver) { Object.assign(receiver, { id: value.id, revision: value.revision, name: value.name, enabled: value.enabled, endpoint: '', secret: '' }) }
function editRoute(value: Rule) { Object.assign(route, { ...value, receiver_ids: [...value.receiver_ids], events: [...value.events], severities: [...value.severities] }) }
function selectRoute() {
  const value = data.data.value?.routes.find(r => r.scope_type === route.scope_type && r.scope_id === route.scope_id)
  if (value) editRoute(value)
  else Object.assign(route, { revision: 0, base_settings_revision: 0, enabled: true, receiver_ids: [], events: [...groups], severities: [] })
}
watch(selectedIssue, () => { previewSequence++; preview.value = undefined })
watch(() => silence.scope_type, () => { silence.scope_id = '' })
watch(inaccessible, denied => { if (denied) { previewSequence++; resetReceiver(); password.value = ''; preview.value = undefined } })
watch(() => data.data.value, value => { if (value && !routeInitialized) { selectRoute(); routeInitialized = true } })
function headers(revision: number) { return { 'If-Match': `"rev-${revision}"`, 'X-NSC-Reauthenticate': password.value } }
async function action(work: () => Promise<unknown>) {
  if (!canWrite.value) return
  busy.value = true; error.value = ''; message.value = ''
  try { await work(); message.value = 'notifications.saved' } catch (cause) { error.value = errorText(cause) }
  finally { password.value = ''; receiver.secret = ''; busy.value = false; await data.refetch() }
}
async function saveReceiver() {
  if (receiverStale.value) return
  await action(async () => {
    const id = receiver.id || `receiver_${randomID().replace(/-/g, '')}`
    const result = await request('put', '/notification-receivers/{id}', { params: { path: { id } }, headers: headers(receiver.revision), body: { name: receiver.name, enabled: receiver.enabled, ...(receiver.endpoint ? { endpoint: receiver.endpoint } : {}), ...(receiver.secret ? { secret: receiver.secret } : {}) } })
    editReceiver(result.data as Receiver)
  })
}
async function saveRoute() {
  if (routeStale.value) return
  await action(async () => {
    const result = await request('put', '/notification-routes', { headers: headers(route.revision), body: { scope_type: route.scope_type, scope_id: route.scope_id, base_settings_revision: route.base_settings_revision, enabled: route.enabled, receiver_ids: route.receiver_ids, events: route.events, severities: route.severities } })
    editRoute(result.data as Rule)
  })
}
async function saveSilence() { await action(() => request('put', '/notification-silences/{id}', { params: { path: { id: `silence_${randomID().replace(/-/g, '')}` } }, headers: headers(0), body: { scope_type: silence.scope_type, scope_id: silence.scope_id, reason: silence.reason, expires_at: new Date(Date.now() + silence.hours * 3600000).toISOString() } })) }
async function showPreview() {
  error.value = ''; preview.value = undefined
  const sequence = ++previewSequence, issueID = selectedIssue.value
  try { const result = await request('post', '/notification-routing/preview', { body: { issue_id: issueID } }); if (sequence === previewSequence && issueID === selectedIssue.value && !inaccessible.value) preview.value = result.data as components['schemas']['NotificationRoutingPreview'] } catch (cause) { if (sequence === previewSequence) error.value = errorText(cause) }
}
function receiverName(value: Receiver) { return value.id === 'default' ? t('routing.defaultReceiver') : value.name }
</script>

<template>
  <section v-if="admin" class="card routing-panel" data-testid="notification-routing">
    <div class="routing-heading"><h2>{{ t('routing.title') }}</h2><button class="btn" :disabled="busy" data-testid="routing-refresh" @click="data.refetch()">{{ t('notifications.refresh') }}</button></div>
    <p>{{ t('routing.description') }}</p>
    <p v-if="data.isError.value" class="callout amber" role="alert">{{ t('notifications.readFailed') }}</p>
    <p v-if="error" class="callout red" role="alert">{{ error }}</p><p v-if="message" class="callout green" role="status">{{ t(message) }}</p>
    <template v-if="data.data.value && !inaccessible">
      <div class="callout" data-testid="routing-health"><strong>{{ t('routing.health') }}</strong><p v-if="!data.data.value.health.initialized">{{ t('routing.initializing') }}</p><p v-for="source in data.data.value.health.sources" :key="source.group">{{ source.group === 'routing' ? t('routing.routes') : t(`notifications.group.${source.group}`) }} · {{ t(source.state === 'CURRENT' ? 'routing.lastCollected' : source.state === 'FAILED' ? 'routing.collectionFailed' : 'routing.collectionUnknown') }} · {{ formatDate(source.attempted_at) }}</p><p v-for="count in data.data.value.health.queue" :key="`${count.kind}:${count.status}`">{{ t(`routing.queue.${count.kind}`) }} · {{ t(`routing.state.${count.status}`) }}: {{ count.count }}</p></div>
      <label class="routing-password">{{ t('notifications.password') }}<input v-model="password" type="password" autocomplete="current-password" /></label>
      <div class="routing-grid">
        <section>
          <h3>{{ t('routing.receivers') }}</h3>
          <ul class="routing-list"><li v-for="value in data.data.value.receivers" :key="value.id"><span>{{ receiverName(value) }}<small>{{ value.endpoint_host }} · {{ t(value.enabled ? 'notifications.enabled' : 'routing.disabled') }}</small></span><button v-if="value.id !== 'default'" class="btn sm" @click="editReceiver(value)">{{ t('routing.edit') }}</button><button class="btn sm" :disabled="!canWrite || !value.enabled" @click="action(() => request('post', '/notification-receivers/{id}/test', { params: { path: { id: value.id } }, headers: headers(value.revision) }))">{{ t('notifications.test') }}</button></li></ul>
          <form class="routing-form" data-testid="receiver-form" @submit.prevent="saveReceiver">
            <p v-if="receiverStale" class="callout amber">{{ t('notifications.changed') }}</p>
            <label>{{ t('routing.name') }}<input v-model="receiver.name" required maxlength="100" /></label><label class="routing-toggle"><input v-model="receiver.enabled" type="checkbox" />{{ t('notifications.enabled') }}</label>
            <label>{{ t('notifications.endpoint') }}<input v-model="receiver.endpoint" type="password" autocomplete="off" /></label><label>{{ t('notifications.secret') }}<input v-model="receiver.secret" type="password" autocomplete="new-password" minlength="16" maxlength="256" /></label><p class="muted">{{ t('notifications.keepSecrets') }}</p>
            <div class="routing-actions"><button class="btn primary" :disabled="!canWrite || !!receiverStale">{{ t('notifications.save') }}</button><button class="btn" type="button" @click="resetReceiver">{{ t('routing.newReceiver') }}</button></div>
          </form>
        </section>
        <section>
          <h3>{{ t('routing.routes') }}</h3>
          <ul class="routing-list"><li v-for="value in data.data.value.routes" :key="value.id"><span>{{ t(`routing.scope.${value.scope_type}`) }}<small>{{ value.scope_id }} · {{ value.receiver_ids.join(', ') }}</small></span><button class="btn sm" @click="editRoute(value)">{{ t('routing.edit') }}</button></li></ul>
          <form class="routing-form" data-testid="route-form" @submit.prevent="saveRoute">
            <p v-if="routeStale" class="callout amber">{{ t('notifications.changed') }}</p>
            <label>{{ t('routing.scopeLabel') }}<select v-model="route.scope_type" @change="route.scope_id = ''; selectRoute()"><option v-for="scope in ['global', 'project', 'environment']" :key="scope" :value="scope">{{ t(`routing.scope.${scope}`) }}</option></select></label>
            <label v-if="route.scope_type !== 'global'">{{ t('routing.target') }}<select v-model="route.scope_id" required @change="selectRoute"><option value="">{{ t('routing.select') }}</option><option v-for="scope in routeOptions" :key="scope.id" :value="scope.id">{{ scope.name }}</option></select></label>
            <label class="routing-toggle"><input v-model="route.enabled" type="checkbox" />{{ t('notifications.enabled') }}</label>
            <fieldset><legend>{{ t('routing.receivers') }}</legend><label v-for="value in data.data.value.receivers" :key="value.id" class="routing-toggle"><input v-model="route.receiver_ids" type="checkbox" :value="value.id" />{{ receiverName(value) }}</label></fieldset>
            <fieldset><legend>{{ t('notifications.events') }}</legend><label v-for="group in groups" :key="group" class="routing-toggle"><input v-model="route.events" type="checkbox" :value="group" />{{ t(`notifications.group.${group}`) }}</label></fieldset>
            <fieldset><legend>{{ t('routing.severity') }}</legend><label v-for="severity in severities" :key="severity" class="routing-toggle"><input v-model="route.severities" type="checkbox" :value="severity" />{{ t(`routing.severityValue.${severity}`) }}</label></fieldset>
            <button class="btn primary" :disabled="!canWrite || !!routeStale">{{ t('notifications.save') }}</button>
          </form>
        </section>
      </div>
      <div class="routing-grid">
        <section>
          <h3>{{ t('routing.silences') }}</h3><p>{{ t('routing.silenceHelp') }}</p>
          <form class="routing-form" data-testid="silence-form" @submit.prevent="saveSilence">
            <label>{{ t('routing.scopeLabel') }}<select v-model="silence.scope_type"><option v-for="scope in ['global', 'project', 'environment', 'issue']" :key="scope" :value="scope">{{ t(`routing.scope.${scope}`) }}</option></select></label>
            <label v-if="silence.scope_type !== 'global'">{{ t('routing.target') }}<select v-model="silence.scope_id" required><option value="">{{ t('routing.select') }}</option><option v-for="scope in scopeOptions" :key="scope.id" :value="scope.id">{{ scope.name }}</option></select></label>
            <label>{{ t('routing.reason') }}<input v-model="silence.reason" required maxlength="512" /></label><label>{{ t('routing.hours') }}<input v-model="silence.hours" type="number" min="0.0167" max="168" step="any" required /></label><button class="btn" :disabled="!canWrite">{{ t('routing.createSilence') }}</button>
          </form>
          <ul class="routing-list"><li v-for="value in data.data.value.silences" :key="value.id"><span>{{ value.reason }}<small>{{ t(`routing.scope.${value.scope_type}`) }} · {{ value.scope_id }} · {{ value.actor }} · {{ formatDate(value.expires_at) }}</small></span><button v-if="value.active" class="btn sm" :disabled="!canWrite" @click="action(() => request('post', '/notification-silences/{id}/cancel', { params: { path: { id: value.id } }, headers: headers(value.revision) }))">{{ t('routing.endSilence') }}</button></li></ul>
        </section>
        <section>
          <h3>{{ t('routing.preview') }}</h3><p>{{ t('routing.previewHelp') }}</p><form class="routing-form" data-testid="routing-preview" @submit.prevent="showPreview"><label>{{ t('routing.scope.issue') }}<select v-model="selectedIssue" required><option value="">{{ t('routing.select') }}</option><option v-for="issue in data.data.value.issues" :key="issue.id" :value="issue.id">{{ t(`community.issue.${issue.code}`) }} · {{ issue.resource_id }}</option></select></label><button class="btn" :disabled="busy || !selectedIssue || data.isError.value">{{ t('routing.preview') }}</button></form>
          <div v-if="preview" role="status"><p v-if="preview.globally_silenced || preview.silenced_scopes.length">{{ t('routing.silenced') }}</p><p v-if="!preview.matches.length">{{ t('routing.noRoute') }}</p><ul><li v-for="match in preview.matches" :key="match.receiver_id">{{ match.receiver_id }} · {{ match.scopes.map(s => s.environment_id).join(', ') || t('routing.scope.global') }}</li></ul></div>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
.routing-panel { padding: 22px; margin-top: 22px; min-width: 0; }
.routing-heading, .routing-actions, .routing-list li { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.routing-heading { justify-content: space-between; }.routing-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 24px; margin-top: 20px; }.routing-grid > section { min-width: 0; }
.routing-form { display: grid; gap: 12px; }.routing-form label, .routing-password { display: grid; gap: 6px; }.routing-password { max-width: 420px; }.routing-form input:not([type=checkbox]), .routing-form select { width: 100%; min-width: 0; }.routing-form .routing-toggle { display: flex; align-items: center; gap: 8px; }.routing-form fieldset { display: grid; gap: 8px; min-width: 0; border: 1px solid var(--border); padding: 12px; }.routing-list { padding: 0; list-style: none; }.routing-list li { border-bottom: 1px solid var(--border); padding: 10px 0; }.routing-list li > span { flex: 1; min-width: 120px; overflow-wrap: anywhere; }.routing-list small { display: block; color: var(--muted); overflow-wrap: anywhere; }
@media (max-width: 900px) { .routing-grid { grid-template-columns: minmax(0,1fr); }.routing-panel { padding: 16px; } }
</style>
