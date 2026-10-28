<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { request, session, errorText } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'

type Issue = components['schemas']['Issue']
type Notice = components['schemas']['Notification']
const route = useRoute(), router = useRouter()
const allowed = computed(() => session.value.authenticated)
const admin = computed(() => allowed.value && session.value.role === 'administrator')
const selected = ref(''), cursor = ref(''), busy = ref(false), error = ref('')
const state = ref(String(route.query.state || 'active'))
const filters = computed(() => Object.fromEntries(['resource_type', 'resource_id', 'project_id', 'environment_id', 'state'].map(key => [key, String(route.query[key] || (key === 'state' ? 'active' : ''))])))
watch(() => route.fullPath, () => { state.value = String(route.query.state || 'active'); selected.value = ''; cursor.value = ''; error.value = '' })
watch(allowed, value => { if (!value) { selected.value = ''; error.value = '' } })
const issues = useQuery({
  queryKey: computed(() => ['activity-hub', 'issues', session.value.user_id, session.value.role, filters.value]), enabled: allowed, retry: false, staleTime: 0, refetchInterval: 15000,
  queryFn: async ({ signal }) => {
    const rows: Issue[] = []; let after = ''
    do {
      const response = await request('get', '/issues', { signal, cache: 'no-store', params: { query: { ...filters.value, limit: 200, cursor: after } } })
      rows.push(...response.data as Issue[]); after = response.pagination?.next_cursor || ''
    } while (after)
    return rows
  },
})
const notices = useQuery({
  queryKey: computed(() => ['activity-hub', 'deliveries', session.value.user_id, session.value.role, filters.value.project_id, filters.value.environment_id, filters.value.resource_id, selected.value, cursor.value]), enabled: admin, retry: false, staleTime: 0, refetchInterval: 15000,
  queryFn: async ({ signal }) => request('get', '/notifications', { signal, cache: 'no-store', params: { query: { project_id: filters.value.project_id, environment_id: filters.value.environment_id, resource_id: filters.value.resource_id, issue_id: selected.value, cursor: cursor.value, limit: 50 } } }),
})
const visibleIssues = computed(() => allowed.value && !issues.isError.value ? issues.data.value : undefined)
const visibleNotices = computed(() => admin.value && !notices.isError.value ? notices.data.value?.data as Notice[] | undefined : undefined)
async function mutate(issue: Issue, assign: boolean) {
  if (!admin.value || busy.value || issues.isError.value) return
  busy.value = true; error.value = ''
  try {
    await request(assign ? 'put' : 'post', assign ? '/issues/{issueID}/responsibility' : '/issues/{issueID}/acknowledge', { params: { path: { issueID: issue.id } }, headers: { 'If-Match': `"rev-${issue.revision}"` }, ...(assign ? { body: { user_id: issue.responsible_user_id === session.value.user_id ? '' : session.value.user_id } } : {}) })
  } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false; await issues.refetch() }
}
function select(issue: Issue) { selected.value = selected.value === issue.id ? '' : issue.id; cursor.value = '' }
function refresh() { void issues.refetch(); if (admin.value) void notices.refetch() }
</script>

