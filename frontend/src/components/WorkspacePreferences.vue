<script setup lang="ts">
import { computed } from 'vue'
import { usePreferencesStore } from '../stores/preferences'
import { useWorkspaceStore } from '../stores/workspace'
import { translate as t } from '../i18n'
import { languages } from '../shared/i18n'
const preferences = usePreferencesStore()
const workspace = useWorkspaceStore()
const operations = computed(() => { void workspace.revision; return workspace.state().view === 'ops' })
function togglePerspective() { workspace.service?.setPerspective(operations.value ? 'cloud' : 'ops') }
</script>
<template>
  <div class="workspace-preferences">
    <label class="preference-select" :title="t('ui.locale')">
      <svg class="icon sm" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3a18 18 0 0 1 0 18 18 18 0 0 1 0-18Z" /></svg>
      <select id="workspace-language" v-model="preferences.language" :aria-label="t('ui.locale')"><option v-for="locale in languages" :key="locale.code" :value="locale.code">{{ locale.name }}</option></select>
    </label>
    <label class="preference-select" :title="t('prototype.theme')">
      <svg class="icon sm" viewBox="0 0 24 24" aria-hidden="true"><path v-if="preferences.resolvedTheme === 'dark'" d="M20 14A8 8 0 0 1 10 4a8.5 8.5 0 1 0 10 10Z" /><template v-else><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5" /></template></svg>
      <select id="workspace-theme" v-model="preferences.theme" :aria-label="t('prototype.theme')"><option value="light">{{ t('console.light') }}</option><option value="dark">{{ t('console.dark') }}</option><option value="system">{{ t('console.system') }}</option></select>
    </label>
    <button id="perspective-toggle" class="perspective-toggle" role="switch" :aria-checked="operations" :aria-label="t('prototype.operations_view')" :title="operations ? 'Cloud Native' : t('prototype.operations_view')" data-action="view" :data-view="operations ? 'cloud' : 'ops'" @click.stop="togglePerspective">
      <span :class="['perspective-label', { selected: !operations, inactive: operations }]">Cloud Native</span>
      <span class="toggle-track" :class="{ on: operations }" aria-hidden="true"><span /></span>
      <span :class="['perspective-label', { selected: operations, inactive: !operations }]">{{ t('prototype.operations_view') }}</span>
    </button>
  </div>
</template>
