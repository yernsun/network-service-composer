<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, errorText, session, ApiRequestError, asRow } from '../shared/api/client'
import type { paths } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'
import StatusBadge from './StatusBadge.vue'

type Receipt = NonNullable<paths['/operations/{id}/entrypoint']['get']['responses'][200]['content']['application/json']['data']>
const props = defineProps<{ operationId: string }>()
const busyId = ref(''), failure = ref('')
watch(() => props.operationId, () => { failure.value = '' })
const query = useQuery({
  queryKey: computed(() => ['operation-entrypoint', props.operationId]),
  queryFn: async ({ signal, queryKey }): Promise<Receipt | null> => {
    try {
      const { data } = await request('get', '/operations/{id}/entrypoint', { params: { path: { id: String(queryKey[1]) } }, signal, cache: 'no-store' })
	  const value = asRow(data)
	  if (typeof value.status !== 'string' || !value.status || typeof value.revision !== 'number' || !Number.isSafeInteger(value.revision) || value.revision < 0 || typeof value.attempts !== 'number' || !Number.isSafeInteger(value.attempts) || value.attempts < 0) throw new ApiRequestError(502, 'internal.operation_failed')
      return value as Receipt
    } catch (error) {
      if (error instanceof ApiRequestError && error.status === 404) return null
      throw error
    }
  },
  refetchInterval: 5000, refetchOnMount: 'always', staleTime: 0, retry: false,
})
const inaccessible = computed(() => query.error.value instanceof ApiRequestError && [401, 403].includes(query.error.value.status))
const receipt = computed(() => inaccessible.value ? undefined : query.data.value)
const busy = computed(() => busyId.value === props.operationId)
const state = computed(() => query.isError.value ? 'UNKNOWN' : receipt.value?.status || 'PENDING')
const canRetry = computed(() => receipt.value?.status === 'FAILED' && !query.isError.value && !query.isFetching.value && !busy.value && session.value.role === 'administrator')
const checkedAt = computed(() => query.dataUpdatedAt.value ? formatDate(new Date(query.dataUpdatedAt.value).toISOString()) : '')

async function refresh() { await query.refetch() }
async function retry() {
  if (!canRetry.value || !receipt.value) return
  const operationId = props.operationId, revision = receipt.value.revision
  busyId.value = operationId; failure.value = ''
  try {
    await request('post', '/operations/{id}/entrypoint/retry', { params: { path: { id: operationId } }, headers: { 'Idempotency-Key': crypto.randomUUID(), 'If-Match': `"rev-${revision}"` }, body: {} })
  } catch (error) {
    if (props.operationId === operationId) failure.value = errorText(error)
  } finally {
    // Rejected stale ETags also need a fresh receipt. The shared write path
    // invalidates related inventory and component queries while retaining drafts.
    if (props.operationId === operationId) await refresh()
    if (busyId.value === operationId) busyId.value = ''
  }
}
</script>

<template>
  <section v-if="receipt || query.isPending.value || query.isError.value" class="card operation-entrypoint" data-testid="operation-entrypoint" :aria-busy="query.isFetching.value" :data-state="state">
    <div class="entrypoint-heading">
      <h2>{{ t('bindingUI.title') }}</h2>
      <button class="btn" data-testid="entrypoint-refresh" :disabled="query.isFetching.value || busy" @click="refresh">{{ t('readiness.refresh') }}</button>
    </div>
    <p v-if="query.isError.value" class="callout amber" role="alert" data-testid="entrypoint-observation-error">{{ t('entrypointUI.refreshFailed') }} {{ errorText(query.error.value) }}</p>
    <p v-else-if="query.isFetching.value" class="muted" role="status">{{ t('readiness.refreshing') }}</p>
    <p v-if="!inaccessible"><StatusBadge :status="state" context="operation" /></p>
    <p v-if="query.isError.value && receipt" class="muted" data-testid="entrypoint-last-result">{{ t('entrypointUI.lastResult') }} <StatusBadge :status="receipt.status" context="operation" /></p>
    <p v-if="checkedAt && receipt" class="muted" data-testid="entrypoint-checked-at">{{ t('entrypointUI.checkedAt', { time: checkedAt }) }}</p>
    <p v-if="!query.isError.value && receipt?.error_code">{{ receipt.error_code }}</p>
    <p v-if="failure" role="alert">{{ failure }}</p>
    <button v-if="receipt?.status === 'FAILED'" class="btn" data-testid="entrypoint-retry" :disabled="!canRetry" @click="retry">{{ t('bindingUI.retry') }}</button>
  </section>
</template>

<style scoped>
.operation-entrypoint { margin-top: 20px; min-width: 0; overflow-wrap: anywhere; }
.entrypoint-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
</style>
