<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import type { components } from '../shared/api/schema'
import { request, session } from '../shared/api/client'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'

type Readiness = components['schemas']['OperationReadiness']
type Stage = Readiness['stages'][number]
const props = defineProps<{ operationId: string }>()
const names: Stage['name'][] = ['accepted', 'workload', 'certificate', 'gateway', 'client_access']
const query = useQuery({
  queryKey: computed(() => ['operation-readiness', props.operationId, session.value.user_id, session.value.role]),
  enabled: computed(() => session.value.authenticated),
  queryFn: async ({ signal }) => (await request('get', '/operations/{id}/readiness', { params: { path: { id: props.operationId } }, signal })).data as Readiness,
  refetchInterval: 5000,
  refetchOnMount: 'always',
  staleTime: 0,
  retry: false,
})
const stages = computed<Stage[]>(() => names.map(name => {
  const stage = query.data.value?.stages.find(value => value.name === name)
  if (!session.value.authenticated || query.isError.value || !stage) return { name, state: 'UNKNOWN' }
  return stage
}))
const execution = computed(() => !session.value.authenticated || query.isError.value ? undefined : query.data.value?.execution)
const executionReason = computed(() => {
  const code = execution.value?.reason
  if (!code) return ''
  if (code === 'management_disabled') return t('capabilities.reason.management_disabled')
  if (code === 'frozen_target_requires_review') return t('readiness.execution.reviewTarget')
  if (code === 'accepted_effects_require_review') return t('readiness.execution.reviewEffects')
  if (code === 'execution_evidence_unavailable') return t('readiness.execution.unavailable')
  if (execution.value?.state === 'WAITING') return t('readiness.execution.waiting')
  return t('readiness.execution.uncertain')
})
const targets = computed(() => session.value.authenticated ? query.data.value?.gateway_targets || [] : [])
const reasonKeys: Record<string, string> = {
  client_not_verified: 'client',
  not_workload_operation: 'notApplicable',
  access_not_requested: 'notApplicable',
  operation_failed: 'failed',
  operation_stalled: 'unknown',
  workload_unavailable: 'unknown',
  workload_superseded: 'superseded',
  workload_stale: 'stale',
  workload_unhealthy: 'failed',
  workload_converging: 'pending',
  binding_unavailable: 'unknown',
  binding_failed: 'failed',
  binding_pending: 'pending',
  certificate_unavailable: 'unknown',
  certificate_pending: 'pending',
  certificate_failed: 'failed',
  gateway_unavailable: 'unknown',
  gateway_failed: 'failed',
  gateway_pending: 'pending',
}
const reason = (code: string) => code === 'recovery_held'
  ? t('problem.recovery.read_only')
  : t(`readiness.reason.${reasonKeys[code] || 'unknown'}`)
</script>

