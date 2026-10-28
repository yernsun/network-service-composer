<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useWorkspaceStore } from '../stores/workspace'
import { translate as t } from '../i18n'
const store = useWorkspaceStore()
type NetworkWizard = { runtime: string; project: string; key: string; step: number; networkName?: string; networkPreview?: string; touched: boolean }
const wizard = computed(() => { void store.revision; const value = store.service?.workload.wizard as NetworkWizard | undefined; return value ? { ...value } : undefined })
const swarm = computed(() => { void store.revision; return store.state().runtimes.some(r => r.id === wizard.value?.runtime && r.type === 'swarm') })
const projectName = computed(() => store.state().projects.find(p => p.id === wizard.value?.project)?.key || '')
const name = ref(wizard.value?.networkName || '')
watch(projectName, (value, previous) => {
 const current = store.service?.workload.wizard as NetworkWizard | undefined
 if (current && (!current.networkName || current.networkName === previous)) { current.networkName = value; name.value = value }
}, { immediate: true })
function update() {
  const value = store.service?.workload.wizard as NetworkWizard | undefined
  if (!value) return
  value.networkName = name.value.trim()
  value.touched = true
}
</script>
<template>
  <div v-if="swarm && wizard" class="callout blue" data-testid="deployment-network">
    <label v-if="wizard.step < 4" for="deployment-network-name">{{ t('bindingUI.networkName') }}<input id="deployment-network-name" v-model="name" :placeholder="projectName" maxlength="128" pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]*" @input="update"></label>
    <p>{{ t('bindingUI.networkName') }}: <strong>{{ wizard.networkPreview && wizard.step === 4 ? wizard.networkPreview : name || projectName }}</strong></p>
    <p>{{ t('bindingUI.applicationNetworkHint') }}</p>
  </div>
</template>

<style scoped>
.callout { display: grid; gap: 10px; margin-top: 18px; min-width: 0; }
label { display: grid; gap: 7px; min-width: 0; }
input { width: 100%; min-width: 0; }
p { margin: 0; overflow-wrap: anywhere; }
</style>
