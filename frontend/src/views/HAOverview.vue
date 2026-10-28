<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, session, errorText } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'
import { formatDate } from '../shared/i18n'

const admin = computed(() => session.value.authenticated && session.value.role === 'administrator')
const query = useQuery({
  queryKey: computed(() => ['system-ha', session.value.user_id, session.value.role]),
  enabled: admin, retry: false, staleTime: 0, refetchInterval: 15000,
  queryFn: async ({ signal }) => (await request('get', '/system/ha', { signal, cache: 'no-store' })).data as components['schemas']['HAOverview'],
})
const visible = computed(() => admin.value && !query.isError.value ? query.data.value : undefined)
</script>

<template>
  <main class="ha-overview">
    <div class="ha-heading"><h1>{{ t('ha.title') }}</h1><button class="btn" :disabled="!admin || query.isFetching.value" @click="query.refetch()">{{ t('notifications.refresh') }}</button></div>
    <p class="callout amber">{{ t('ha.limit') }}</p>
    <p v-if="!admin" role="alert">{{ t('users.adminOnly') }}</p>
    <p v-else-if="query.isError.value" role="alert">{{ errorText(query.error.value) }}</p>
    <template v-else-if="visible">
      <p class="muted">{{ t('ha.generated') }}: {{ formatDate(visible.generated_at) }}</p>
      <div class="ha-layers">
        <section v-for="layer in visible.layers" :key="layer.name" class="card ha-layer" :data-layer="layer.name" :data-state="layer.state">
          <div class="ha-heading"><h2>{{ t(`ha.layer.${layer.name}`) }}</h2><strong>{{ t(`ha.state.${layer.state}`) }}</strong></div>
          <p v-if="!layer.evidence.length">{{ t('ha.empty') }}</p>
          <dl v-for="item in layer.evidence" :key="item.id" class="ha-evidence" :data-evidence="item.id">
            <dt>{{ item.id || t('ha.local') }}</dt><dd>{{ t(`ha.state.${item.state}`) }} · {{ t(`ha.reason.${item.reason}`) }}</dd>
            <dt>{{ t('ha.sourceLabel') }}</dt><dd>{{ t(`ha.source.${item.source}`) }}</dd>
            <template v-if="item.instance_id || item.node_id"><dt>{{ t('readiness.execution.actual') }}</dt><dd>{{ item.instance_id }} {{ item.node_id }}</dd></template>
            <dt>{{ t('ha.observed') }}</dt><dd>{{ item.observed_at ? formatDate(item.observed_at) : t('ha.state.UNKNOWN') }}</dd>
          </dl>
        </section>
      </div>
    </template>
  </main>
</template>

<style scoped>
.ha-overview { padding: 24px; min-width: 0; }.ha-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; }.ha-layers { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }.ha-layer { padding: 20px; min-width: 0; }.ha-evidence { display: grid; grid-template-columns: minmax(80px, 1fr) minmax(0, 2fr); gap: 8px 12px; border-top: 1px solid var(--border); padding-top: 12px; }.ha-evidence dt, .ha-evidence dd { overflow-wrap: anywhere; margin: 0; }.ha-evidence dt { color: var(--muted); }
@media (max-width: 900px) { .ha-layers { grid-template-columns: minmax(0, 1fr); }.ha-overview { padding: 16px; }.ha-evidence { grid-template-columns: minmax(0, 1fr); } }
</style>
