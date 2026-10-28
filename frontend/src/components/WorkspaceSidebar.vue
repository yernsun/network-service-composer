<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { session } from '../shared/api/client'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from './ViewContent.vue'

const store = useWorkspaceStore()
const route = useRoute()
const state = computed(() => ({ ...store.state() }))
const ui = computed(() => {
  void store.revision
  return { ...store.service!.ui }
})
const parentPages: Record<string, string> = {
  application: 'applications',
  compose: 'applications',
  service: 'services',
  domain: 'domains',
  runtime: 'runtimes',
  node: 'runtimes',
  instance: 'runtimes',
}
const activePage = computed(() => {
  if (route.path === '/account' || route.path === '/users') return route.path.slice(1)
  const page = String(ui.value.page)
  return parentPages[page] || page
})
const project = computed(() => state.value.projects.find((item) => item.id === state.value.project))
const environment = computed(() =>
  state.value.environments.find((item) => item.id === state.value.env),
)
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
const domainCount = computed(
  () =>
    state.value.domains.filter(
      (item) =>
        allProjects.value ||
        (item.project === state.value.project &&
          (allEnvironments.value || item.env === state.value.env)),
    ).length,
)
const applicationCount = computed(
  () =>
    state.value.apps.filter(
      (item) =>
        allProjects.value ||
        (item.project === state.value.project &&
          (allEnvironments.value || item.env === state.value.env)),
    ).length,
)
const projectItems = computed(() => [
  { page: 'project', label: t('prototype.project_overview'), icon: 'grid', count: '' },
  { page: 'environments', label: t('prototype.environments_domains'), icon: 'layers', count: '' },
  { page: 'variables', label: t('prototype.variables_configuration'), icon: 'sliders', count: '' },
])
const environmentItems = computed(() => [
  {
    page: 'applications',
    label: t('archive.message_63c73c4730f4'),
    icon: 'box',
    count: String(applicationCount.value),
  },
  { page: 'services', label: t('cluster.services'), icon: 'layers', count: '' },
  {
    page: 'domains',
    label: t('archive.message_222952431147'),
    icon: 'globe',
    count: String(domainCount.value),
  },
  { page: 'routes', label: t('prototype.routing_rules'), icon: 'route', count: '' },
  { page: 'configs', label: t('archive.message_3f90761c6e4a'), icon: 'code', count: '' },
  { page: 'certificates', label: t('prototype.tls_certificates'), icon: 'shield', count: '' },
  {
    page: 'workloadHistory',
    label: t('prototype.deployments_changes'),
    icon: 'history',
    count: '',
  },
])
const infrastructureItems = computed(() => [
  { page: 'activity', label: t('activityHub.title'), icon: 'activity' },
  ...(session.value.role === 'administrator' ? [{ page: 'ha', label: t('ha.title'), icon: 'activity' }] : []),
  { page: 'market', label: t('archive.message_f2e7caa9f119'), icon: 'grid' },
  { page: 'runtimes', label: t('prototype.clusters_hosts'), icon: 'server' },
  { page: 'runtimeEvents', label: t('prototype.runtime_records'), icon: 'activity' },
  { page: 'gateways', label: t('prototype.available_gateways'), icon: 'server', count: '' },
])
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function navigate(page: string) {
  store.service?.navigatePage(page)
}
function selectProject(event: Event) {
  store.service?.selectProject((event.target as HTMLSelectElement).value)
}
function selectEnvironment(event: Event) {
  store.service?.selectEnvironment((event.target as HTMLSelectElement).value)
}
</script>

