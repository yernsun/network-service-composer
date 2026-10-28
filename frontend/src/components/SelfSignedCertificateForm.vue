<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, session, errorText } from '../shared/api/client'
import { allPages } from '../data/pagination'
import { translate as t } from '../i18n'
import type { components } from '../shared/api/schema'

const props = defineProps<{ poolId?: string; host?: string }>()
const emit = defineEmits<{ created: [certificate: components['schemas']['Certificate']] }>()
const opened = ref(false), busy = ref(false), error = ref(''), names = ref(''), pool = ref(''), years = ref(20)
const admin = computed(() => session.value.role === 'administrator')
const pools = useQuery({ queryKey: computed(() => ['certificate-pools', session.value.user_id]), enabled: computed(() => admin.value && opened.value && !props.poolId), queryFn: ({ signal }) => allPages('/gateway-pools', {}, signal), staleTime: 0 })
watch(() => props.poolId, () => { opened.value = false; error.value = '' })
function open() { names.value = props.host || ''; pool.value = props.poolId || ''; years.value = 20; error.value = ''; opened.value = true }
const valid = computed(() => !!(props.poolId || pool.value) && !!names.value.trim() && Number.isInteger(years.value) && years.value >= 1 && years.value <= 100)
async function create() {
  if (busy.value || !admin.value || !valid.value) return
  busy.value = true; error.value = ''
  try {
    const result = await request('post', '/certificates/self-signed', { body: { pool_id: props.poolId || pool.value, domains: names.value.trim().split(/[\s,;]+/), validity_years: years.value } })
    emit('created', result.data as components['schemas']['Certificate'])
    opened.value = false
    window.dispatchEvent(new Event('nsc:resources-changed'))
  } catch (e) { error.value = errorText(e) } finally { busy.value = false }
}
</script>

<template>
  <section v-if="admin" class="self-signed" data-testid="self-signed-certificate">
    <button v-if="!opened" type="button" class="btn" @click="open">{{ t('selfSigned.create') }}</button>
    <fieldset v-else :disabled="busy">
      <legend>{{ t('selfSigned.create') }}</legend>
      <p class="callout amber">{{ t('selfSigned.trust') }}</p>
      <p v-if="error || pools.isError.value" role="alert">{{ error || errorText(pools.error.value) }}</p>
      <label v-if="!poolId">{{ t('selfSigned.pool') }}<select v-model="pool" data-testid="self-signed-pool"><option disabled value="">{{ t('bindingUI.select') }}</option><option v-for="item in pools.data.value" :key="String(item.id)" :value="String(item.id)">{{ item.name || item.id }}</option></select></label>
      <label>{{ t('selfSigned.domains') }}<textarea v-model="names" rows="2" autocomplete="off" spellcheck="false" data-testid="self-signed-domains"></textarea></label>
      <label>{{ t('selfSigned.years') }}<input v-model.number="years" type="number" min="1" max="100" step="1" data-testid="self-signed-years"></label>
      <p class="hint">{{ t('selfSigned.binding') }}</p>
      <div class="flex"><button type="button" class="btn primary" :disabled="!valid || busy || !!pools.error.value" data-testid="self-signed-submit" @click="create">{{ t('selfSigned.issue') }}</button><button type="button" class="btn" @click="opened = false">{{ t('console.cancel') }}</button></div>
    </fieldset>
  </section>
</template>

<style scoped>
.self-signed { margin: 12px 0; min-width: 0; } fieldset { display: grid; gap: 12px; min-width: 0; padding: 16px; border: 1px solid var(--border); border-radius: 12px; } label { display: grid; gap: 6px; min-width: 0; } input, textarea, select { width: 100%; box-sizing: border-box; } .flex { flex-wrap: wrap; }
</style>
