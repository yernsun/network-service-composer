<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'

const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
function environments(projectId: string) {
  return state.value.environments.filter((item) => item.project === projectId)
}
function domains(projectId: string) {
  return state.value.domains.filter((item) => item.project === projectId)
}
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function enterProject(id: string) {
  store.service?.enterProject(id)
}
</script>

<template>
  <div class="breadcrumbs">
    <span class="current">{{ t('prototype.all_projects') }}</span>
  </div>
  <div class="global-title">
    <div>
      <h1>{{ t('prototype.project_group') }}</h1>
      <p>{{ t('prototype.projects_intro') }}</p>
    </div>
    <button
      class="btn"
      type="button"
      data-action="scope-all-projects"
      @click.stop="store.service?.browseAllProjects()"
    >
      <ViewContent :content="icon('layers')" />{{ t('workspace.browse_all') }}
    </button>
    <button
      class="btn primary"
      type="button"
      data-action="p-project-add"
      data-vue-control="true"
      @click.stop="store.service?.openProjectForm()"
    >
      <ViewContent :content="icon('plus')" />{{ t('archive.message_43d7d2b51d75') }}
    </button>
  </div>
  <div class="project-grid">
    <article
      v-for="project in state.projects"
      :key="project.id"
      class="card project-card actionable-card"
      role="link"
      tabindex="0"
      data-action="p-project"
      data-vue-control="true"
      :data-id="project.id"
      @click.stop="enterProject(project.id)"
      @keydown.enter.prevent="enterProject(project.id)"
      @keydown.space.prevent="enterProject(project.id)"
    >
      <div class="flex">
        <div class="project-large-symbol small"><ViewContent :content="icon('folder')" /></div>
        <div class="spacer">
          <h2>{{ project.name }}</h2>
          <p class="muted mono">{{ project.key }}</p>
        </div>
        <span class="badge" :class="environments(project.id).length === 1 ? '' : 'green'">{{
          t(
            environments(project.id).length === 1
              ? 'archive.message_a3e236748012'
              : 'archive.message_c40675cc5749',
          )
        }}</span>
      </div>
      <p class="project-description">
        {{ project.description || t('archive.message_45115b166f77') }}
      </p>
      <div class="flex wrap">
        <span
          v-for="environment in environments(project.id)"
          :key="environment.id"
          class="badge"
          :class="{ green: environment.kind === 'production' }"
        >{{ environment.name }}</span>
      </div>
      <div class="card-foot">
        <span>{{
          t('archive.message_100b402c2b90', {
            v6: environments(project.id).length,
            v7: domains(project.id).length,
          })
        }}</span>
        <div>
          <button
            class="btn sm"
            type="button"
            data-action="p-project"
            data-vue-control="true"
            :data-id="project.id"
            @click.stop="enterProject(project.id)"
          >
            {{ t('prototype.enter_project') }}
          </button>
          <button
            class="btn sm"
            type="button"
            data-action="p-project-settings"
            :data-id="project.id"
            @click.stop
          >
            {{ t('projectLifecycle.settings') }}
          </button>
        </div>
      </div>
    </article>
  </div>
  <div class="ribbon mt16">
    {{ t('archive.message_e191190914d9', { v3: state.projects.length }) }}
  </div>
</template>
