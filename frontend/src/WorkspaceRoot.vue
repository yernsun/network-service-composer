<script setup lang="ts">
import { getCurrentInstance, onBeforeUnmount, watchEffect } from 'vue'
import { RouterView } from 'vue-router'
import { useWorkspaceStore } from './stores/workspace'
import WorkspaceLayout from './components/WorkspaceLayout.vue'
import ModalHost from './components/ModalHost.vue'
import ToastHost from './components/ToastHost.vue'
import ProjectSettings from './components/ProjectSettings.vue'
import { navigationLoading, refreshFailure, retryResourceRefresh } from './data/resource-changes'
import { translate as t } from './i18n'
const store = useWorkspaceStore()
watchEffect(() => { void store.revision; document.title = (store.service?.pageTitle || 'NSC') + ' · Network Service Composer' })
const instance = getCurrentInstance()!
// Original form workflows restore focus immediately after an action. Flush the
// Vue component tree before those DOM focus/selection reads, without replacing HTML.
store.connectRenderer(() => { (instance as unknown as { update: () => void }).update() })
onBeforeUnmount(() => store.dispose())
</script>
<template>
  <div v-if="navigationLoading" class="workspace-loading" role="status" aria-live="polite">{{ t('refresh.loading') }}</div>
  <section v-if="refreshFailure" class="workspace-refresh-notice callout amber" role="alert">
    {{ t('refresh.failed') }}
    <button class="btn sm" type="button" :disabled="navigationLoading" @click="retryResourceRefresh?.()">{{ t('console.retry') }}</button>
  </section>
  <WorkspaceLayout :revision="store.revision"><RouterView /></WorkspaceLayout>
  <ModalHost :revision="store.revision" />
  <ToastHost :revision="store.revision" />
  <ProjectSettings />
</template>
<style scoped>
.workspace-loading { position: fixed; inset: 0 0 auto; z-index: 2000; padding: 6px 16px; background: var(--accent, #07975c); color: white; text-align: center; pointer-events: none; }
.workspace-refresh-notice { position: fixed; top: 76px; right: 16px; z-index: 1900; max-width: min(440px, calc(100vw - 32px)); }
</style>
