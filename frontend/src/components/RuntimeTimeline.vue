<script setup>
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, errorText, session } from '../shared/api/client'
import { i18n } from '../shared/i18n'
import { statusTone } from '../data/status-tone'
import StatusBadge from './StatusBadge.vue'

const props = defineProps({ state: { type: Object, required: true }, projectId: { type: String, default: '' }, environmentId: { type: String, default: '' }, applicationId: { type: String, default: '' }, serviceId: { type: String, default: '' } })
const allScopes = ref(!props.projectId || !props.environmentId)
const runtimeId = ref('')
const selectedServiceId = ref('')
const category = ref('')
const cursor = ref('')
const previous = ref([])
const timelineText = key => i18n.global.t(`timeline.${key}`)
const scopeRuntimeId = computed(() => {
  if (props.serviceId) return props.state.services.find(item => item.id === props.serviceId)?.runtimeId || ''
  if (props.applicationId) return props.state.apps.find(item => item.id === props.applicationId)?.runtime || ''
  if (!allScopes.value) return props.state.environments.find(item => item.id === props.environmentId)?.runtime_id || ''
  return ''
})
const availableServices = computed(() => props.state.services.filter(item =>
  (allScopes.value || item.project === props.projectId && item.env === props.environmentId) &&
  (!runtimeId.value || item.runtimeId === runtimeId.value)))
const availableRuntimes = computed(() => props.state.runtimes.filter(item => allScopes.value || !scopeRuntimeId.value || item.id === scopeRuntimeId.value))
const filters = computed(() => ({
  ...(allScopes.value || props.applicationId || props.serviceId ? {} : { project_id: props.projectId, environment_id: props.environmentId }),
  ...(props.applicationId ? { application_id: props.applicationId } : {}),
  ...(props.serviceId || selectedServiceId.value ? { service_id: props.serviceId || selectedServiceId.value } : {}),
  ...(runtimeId.value || scopeRuntimeId.value ? { runtime_id: runtimeId.value || scopeRuntimeId.value } : {}),
  ...(category.value ? { category: category.value } : {}),
  ...(cursor.value ? { cursor: cursor.value } : {}),
  limit: 50,
}))
watch([allScopes, runtimeId, selectedServiceId, category], () => { cursor.value = ''; previous.value = [] })
watch(runtimeId, () => { selectedServiceId.value = '' })
const query = useQuery({
  queryKey: computed(() => ['runtime-timeline', filters.value]),
  queryFn: ({ signal }) => request('get', '/timeline', { params: { query: filters.value }, signal }),
  refetchInterval: 15000,
})
const records = computed(() => Array.isArray(query.data.value?.data) ? query.data.value.data : [])
const coverage = computed(() => query.data.value?.coverage)
const incomplete = computed(() => coverage.value?.sources?.filter(source => source.state !== 'collecting') || [])
const runtimeName = id => props.state.runtimes.find(item => item.id === id)?.name || id || '—'
const serviceName = id => props.state.services.find(item => item.id === id)?.name || id || '—'
const scopeName = record => {
  if (record.source === 'gap') return timelineText('runtime')
  if (!record.project_id || !record.environment_id) return timelineText('scopeUnknown')
  const project = props.state.projects.find(item => item.id === record.project_id)
  const environment = props.state.environments.find(item => item.id === record.environment_id)
  return `${project?.name || project?.slug || record.project_id} / ${environment?.name || record.environment_id}`
}
const occurred = value => value ? new Intl.DateTimeFormat(session.value.locale, { dateStyle: 'medium', timeStyle: 'medium', timeZone: 'Asia/Singapore' }).format(new Date(value)) : '—'
const actionNames = { INSTALL: 'deploy', ADOPT: 'deploy', UPGRADE: 'deploy', REDEPLOY: 'deploy', SCALE: 'scale',
  RESTART: 'restart', oom: 'oom', die: 'exit', kill: 'exit', 'health_status: healthy': 'healthy', 'health_status: unhealthy': 'unhealthy',
  create: 'create', update: 'update', remove: 'remove', destroy: 'remove', start: 'start', stop: 'stop', restart: 'restart' }
const action = value => value === 'collection_gap' ? timelineText('gapResult') : actionNames[value] ? timelineText(`action.${actionNames[value]}`) : value
const source = value => timelineText(`source.${value}`)
const result = value => i18n.global.te(`status.${value}`) ? i18n.global.t(`status.${value}`) : value
const recordResult = record => {
  if (record.source === 'operation') return result(record.status)
  if (record.source === 'gap') return timelineText('gapResult')
  if (record.oom_killed) return action('oom')
  if (record.action === 'die' || record.action === 'kill') return timelineText('causeUnknown')
  return record.status && record.status !== 'observed' ? result(record.status) : timelineText('observed')
}
const recordTone = record => {
  if (record.source === 'gap') return 'amber'
  if (record.oom_killed) return 'red'
  if (record.action === 'die' || record.action === 'kill') return 'amber'
  return statusTone(record.status, record.source === 'operation' ? 'operation' : 'resource')
}
const evidence = record => {
  if (record.source === 'operation') return record.operation_id
  if (record.source === 'gap') return `${occurred(record.occurred_at)} – ${occurred(record.gap_end_at)}`
  return [record.actor_type, record.actor_id, record.exit_code === undefined ? '' : `exit ${record.exit_code}`, record.oom_killed ? 'OOM' : ''].filter(Boolean).join(' · ')
}
function nextPage() {
  const next = query.data.value?.pagination?.next_cursor
  if (!next) return
  previous.value.push(cursor.value)
  cursor.value = next
}
function previousPage() { cursor.value = previous.value.pop() || '' }
</script>

