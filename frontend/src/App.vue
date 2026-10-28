<script setup lang="ts">
import { RouterView, useRoute } from 'vue-router'
import WorkspaceRoot from './WorkspaceRoot.vue'
import { useWorkspaceStore } from './stores/workspace'
import { bootstrapError } from './auth/bootstrap'
import { errorText } from './shared/api/client'
import { translate as t } from './i18n'
const route = useRoute()
const store = useWorkspaceStore()
function retryBootstrap() { window.location.reload() }
</script>
<template>
  <main v-if="bootstrapError" class="auth-page"><section class="card auth-card"><p class="callout red" role="alert">{{ errorText(bootstrapError) }}</p><button class="btn primary" @click="retryBootstrap">{{ t('console.retry') }}</button></section></main>
  <WorkspaceRoot v-else-if="store.service && !route.meta.public" />
  <RouterView v-else />
</template>