<template>
  <main class="activity-hub">
    <div class="activity-heading"><h1>{{ t('activityHub.title') }}</h1><button class="btn" :disabled="!allowed || issues.isFetching.value" @click="refresh">{{ t('notifications.refresh') }}</button></div>
    <p>{{ t('activityHub.boundary') }}</p><p v-if="error" class="callout red" role="alert">{{ error }}</p>
    <p v-if="!allowed" role="alert">{{ t('console.expired') }}</p>
    <template v-else>
      <form class="activity-heading" @submit.prevent="router.push({ query: { ...route.query, state } })"><label>{{ t('activityHub.state') }} <select v-model="state"><option value="active">{{ t('activityHub.active') }}</option><option value="resolved">{{ t('activityHub.resolved') }}</option><option value="all">{{ t('activityHub.all') }}</option></select></label><button class="btn">{{ t('activityHub.apply') }}</button></form>
      <p v-if="issues.isError.value" role="alert">{{ errorText(issues.error.value) }}</p>
      <p v-else-if="visibleIssues && !visibleIssues.length">{{ t('activityHub.empty') }}</p>
      <section v-for="issue in visibleIssues" :key="issue.id" class="card activity-issue" :data-issue="issue.id">
        <div class="activity-heading"><h2>{{ t(`community.issue.${issue.code}`) }}</h2><strong>{{ t(issue.active ? 'activityHub.active' : 'activityHub.resolved') }}</strong></div>
        <p>{{ issue.resource_type }} · {{ issue.resource_id }}</p>
        <dl><dt>{{ t('activityHub.observation') }}</dt><dd>{{ issue.observation_state === 'OFFLINE' ? t('activityHub.offline') : issue.observation_state === 'UNMONITORED' ? t('activityHub.unmonitored') : t(`community.evidence_state.${['CONFIRMED', 'RISK', 'MISMATCH', 'EXTERNAL_CHANGE'].includes(issue.observation_state || '') ? issue.observation_state : 'UNKNOWN'}`) }} · {{ formatDate(issue.last_seen_at) }}</dd><dt>{{ t('activityHub.occurrence') }}</dt><dd>{{ issue.occurrences }}</dd><dt>{{ t('activityHub.owner') }}</dt><dd>{{ issue.responsible_user_id || t('activityHub.unassigned') }}</dd><dt>{{ t('activityHub.acknowledged') }}</dt><dd>{{ issue.acknowledged_by || t('activityHub.unassigned') }} {{ issue.acknowledged_at ? formatDate(issue.acknowledged_at) : '' }}</dd><dt>{{ t('activityHub.recovered') }}</dt><dd>{{ issue.resolved_at ? formatDate(issue.resolved_at) : t('activityHub.unconfirmed') }}</dd></dl>
        <ul><li v-for="scope in issue.scopes" :key="scope.environment_id"><RouterLink :to="`/projects/${encodeURIComponent(scope.project_id)}/environments/${encodeURIComponent(scope.environment_id)}/overview`">{{ scope.project_id }} / {{ scope.environment_id }}</RouterLink></li></ul>
        <div class="activity-heading"><RouterLink v-if="issue.operation_id" :to="`/operations/${encodeURIComponent(issue.operation_id)}`">{{ t('activityHub.operation') }}</RouterLink><template v-if="admin"><button class="btn" :disabled="busy || issue.acknowledged || !issue.active" @click="mutate(issue, false)">{{ t('activityHub.acknowledge') }}</button><button class="btn" :disabled="busy" @click="mutate(issue, true)">{{ t(issue.responsible_user_id === session.user_id ? 'activityHub.release' : 'activityHub.take') }}</button><button class="btn" @click="select(issue)">{{ t('activityHub.deliveries') }}</button></template></div>
      </section>
      <section v-if="admin" class="card activity-issue">
        <div class="activity-heading"><h2>{{ t('activityHub.deliveries') }}</h2><button v-if="selected" class="btn" @click="selected = ''; cursor = ''">{{ t('activityHub.all') }}</button></div>
        <p v-if="notices.isError.value" role="alert">{{ errorText(notices.error.value) }}</p>
        <article v-for="notice in visibleNotices" :key="notice.id" class="activity-notice" :data-delivery="notice.id"><strong>{{ notice.event.type }} · {{ t(`routing.state.${notice.status}`) }}</strong><p>{{ notice.event.id }} · {{ notice.receiver_id }} · {{ formatDate(notice.event.occurred_at) }}</p><p v-if="notice.event.issue_id">{{ notice.event.issue_id }} · {{ notice.event.occurrence }}</p><RouterLink v-if="notice.event.operation_id" :to="`/operations/${encodeURIComponent(notice.event.operation_id)}`">{{ t('activityHub.operation') }}</RouterLink></article>
        <button v-if="notices.data.value?.pagination?.next_cursor" class="btn" @click="cursor = notices.data.value.pagination.next_cursor">{{ t('activityHub.next') }}</button>
      </section>
    </template>
  </main>
</template>

<style scoped>
.activity-hub { padding: 24px; min-width: 0; }.activity-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }.activity-issue { padding: 20px; margin-top: 16px; min-width: 0; overflow-wrap: anywhere; }.activity-issue dl { display: grid; grid-template-columns: minmax(90px, 1fr) minmax(0, 3fr); gap: 8px 12px; }.activity-issue dd { margin: 0; }.activity-notice { border-top: 1px solid var(--line); padding: 12px 0; }.activity-heading h2 { font-size: 18px; }
@media (max-width: 600px) { .activity-hub { padding: 12px; }.activity-issue dl { grid-template-columns: minmax(0, 1fr); } }
</style>
