<script setup lang="ts">
import { translate as t } from '../i18n'
import { computed, ref, watch, onBeforeUpdate, onUpdated } from 'vue'
import { i18n } from '../shared/i18n'
import { retainModalControls } from '../i18n/modal'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from './ViewContent.vue'
import DeploymentNetwork from './DeploymentNetwork.vue'
import DeploymentPorts from './DeploymentPorts.vue'
import DeploymentEntrypoint from './DeploymentEntrypoint.vue'
import DeploymentAnalysis from './DeploymentAnalysis.vue'
import MarketplaceParameters from './MarketplaceParameters.vue'
import DeploymentWizardBody from './DeploymentWizardBody.vue'
import ProjectForm from './ProjectForm.vue'
import EnvironmentForm from './EnvironmentForm.vue'
import RouteForm from './RouteForm.vue'
import VariableForm from './VariableForm.vue'
import VariableDialogs from './VariableDialogs.vue'
import CommandPalette from './CommandPalette.vue'
const composeEditing = ref(false)
defineProps<{ revision: number }>()
const store = useWorkspaceStore()
const modal = computed(() => store.modal())
watch(() => modal.value?.kind, kind => { if (kind !== 'x-wizard') composeEditing.value = false })
let renderedLanguage = i18n.global.locale.value
let restoreControls: (() => void) | undefined
onBeforeUpdate(() => {
  if (renderedLanguage !== i18n.global.locale.value) restoreControls = retainModalControls()
})
onUpdated(() => {
  restoreControls?.(); restoreControls = undefined
  renderedLanguage = i18n.global.locale.value
})
</script>

<template>
  <div id="overlay">
    <div v-if="modal" class="backdrop" data-action="backdrop" @click.self.stop="store.service?.closeModal()">
      <section :class="['modal', modal.size || '']" role="dialog" aria-modal="true" aria-labelledby="modal-title">
        <CommandPalette v-if="modal.kind === 'palette'" />
        <template v-else>
          <div class="modal-head">
            <div><h2 id="modal-title"><ViewContent :content="modal.title" /></h2><p v-if="modal.sub"><ViewContent :content="modal.sub" /></p></div>
            <button class="iconbtn" type="button" data-action="close" :aria-label="t('common.close')" :disabled="modal.locked" @click.stop="store.service?.closeModal()"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
          </div>
          <ProjectForm v-if="modal.kind === 'p-project-form'" />
          <EnvironmentForm v-else-if="modal.kind === 'p-env-form'" :key="modal.envId || 'new'" :env-id="modal.envId" />
          <RouteForm v-else-if="modal.kind === 'route'" />
          <VariableForm v-else-if="modal.kind === 'y-variable'" />
          <VariableDialogs v-else-if="['y-env-import', 'y-remove', 'y-usage', 'y-history'].includes(modal.kind || '')" :kind="modal.kind || ''" />
          <template v-else>
            <div class="modal-body">
              <div v-show="!composeEditing">
                <DeploymentWizardBody v-if="modal.kind === 'x-wizard'" :content="modal.body">
                  <MarketplaceParameters /><DeploymentPorts /><DeploymentNetwork /><DeploymentEntrypoint />
                </DeploymentWizardBody>
                <ViewContent v-else :content="modal.body" />
              </div>
              <DeploymentAnalysis v-if="modal.kind === 'x-wizard'" @editing="composeEditing = $event" />
            </div>
            <div v-if="modal.footer" v-show="!composeEditing" class="modal-foot"><ViewContent :content="modal.footer" /></div>
          </template>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
.modal-head > div:first-child { flex: 1; min-width: 0; }
.modal-head > .iconbtn { flex-shrink: 0; align-self: flex-start; }
</style>