<template>
  <section class="timeline-view">
    <header class="timeline-heading">
      <p class="timeline-kicker">OPERATIONS TIMELINE</p>
      <h1>{{ timelineText('title') }}</h1>
      <p>{{ timelineText('description') }}</p>
    </header>
    <div v-if="!applicationId && !serviceId" class="timeline-controls">
      <label v-if="projectId && environmentId"><input v-model="allScopes" type="checkbox"> {{ timelineText('allScopes') }}</label>
      <label>{{ timelineText('runtime') }}
        <select v-model="runtimeId"><option value="">{{ timelineText('all') }}</option><option v-for="runtime in availableRuntimes" :key="runtime.id" :value="runtime.id">{{ runtime.name }}</option></select>
      </label>
      <label>{{ timelineText('service') }}
        <select v-model="selectedServiceId"><option value="">{{ timelineText('all') }}</option><option v-for="service in availableServices" :key="service.id" :value="service.id">{{ service.name }}</option></select>
      </label>
      <label>{{ timelineText('categoryLabel') }}
        <select v-model="category"><option value="">{{ timelineText('all') }}</option><option v-for="kind in ['planned', 'fault', 'health', 'lifecycle', 'restart', 'completed', 'gap']" :key="kind" :value="kind">{{ timelineText(`category.${kind}`) }}</option></select>
      </label>
    </div>
    <p v-if="query.isError.value" role="alert" class="timeline-alert">{{ timelineText('readFailed') }} {{ errorText(query.error.value) }} <button type="button" @click="query.refetch()">{{ timelineText('retry') }}</button></p>
    <template v-else>
      <p v-if="query.isPending.value" role="status">{{ timelineText('loading') }}</p>
      <template v-else>
        <div v-if="coverage?.state !== 'collecting'" role="status" class="timeline-alert" :class="{ 'is-unavailable': coverage?.state !== 'partial' }">
          <StatusBadge :status="coverage?.state === 'partial' ? 'PARTIAL' : 'UNAVAILABLE'" :label="coverage?.state === 'partial' ? timelineText('partial') : timelineText('unavailable')" />
          <ul v-if="incomplete.length"><li v-for="item in incomplete" :key="item.id">{{ item.node_id || runtimeName(item.runtime_id) }} · {{ timelineText(`gap.${item.last_error || 'unknown'}`) }}</li></ul>
        </div>
        <p class="timeline-retention">{{ timelineText('retention') }} {{ occurred(coverage?.retained_from) }} · {{ timelineText('noBackfill') }}</p>
        <div class="timeline-scroll">
          <table>
            <thead><tr><th>{{ timelineText('time') }} (UTC+8)</th><th>{{ timelineText('event') }}</th><th>{{ timelineText('scope') }}</th><th>{{ timelineText('target') }}</th><th>{{ timelineText('result') }}</th><th>{{ timelineText('evidence') }}</th></tr></thead>
            <tbody>
              <tr v-for="record in records" :key="record.id">
                <td>{{ occurred(record.occurred_at) }}</td>
                <td>{{ source(record.source) }} · {{ action(record.action) }}</td>
                <td>{{ scopeName(record) }}</td>
                <td>{{ record.service_id ? serviceName(record.service_id) : runtimeName(record.runtime_id) }}<small v-if="record.node_id">{{ record.node_id }}</small></td>
                <td class="timeline-result"><StatusBadge :status="record.status" :label="recordResult(record)" :tone="recordTone(record)" /></td>
                <td><a v-if="record.operation_id" :href="`/operations/${encodeURIComponent(record.operation_id)}`">{{ timelineText('operationDetails') }}</a><span v-else>{{ evidence(record) }}</span></td>
              </tr>
              <tr v-if="!records.length"><td colspan="6">{{ coverage?.state === 'collecting' ? timelineText('empty') : timelineText('unavailableEmpty') }}</td></tr>
            </tbody>
          </table>
        </div>
        <nav class="timeline-pages" :aria-label="timelineText('pages')"><button v-if="previous.length" type="button" @click="previousPage">{{ timelineText('previous') }}</button><button v-if="query.data.value?.pagination?.next_cursor" type="button" @click="nextPage">{{ timelineText('next') }}</button></nav>
      </template>
    </template>
  </section>
</template>

<style scoped>
.timeline-view { padding: 1.5rem; color: var(--ink, #243047); }
.timeline-heading h1 { margin: .25rem 0; font-size: 2rem; }
.timeline-heading p { margin: .4rem 0 1.25rem; color: #71819a; }
.timeline-kicker { color: #159366 !important; font-size: .75rem; font-weight: 700; letter-spacing: .14em; }
.timeline-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 1rem; margin-bottom: 1rem; }
.timeline-controls label { display: inline-flex; align-items: center; gap: .5rem; }
.timeline-controls select { min-width: 8rem; padding: .4rem 2.125rem .4rem .65rem; border-color: var(--form-border); background-color: var(--form-surface); }
.timeline-alert { padding: .75rem 1rem; background: #eef6ff; border-radius: .5rem; }
.timeline-alert.is-unavailable { background: var(--red-soft); }
.timeline-alert ul { margin: .5rem 0 0; }
.timeline-retention { color: #71819a; font-size: .9rem; }
.timeline-scroll { overflow-x: auto; border: 1px solid #d9e2ee; border-radius: .6rem; background: white; }
table { width: 100%; border-collapse: collapse; text-align: left; }
th, td { padding: .8rem 1rem; border-bottom: 1px solid #e5ebf3; vertical-align: top; }
th { color: #71819a; white-space: nowrap; }
td small { display: block; color: #71819a; }
.timeline-result .badge { font-size: .78rem; font-weight: 650; }
.timeline-pages { display: flex; gap: .5rem; margin-top: 1rem; }
button { cursor: pointer; }
</style>
