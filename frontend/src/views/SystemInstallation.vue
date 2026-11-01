<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useRoute, useRouter } from 'vue-router'
import { request, session, errorText } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'
import { resourcesChanged } from '../data/resource-changes'

type Plan = components['schemas']['InstallationPlan']
type Job = components['schemas']['InstallationJob']
type Installation = components['schemas']['SystemInstallation']
type Action = components['schemas']['InstallationPlanInput']['action']
const route = useRoute(), router = useRouter(), cache = useQueryClient()
const admin = computed(() => session.value.authenticated && session.value.role === 'administrator')
const query = useQuery({ queryKey: computed(() => ['system-installation', session.value.user_id, session.value.role]), enabled: admin, staleTime: 0, retry: false, refetchInterval: 10000,
  queryFn: async ({ signal }) => (await request('get', '/system/installation', { signal, cache: 'no-store' })).data as Installation })
const installation = computed(() => admin.value && !query.isError.value ? query.data.value : undefined)
const jobID = computed(() => admin.value ? String(route.query.job || installation.value?.operation_id || '') : '')
const operation = useQuery<Job>({ queryKey: computed(() => ['system-installation-operation', session.value.user_id, jobID.value]), enabled: computed(() => admin.value && Boolean(jobID.value)), staleTime: 0, retry: false,
  refetchInterval: current => ['PENDING', 'RUNNING', 'UNKNOWN'].includes(current.state.data?.state || '') ? 2000 : false,
  queryFn: async ({ signal }) => (await request('get', '/system/installation/operations/{operationID}', { signal, cache: 'no-store', params: { path: { operationID: jobID.value } } })).data as Job })
const job = computed(() => admin.value && !operation.isError.value && operation.data.value?.id === jobID.value ? operation.data.value : undefined)
const action = ref<Action>('verify'), manifest = ref(''), approval = ref(''), plan = ref<Plan>(), busy = ref(false), failure = ref<unknown>(), invalid = ref(false), key = ref('')
const selectedComponents = ref(['controller', 'nginx', 'nginx-sandbox'])
const available = computed(() => installation.value?.allowed_operations || [])
const approved = computed(() => Boolean(plan.value && approval.value === plan.value.hash && Date.parse(plan.value.expires_at) > Date.now() && plan.value.revision === installation.value?.revision))
watch([action, manifest, selectedComponents], () => { plan.value = undefined; approval.value = ''; invalid.value = false }, { deep: true })
watch(admin, () => { plan.value = undefined; approval.value = ''; manifest.value = ''; failure.value = undefined })
watch(job, (next, previous) => {
  if (!next || !['SUCCEEDED', 'FAILED', 'BLOCKED'].includes(next.state) || (previous?.id === next.id && previous?.state === next.state)) return
  resourcesChanged({ reason: 'operation', path: '/system/installation', operationID: next.id })
  void cache.invalidateQueries({ queryKey: ['system-installation'] })
  void cache.invalidateQueries({ queryKey: ['system-ha'] })
})
function refresh() { void query.refetch(); if (jobID.value) void operation.refetch() }
window.addEventListener('online', refresh)
onBeforeUnmount(() => window.removeEventListener('online', refresh))
async function review() {
  if (!installation.value || busy.value) return
  busy.value = true; failure.value = undefined; invalid.value = false; plan.value = undefined; approval.value = ''
  try {
    let release: unknown
    if (action.value === 'upgrade') {
      try { release = JSON.parse(manifest.value) } catch { invalid.value = true; return }
      if (!release || typeof release !== 'object' || Array.isArray(release)) { invalid.value = true; return }
    }
    plan.value = (await request('post', '/system/installation/plans', { body: { action: action.value, revision: installation.value.revision, ...(action.value === 'repair' ? { components: selectedComponents.value } : {}), ...(release ? { release } : {}) } })).data as Plan
    key.value = crypto.randomUUID()
  } catch (error) { failure.value = error; void query.refetch() } finally { busy.value = false }
}
async function execute(resume = false) {
  const target = resume ? job.value?.plan : plan.value
  if (!target || busy.value || (!resume && !approved.value)) return
  busy.value = true; failure.value = undefined
  try {
    const result = (await request('post', '/system/installation/operations', { headers: { 'Idempotency-Key': resume ? crypto.randomUUID() : key.value }, body: { plan_hash: target.hash, revision: target.revision, ...(resume ? { operation_id: job.value?.id } : {}) } })).data as Job
    await router.replace({ query: { ...route.query, job: result.id } })
    plan.value = undefined; approval.value = ''; await Promise.all([query.refetch(), operation.refetch()])
  } catch (error) { failure.value = error; void query.refetch() } finally { busy.value = false }
}
</script>