<template>
  <aside class="sidebar" :class="{ open: ui.menu }">
    <button
      class="all-projects"
      type="button"
      data-action="nav"
      data-page="projects"
      @click.stop="navigate('projects')"
    >
      <ViewContent :content="icon('back', 'sm')" />
      {{ t('archive.message_7003ecc1eeb5', { v1: '' }) }}
      <span class="kbd">{{ state.projects.length }}</span>
    </button>
    <div class="scope-selector">
      <label for="scope-project">{{ t('archive.message_3ec016603b5e') }}</label>
      <div class="selector-row">
        <ViewContent :content="icon('folder')" />
        <select
          id="scope-project"
          :value="allProjects ? 'all' : state.project"
          :aria-label="t('archive.message_3328125f8a4d')"
          @change.stop="selectProject"
        >
          <option value="all">{{ t('workspace.all_projects') }}</option>
          <option v-for="item in state.projects" :key="item.id" :value="item.id">
            {{ item.name }}
          </option>
        </select>
        <ViewContent :content="icon('chevron', 'sm')" />
      </div>
      <div class="project-key">
        {{ allProjects ? t('archive.message_e2c00c9bebfe') : project?.key }}
      </div>
    </div>
    <div class="nav-label">{{ t('prototype.project_group') }}</div>
    <button
      v-for="item in projectItems"
      :key="item.page"
      class="nav-item"
      :class="{ active: activePage === item.page }"
      :aria-current="activePage === item.page ? 'page' : undefined"
      type="button"
      data-action="nav"
      :data-page="item.page"
      @click.stop="navigate(item.page)"
    >
      <ViewContent :content="icon(item.icon)" /><span>{{ item.label }}</span><span v-if="item.count !== ''" class="count">{{ item.count }}</span>
    </button>
    <div class="scope-selector compact">
      <label for="scope-env">{{ t('archive.message_a818af3eb531') }}</label>
      <div class="selector-row">
        <span class="dot" :class="{ gray: environment?.kind !== 'production' }" />
        <select
          id="scope-env"
          :value="allEnvironments ? 'all' : state.env"
          :aria-label="t('archive.message_4fab2fa2aaa0')"
          @change.stop="selectEnvironment"
        >
          <option value="all">{{ t('workspace.all_environments') }}</option>
          <option v-for="item in allProjects ? [] : environments" :key="item.id" :value="item.id">
            {{ item.name }}
          </option>
        </select>
        <ViewContent :content="icon('chevron', 'sm')" />
      </div>
      <div v-if="environments.length === 1" class="project-key">
        {{ t('archive.message_221293283d82') }}
      </div>
    </div>
    <button
      v-for="item in environmentItems"
      :key="item.page"
      class="nav-item"
      :class="{ active: activePage === item.page }"
      :aria-current="activePage === item.page ? 'page' : undefined"
      type="button"
      data-action="nav"
      :data-page="item.page"
      @click.stop="navigate(item.page)"
    >
      <ViewContent :content="icon(item.icon)" /><span>{{ item.label }}</span><span v-if="item.count !== ''" class="count">{{ item.count }}</span>
    </button>
    <div class="nav-label">{{ t('prototype.resources_infrastructure') }}</div>
    <button
      v-for="item in infrastructureItems"
      :key="item.page"
      class="nav-item"
      :class="{ active: activePage === item.page }"
      :aria-current="activePage === item.page ? 'page' : undefined"
      type="button"
      data-action="nav"
      :data-page="item.page"
      @click.stop="navigate(item.page)"
    >
      <ViewContent :content="icon(item.icon)" /><span>{{ item.label }}</span>
    </button>
    <div class="sidebar-bottom">
      <RouterLink to="/account" class="nav-item" :class="{ active: activePage === 'account' }">{{ t('account.title') }}</RouterLink>
      <RouterLink v-if="session.role === 'administrator'" to="/users" class="nav-item" :class="{ active: activePage === 'users' }">{{ t('users.title') }}</RouterLink>
      <div class="demo-note">
        <div class="flex">
          <ViewContent :content="icon('play', 'sm')" /><strong>{{
            t('prototype.local_design')
          }}</strong><span class="badge">1.3</span>
        </div>
        <p>{{ t('archive.message_39db43275bce') }}</p>
        <button
          class="textbtn"
          type="button"
          data-action="x-demo"
          @click.stop="store.service?.openDemoSettings()"
        >
          {{ t('prototype.failover_demo') }}
        </button>
      </div>
    </div>
  </aside>
</template>
