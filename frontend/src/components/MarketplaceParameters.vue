<script setup>
import { computed, ref, watch } from 'vue'
import { useWorkspaceStore } from '../stores/workspace'
import { translate as t } from '../i18n'
import { i18n } from '../shared/i18n'
import { invalidateCompose } from '../compose/session'
const store = useWorkspaceStore()
const wizard = computed(() => { void store.revision; return store.service?.workload.wizard })
const parameters = computed(() => { void store.revision; return wizard.value?.source === 'template' ? wizard.value.catalogVariables || [] : [] })
const visible = computed(() => { void store.revision; return wizard.value && parameters.value.length && wizard.value.step < 4 })
const values = ref({})
watch(() => { void store.revision; return [wizard.value?.template, wizard.value?.catalogVariables] }, () => {
 const w=wizard.value
 values.value={...(w?.catalogValues || {})}
 for (const p of parameters.value) if (!(p.key in values.value)) values.value[p.key]=p.secret ? '' : p.default || ''
}, { immediate: true })
function update() {
 const w=wizard.value
 if (!w) return
 w.catalogValues={...values.value}
 w.runtimeEnv=parameters.value.filter(p=>values.value[p.key] !== '').map(p=>`${p.key}=${JSON.stringify(String(values.value[p.key] || '').replaceAll('$', () => '$$'))}`).join('\n')
 w.touched=true
 invalidateCompose(w)
 window.dispatchEvent(new Event('nsc:compose-change'))
}
function description(p) { return typeof p.description === 'string' ? p.description : p.description?.[i18n.global.locale.value] || p.description?.en || '' }
</script>
<template>
  <section v-if="visible" data-testid="market-parameters" class="marketplace-parameters">
    <h3>{{ t('market.parameters') }}</h3>
    <div class="market-parameter-grid">
      <div v-for="p in parameters" :key="p.key" class="market-parameter">
        <label :for="`market-parameter-${p.key}`">{{ p.key }}</label>
        <input :id="`market-parameter-${p.key}`" v-model="values[p.key]" :type="p.secret ? 'password' : 'text'" :required="p.required" :minlength="p.min_length" :maxlength="p.max_length" :pattern="p.pattern" :aria-invalid="p.required && !values[p.key] ? 'true' : undefined" :aria-describedby="[`market-parameter-${p.key}-help`, ...(p.required && !values[p.key] ? [`market-parameter-${p.key}-error`] : [])].join(' ')" autocomplete="off" @input="update">
        <small v-if="p.required && !values[p.key]" :id="`market-parameter-${p.key}-error`" class="parameter-error">{{ t('market.parameterRequired') }}</small>
        <small :id="`market-parameter-${p.key}-help`" class="parameter-help">{{ description(p) }}</small>
      </div>
    </div>
  </section>
</template>

<style scoped>
.marketplace-parameters { border: 1px solid var(--line); border-radius: 9px; padding: 16px; margin-top: 18px; }
.marketplace-parameters h3 { margin: 0 0 16px; }
.market-parameter-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px 16px; }
.market-parameter { display: flex; flex-direction: column; gap: 7px; min-width: 0; }
.market-parameter label { font-weight: 600; overflow-wrap: anywhere; }
.market-parameter input { width: 100%; min-width: 0; }
.parameter-help, .parameter-error { display: block; line-height: 1.6; overflow-wrap: anywhere; }
.parameter-help { color: var(--muted); }
.parameter-error { color: var(--red); }
@media (max-width:700px) { .market-parameter-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