<template>
  <main class="system-installation">
    <div class="installation-heading"><h1>{{ t('installation.title') }}</h1><button class="btn" :disabled="!admin || query.isFetching.value" @click="refresh">{{ t('notifications.refresh') }}</button></div>
    <p class="callout amber">{{ t('installation.limit') }}</p>
    <p v-if="!admin" role="alert">{{ t('users.adminOnly') }}</p>
    <p v-else-if="query.isError.value" role="alert">{{ errorText(query.error.value) }}</p>
    <template v-else-if="installation">
      <p v-if="!installation.id" class="callout">{{ t('installation.unmanaged') }}</p>
      <template v-else>
        <section class="card installation-card">
          <dl class="installation-facts"><dt>ID</dt><dd>{{ installation.id }}</dd><dt>{{ t('installation.mode') }}</dt><dd>{{ installation.mode }} · {{ installation.topology.managers }} / {{ installation.topology.majority }}</dd><dt>{{ t('installation.health') }}</dt><dd>{{ installation.health === 'VERIFIED' ? t('installation.state.SUCCEEDED') : t('installation.state.UNKNOWN') }}</dd><dt>{{ t('installation.addresses') }}</dt><dd><a v-for="address in installation.console_urls" :key="address" :href="address" rel="noopener noreferrer">{{ address }}</a></dd><dt>{{ t('installation.certificate') }}</dt><dd>{{ installation.certificate_id }} · {{ installation.console_certificate_expires_at ? formatDate(installation.console_certificate_expires_at) : t('installation.state.UNKNOWN') }}<br><code>{{ installation.console_fingerprint }}</code></dd><dt>{{ t('installation.database') }}</dt><dd><code>{{ installation.database.node_id }}</code><br><code>{{ installation.database.volume }}</code></dd></dl>
        </section>
        <section class="card installation-card"><h2>{{ t('installation.components') }}</h2><dl class="installation-facts"><template v-for="(component, name) in installation.components" :key="name"><dt>{{ name }}</dt><dd><code>{{ component.id }}</code><br><code>{{ component.image }}</code></dd></template></dl></section>
        <section v-if="installation.drift.length" class="callout amber"><h2>{{ t('installation.drift') }}</h2><p>{{ t('installation.cli') }}</p><ul><li v-for="item in installation.drift" :key="item"><code>{{ item }}</code></li></ul></section>
        <form v-if="available.length" class="card installation-card" @submit.prevent="review">
          <h2>{{ t('installation.actions') }}</h2><label>{{ t('installation.actions') }}<select v-model="action"><option v-for="value in available" :key="value" :value="value">{{ t(`installation.action.${value}`) }}</option></select></label>
          <fieldset v-if="action === 'repair'"><legend>{{ t('installation.components') }}</legend><label v-for="name in ['controller', 'nginx', 'nginx-sandbox', 'postgresql']" :key="name" class="component-choice"><input v-model="selectedComponents" type="checkbox" :value="name">{{ name }}</label></fieldset>
          <label v-if="action === 'upgrade'">{{ t('installation.release') }}<textarea v-model="manifest" rows="8" spellcheck="false" autocomplete="off" /></label>
          <p v-if="invalid" role="alert">{{ t('installation.invalid') }}</p><button class="btn" type="submit" :disabled="busy || !available.includes(action)">{{ t('installation.review') }}</button>
        </form>
        <section v-if="plan" class="card installation-card" data-plan>
          <h2>{{ t('installation.impact') }}</h2><p class="callout amber">{{ t('installation.boundary') }} {{ t('installation.limit') }}</p><p v-if="plan.scope.mode === 'single'" class="callout amber">{{ t('installation.downtime') }}</p><p>{{ t('installation.mode') }}: {{ plan.scope.mode }} · {{ t(`installation.action.${plan.action}`) }}</p><dl class="installation-facts"><dt>{{ t('installation.hash') }}</dt><dd><code>{{ plan.hash }}</code></dd><dt>{{ t('installation.expires') }}</dt><dd>{{ formatDate(plan.expires_at) }}</dd></dl>
          <template v-if="plan.scope.release"><h3>{{ t('installation.release') }}</h3><ul><li v-for="(image, component) in plan.scope.release.images" :key="component">{{ component }}<br><code>{{ image }}</code></li></ul></template><h3>{{ t('installation.targets') }}</h3><ul><li v-for="target in plan.scope.targets" :key="target.node_id || target.name">{{ target.name }} · {{ target.address }}<br><code>{{ target.node_id }}</code></li></ul><ol><li v-for="step in plan.steps" :key="step.key"><code>{{ step.key }}</code><template v-if="step.before"><br><code>{{ step.before.id }}</code><br><code>{{ step.before.image }}</code></template></li></ol><label>{{ t('installation.approve') }}<input v-model="approval" autocomplete="off" spellcheck="false"></label><button class="btn primary" :disabled="busy || !approved" @click="execute()">{{ t('installation.execute') }}</button>
        </section>
      </template>
    </template>
    <p v-if="failure" role="alert">{{ errorText(failure) }}</p><p v-if="admin && operation.isError.value" role="alert">{{ errorText(operation.error.value) }}</p>
    <section v-if="job" class="card installation-card" data-operation :data-state="job.state">
      <h2>{{ t('installation.operation') }} · {{ t(`installation.state.${job.state}`) }}</h2><code>{{ job.id }}</code><p>{{ t('installation.progress') }} · {{ formatDate(job.updated_at) }}</p><p v-if="job.state === 'UNKNOWN'" class="callout amber">{{ t('installation.unknown') }}</p><code v-if="job.reason">{{ job.reason }}</code><p v-if="job.state === 'BLOCKED' || job.state === 'FAILED'" class="callout">{{ t('installation.cli') }}</p>
      <ol><li v-for="step in (job.operation?.plan || job.plan).steps" :key="step.key"><code>{{ step.key }}</code> · {{ t(`installation.state.${step.state}`) }}</li></ol><p v-if="job.state === 'UNKNOWN' && job.execution_mode === 'cli'" class="callout">{{ t('installation.cli') }}</p><button v-if="job.state === 'UNKNOWN' && job.execution_mode !== 'cli'" class="btn" :disabled="busy" @click="execute(true)">{{ t('installation.resume') }}</button>
    </section>
  </main>
</template>

<style scoped>
.system-installation { padding: 24px; min-width: 0; max-width: 1300px; margin: auto; }.installation-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; }.installation-card { padding: 20px; margin-block: 20px; min-width: 0; }.installation-facts { display: grid; grid-template-columns: minmax(100px, 1fr) minmax(0, 3fr); gap: 12px; }.installation-facts dt { color: var(--muted); }.installation-facts dd { margin: 0; }.installation-facts a { display: block; }.system-installation code, .system-installation dd { overflow-wrap: anywhere; white-space: normal; }.system-installation label { display: flex; flex-direction: column; gap: 8px; margin-block: 16px; }.system-installation textarea, .system-installation input:not([type=checkbox]), .system-installation select { width: 100%; min-width: 0; box-sizing: border-box; }.system-installation .component-choice { display: inline-flex; flex-direction: row; margin-right: 16px; }.system-installation ol { padding-left: 24px; }.system-installation li { margin-block: 8px; }
@media(max-width: 800px) { .system-installation { padding: 16px; }.installation-facts { grid-template-columns: minmax(0, 1fr); } }
</style>
