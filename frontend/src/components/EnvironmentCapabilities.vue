<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, session, errorText } from '../shared/api/client'
import type { components } from '../shared/api/schema'
import { translate as t } from '../i18n'

const props = defineProps<{ environmentId: string }>()
defineEmits<{ configure: [page: string] }>()
const capabilities = useQuery({
  queryKey: computed(() => ['environment-capabilities', props.environmentId, session.value.user_id, session.value.role]),
  enabled: computed(() => session.value.authenticated), retry: false, staleTime: 0, refetchOnMount: 'always', refetchInterval: 30000,
  queryFn: async ({ signal, queryKey }) => (await request('get', '/environments/{id}/capabilities', {
    params: { path: { id: queryKey[1] as string } }, signal, cache: 'no-store',
  })).data as components['schemas']['EnvironmentCapabilities'],
})
const value = computed(() => !session.value.authenticated || capabilities.isError.value ? undefined : capabilities.data.value)
const changed = () => { if (session.value.authenticated) void capabilities.refetch() }
onMounted(() => window.addEventListener('nsc:resources-changed', changed))
onUnmounted(() => window.removeEventListener('nsc:resources-changed', changed))
function reason(code: string | undefined) {
  const key = code?.startsWith('management.') ? code.split('.').join('_') : code
  return t(`capabilities.reason.${key || 'baseline_required'}`)
}
</script>

<template>
  <section class="card capability-summary" data-testid="environment-capabilities">
    <div class="capability-heading"><h2>{{ t('capabilities.title') }}</h2><button class="btn" type="button" :disabled="!session.authenticated || capabilities.isFetching.value" @click="capabilities.refetch()">{{ t('notifications.refresh') }}</button></div>
    <p>{{ t('capabilities.boundary') }}</p>
    <p v-if="capabilities.isError.value" class="callout amber" role="alert">{{ t('capabilities.unknown') }} {{ errorText(capabilities.error.value) }}</p>
    <p v-else-if="!value" role="status">{{ t('capabilities.loading') }}</p>
    <dl v-else>
      <div><dt>{{ t('capabilities.management') }}</dt><dd>{{ value.management.state === 'CONFIGURED' ? t('capabilities.configured') : reason(value.management.reason) }}<button class="btn sm" type="button" @click="$emit('configure', 'runtimes')">{{ t('capabilities.runtimeLink') }}</button></dd></div>
      <div><dt>{{ t('capabilities.imageBaseline') }}</dt><dd>{{ value.image_updates.state === 'BASELINE_READY' ? t('capabilities.baselineReady') : reason(value.image_updates.reason) }}<button class="btn sm" type="button" @click="$emit('configure', 'applications')">{{ t('capabilities.applicationLink') }}</button></dd></div>
      <div v-for="name in (['certificates', 'acme', 'notifications', 'ha'] as const)" :key="name"><dt>{{ t(`capabilities.${name}`) }}</dt><dd v-if="value[name]"><span>{{ t(`capabilities.state.${value[name].state}`) }} · {{ reason(value[name].reason) }}</span><small v-if="value[name].observed_at">{{ t('ha.observed') }}: {{ value[name].observed_at }}</small><small v-if="value[name].last_succeeded_at">{{ t('activityHub.historical') }}: {{ value[name].last_succeeded_at }}</small></dd></div>
    </dl>
  </section>
</template>

<style scoped>
.capability-summary { padding: 22px; margin: 20px 0; min-width: 0; }
.capability-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; }
dl { display: grid; gap: 14px; }
dl > div { display: grid; grid-template-columns: minmax(100px, 1fr) minmax(0, 3fr); gap: 12px; }
dd { margin: 0; display: flex; align-items: center; gap: 12px; flex-wrap: wrap; overflow-wrap: anywhere; }
@media (max-width: 600px) { dl > div { grid-template-columns: 1fr; } }
</style>