<template>
  <section class="card operation-readiness" data-testid="operation-readiness" :aria-busy="query.isFetching.value">
    <div class="readiness-heading">
      <h2>{{ t('readiness.title') }}</h2>
      <button class="btn" :disabled="!session.authenticated || query.isFetching.value" @click="query.refetch()">{{ t('readiness.refresh') }}</button>
    </div>
    <p class="muted">{{ t('readiness.explanation') }}</p>
    <p v-if="query.isError.value" class="callout amber" role="alert" data-testid="readiness-observation-error">{{ t('readiness.refreshFailed') }}</p>
    <p v-else-if="query.isFetching.value" class="muted" role="status">{{ t('readiness.refreshing') }}</p>
    <div v-if="execution" class="execution-evidence" data-testid="operation-execution">
      <h3>{{ t('readiness.execution.title') }}</h3>
      <dl class="execution-grid">
        <div><dt>{{ t('readiness.execution.configured') }}</dt><dd>{{ execution.configured_node_id || execution.runtime_id }}</dd></div>
        <div><dt>{{ t('readiness.execution.actual') }}</dt><dd>{{ execution.actual_instance_id || t('readiness.execution.unselected') }}<small v-if="execution.actual_node_id">{{ execution.actual_node_id }}</small></dd></div>
        <div><dt>{{ t('readiness.execution.attempt', { attempt: execution.attempt }) }}</dt><dd>{{ t(`readiness.execution.${execution.state === 'RECONCILED' ? 'QUEUED' : execution.state}`) }}</dd></div>
      </dl>
      <p v-if="executionReason" class="callout amber" role="status">{{ executionReason }}</p>
      <ol v-if="execution.state === 'UNKNOWN'" class="recovery-guidance"><li>{{ t('workflow.preserve') }}</li><li>{{ t('workflow.inspect') }}</li><li>{{ t('workflow.review') }}</li></ol>
      <p v-if="execution.state === 'WAITING'" class="muted">{{ t('workflow.wait') }}</p>
      <small v-if="execution.observed_at">{{ t('readiness.execution.observedAt', { time: formatDate(execution.observed_at) }) }}</small>
    </div>
    <ol class="readiness-stages">
      <li v-for="stage in stages" :key="stage.name" :data-stage="stage.name" :data-state="stage.state">
        <h3>{{ t(`readiness.stage.${stage.name}`) }}</h3>
        <span class="readiness-state" :class="stage.state.toLowerCase()">{{ t(`readiness.state.${stage.state}`) }}</span>
        <p v-if="stage.error_code">{{ reason(stage.error_code) }}</p>
        <small v-if="stage.observed_at">{{ t('readiness.observedAt', { time: formatDate(stage.observed_at) }) }}</small>
      </li>
    </ol>
    <div v-if="targets.length" class="readiness-targets">
      <h3>{{ t('readiness.targets') }}</h3>
      <ul>
        <li v-for="target in targets" :key="target.gateway_id" :data-gateway="target.gateway_id" :data-state="query.isError.value ? 'UNKNOWN' : target.state">
          <code>{{ target.gateway_id }}</code>
          <span class="readiness-state" :class="query.isError.value ? 'unknown' : target.state.toLowerCase()">{{ t(`readiness.state.${query.isError.value ? 'UNKNOWN' : target.state}`) }}</span>
          <span v-if="!query.isError.value">{{ t('readiness.revisions', { applied: target.applied_revision, desired: target.desired_revision }) }}</span>
        </li>
      </ul>
    </div>
    <p v-if="stages.some(stage => stage.name === 'client_access' && stage.state === 'UNKNOWN')" class="callout">{{ t('readiness.clientHint') }}</p>
  </section>
</template>

<style scoped>
.execution-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr)); gap: 12px; }
.execution-grid div { min-width: 0; overflow-wrap: anywhere; }
.execution-grid dt { color: var(--muted); font-size: 12px; }
.execution-grid dd { margin: 6px 0; }
.execution-grid small { display: block; }
.operation-readiness { margin-top: 20px; min-width: 0; }
.readiness-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.readiness-stages { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr)); gap: 12px; padding: 0; list-style: none; }
.readiness-stages li { padding: 12px; border: 1px solid var(--line); border-radius: 8px; min-width: 0; overflow-wrap: anywhere; }
.readiness-stages h3 { font-size: 14px; margin: 0 0 10px; }
.readiness-stages p, .readiness-stages small { color: var(--muted); font-size: 12px; }
.readiness-state { display: inline-block; padding: 3px 8px; border-radius: 6px; background: var(--bg); font-size: 12px; }
.readiness-state.confirmed { color: var(--green); }
.readiness-state.failed { color: var(--red); }
.readiness-state.pending, .readiness-state.unknown, .readiness-state.not_applicable { color: var(--muted); }
.readiness-targets ul { padding: 0; list-style: none; }
.readiness-targets li { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; padding: 8px 0; overflow-wrap: anywhere; }
.readiness-targets code { overflow-wrap: anywhere; font-size: 12px; }
</style>
