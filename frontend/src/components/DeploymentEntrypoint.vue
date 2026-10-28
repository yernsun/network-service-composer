<script setup>
import { computed } from 'vue'
import { useWorkspaceStore } from '../stores/workspace'
import { translate as t } from '../i18n'
import { certificateCovers } from '../data/service-bindings'
const store = useWorkspaceStore()
const wizard = computed(() => { void store.revision; const value = store.service?.workload?.wizard; return value ? { ...value } : null })
const certificates = computed(() => {
 void store.revision
 const w = wizard.value
 const runtime = store.state().runtimes.find(r => r.id === w?.runtime)
 return (store.state().certificates || []).filter(c => c.pool_id === runtime?.pool_id && certificateCovers(c, w?.domain || ''))
})
function change(event) {
 const w = store.service?.workload?.wizard; if (!w) return
 w.certificateID = event.target.value; w.touched = true
 window.dispatchEvent(new Event('nsc:entrypoint-change'))
}
</script>
<template>
  <section v-if="wizard && wizard.access !== 'internal' && wizard.step >= 3" class="callout blue">
    <label>{{ t('prototype.tls_certificates') }}
      <select id="deployment-entry-certificate" :value="wizard.certificateID || ''" :disabled="wizard.step === 4" @change="change">
        <option value="">HTTP</option><option v-for="certificate in certificates" :key="certificate.id" :value="certificate.id">HTTPS · {{ certificate.domains.join(', ') }}</option>
      </select>
    </label>
    <p>{{ wizard.domain }} · {{ wizard.certificateID ? 'HTTPS' : 'HTTP' }}</p>
  </section>
</template>
