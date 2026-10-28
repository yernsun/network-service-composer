<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from './ViewContent.vue'

const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const ui = computed(() => {
  void store.revision
  return { ...store.service!.ui }
})
const project = computed(() => state.value.projects.find((item) => item.id === state.value.project))
const environments = computed(() =>
  state.value.environments.filter((item) => item.project === state.value.project),
)
const aggregatePages = new Set([
  'applications',
  'services',
  'domains',
  'routes',
  'configs',
  'certificates',
  'workloadHistory',
  'deployments',
  'events',
  'gateways',
])
const allProjects = computed(() =>
  Boolean(ui.value.allProjects && aggregatePages.has(String(ui.value.page))),
)
const allEnvironments = computed(() =>
  Boolean(
    (ui.value.allEnvironments || ui.value.allProjects) &&
    aggregatePages.has(String(ui.value.page)) &&
    !(
      ui.value.page === 'services' &&
      store.service!.workload.svcScope === 'all' &&
      !ui.value.allProjects
    ),
  ),
)
const visible = computed(
  () =>
    ![
      'projects',
      'project',
      'environments',
      'market',
      'runtimes',
      'runtime',
      'runtimeEvents',
    ].includes(String(ui.value.page)) &&
    !(
      ui.value.page === 'services' &&
      store.service!.workload.svcScope === 'all' &&
      !allProjects.value
    ),
)
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
</script>

<template>
  <div v-if="visible && allProjects" class="project-context global-scope-context">
    <button
      class="context-project"
      type="button"
      data-action="nav"
      data-page="projects"
      @click.stop="store.service?.navigatePage('projects')"
    >
      <ViewContent :content="icon('folder')" /><strong>{{ t('workspace.all_projects') }}</strong><span class="badge">{{ state.projects.length }}</span>
    </button>
    <span class="global-scope-environments">{{
      t('archive.message_7103994ee601', { v2: '', v3: state.environments.length })
    }}</span>
    <span class="global-scope-note">{{ t('archive.message_ffc5a412f0a4') }}</span>
  </div>
  <div v-else-if="visible" class="project-context">
    <button
      class="context-project"
      type="button"
      data-action="nav"
      data-page="project"
      @click.stop="store.service?.navigatePage('project')"
    >
      <ViewContent :content="icon('folder')" /><strong>{{ project?.name }}</strong><ViewContent :content="icon('right', 'sm')" />
    </button>
    <div class="env-tabs" role="group" :aria-label="t('prototype.project_environments')">
      <button
        type="button"
        data-action="p-env"
        data-id="all"
        data-vue-control="true"
        :class="{ selected: allEnvironments }"
        @click.stop="store.service?.selectContextEnvironment('all')"
      >
        {{ t('archive.message_661ea89c7127', { v4: '' }) }}
      </button>
      <button
        v-for="item in environments"
        :key="item.id"
        type="button"
        data-action="p-env"
        data-vue-control="true"
        :data-id="item.id"
        :class="{ selected: !allEnvironments && item.id === state.env }"
        @click.stop="store.service?.selectContextEnvironment(item.id)"
      >
        <span class="dot" :class="{ gray: item.kind !== 'production' }" />{{ item.name }}
      </button>
    </div>
    <button
      class="iconbtn"
      type="button"
      data-action="p-env-settings"
      :data-id="state.env"
      :aria-label="t('archive.message_f812dc4f80ae')"
      :title="t('archive.message_f812dc4f80ae')"
      @click.stop="store.service?.editEnvironment(state.env)"
    >
      <ViewContent :content="icon('settings', 'sm')" />
    </button>
    <button
      class="iconbtn"
      type="button"
      data-action="p-env-add"
      :aria-label="t('archive.message_a2a1229f025d')"
      :title="t('archive.message_a2a1229f025d')"
      @click.stop="store.service?.createEnvironment()"
    >
      <ViewContent :content="icon('plus', 'sm')" />
    </button>
  </div>
</template>
