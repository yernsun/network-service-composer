<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import WorkspaceTopbar from './WorkspaceTopbar.vue'
import WorkspaceSidebar from './WorkspaceSidebar.vue'
import WorkspaceContextBar from './WorkspaceContextBar.vue'
import WorkspacePreferences from './WorkspacePreferences.vue'
import { router } from '../router'
import { useRoute } from 'vue-router'
import OperationEntrypoint from './OperationEntrypoint.vue'
import OperationReadiness from './OperationReadiness.vue'
const route = useRoute()
const operationId = computed(() => route.path.match(/^\/operations\/([^/]+)$/)?.[1])
defineProps<{ revision: number }>()
const store = useWorkspaceStore()
const warning = computed(() => { void store.revision; return store.service?.storageWarning })
</script>

<template>
  <WorkspaceTopbar :revision="revision" />
  <Teleport to=".topbar" defer>
    <div id="y-history-tools" class="y-history-tools">
      <button id="y-history-back" class="iconbtn" :title="t('common.back')" :aria-label="t('common.back')" @click="router.back()"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M21 12H3m5-5-5 5 5 5" /></svg></button>
      <button id="y-history-forward" class="iconbtn" :title="t('prototype.forward')" :aria-label="t('prototype.forward')" @click="router.forward()"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 5 7 7-7 7" /></svg></button>
    </div>
    <WorkspacePreferences />
  </Teleport>
  <WorkspaceSidebar />
  <div class="mobile-scrim" data-action="menu"></div>
  <main id="main" class="main">
    <WorkspaceContextBar />
    <div v-if="warning" class="callout amber">{{ warning }}</div>
    <div v-if="store.service?.ui.externalConflict" class="callout red">{{ t('archive.externalConflict') }} <button class="textbtn" data-action="export">{{ t('archive.exportCurrent') }}</button> · <button class="textbtn" data-action="reload-state">{{ t('archive.reloadState') }}</button></div>
    <slot />
    <OperationReadiness v-if="operationId" :operation-id="operationId" />
    <OperationEntrypoint v-if="operationId" :operation-id="operationId" />
  </main>
</template>
