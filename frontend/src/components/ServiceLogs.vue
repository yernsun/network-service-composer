<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { session } from '../shared/api/client'

const props = defineProps<{ serviceId: string; runtimeType: string }>()
const { t } = useI18n()
const rows = ref<{ stream: string; text: string }[]>([])
const state = ref<'idle' | 'connecting' | 'live' | 'stopped' | 'error' | 'unavailable'>('idle')
let source: EventSource | null = null

function stop() {
  source?.close()
  source = null
  state.value = 'stopped'
}
function start() {
  if (props.runtimeType !== 'swarm' || session.value.role !== 'administrator') return
  stop()
  state.value = 'connecting'
  source = new EventSource(`/api/v1/services/${encodeURIComponent(props.serviceId)}/logs?tail=200`)
  source.onopen = () => { state.value = 'live' }
  source.addEventListener('log', event => {
    try {
      const value = JSON.parse((event as MessageEvent<string>).data) as { stream: string; text: string }
      if (value.stream !== 'stdout' && value.stream !== 'stderr') return
      rows.value.push(value)
      if (rows.value.length > 2000) rows.value.splice(0, rows.value.length - 2000)
    } catch { state.value = 'error'; source?.close(); source = null }
  })
  source.addEventListener('error', () => { state.value = 'error'; source?.close(); source = null })
  source.addEventListener('unavailable', () => { state.value = 'unavailable'; source?.close(); source = null })
  source.onerror = () => { state.value = 'error'; source?.close(); source = null }
}
onMounted(start)
onBeforeUnmount(stop)
</script>

<template>
  <section class="card p-5" aria-live="polite">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h2 class="font-semibold">{{ t('serviceLogs.title') }}</h2>
        <p class="text-sm opacity-70">{{ t(`serviceLogs.${runtimeType !== 'swarm' ? 'swarmOnly' : session.role !== 'administrator' ? 'adminOnly' : state}`) }}</p>
      </div>
      <button v-if="runtimeType === 'swarm' && session.role === 'administrator'" type="button" class="btn" @click="state === 'live' || state === 'connecting' ? stop() : start()">
        {{ t(state === 'live' || state === 'connecting' ? 'serviceLogs.pause' : 'serviceLogs.resume') }}
      </button>
    </div>
    <pre class="mt-4 max-h-[36rem] overflow-auto rounded bg-slate-950 p-4 text-xs text-slate-100"><span v-for="(row, index) in rows" :key="index" :class="row.stream === 'stderr' ? 'text-amber-300' : ''">{{ row.text }}
</span></pre>
  </section>
</template>
