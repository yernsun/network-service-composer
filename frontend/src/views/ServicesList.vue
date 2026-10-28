<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { serviceSearch } from '../data/service-references'
import { useWorkspaceStore } from '../stores/workspace'
import type { Service } from '../types/domain'
import ViewContent from '../components/ViewContent.vue'
import ServicesTable from './ServicesTable.vue'

const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const filters = computed(() => {
  void store.revision
  const workload = store.service?.workload || {}
  return {
    scope: String(workload.svcScope || 'environment'),
    runtime: String(workload.svcRuntime || 'all'),
    health: String(workload.svcHealth || 'all'),
    search: String(workload.svcSearch || ''),
    layout: String(workload.layout || 'grouped'),
    page: Number(workload.svcPage || 1),
    project: String(workload.svcProject || 'all'),
    includeRemoved: Boolean(workload.includeRemoved),
  }
})
const filtered = computed(() =>
  state.value.services.filter((service) => {
    if (service.status === 'REMOVED' && !filters.value.includeRemoved) return false
    if (
      filters.value.scope === 'environment' &&
      (service.project !== state.value.project || service.env !== state.value.env)
    )
      return false
    if (filters.value.scope === 'project' && service.project !== state.value.project) return false
    if (
      filters.value.project !== 'all' &&
      filters.value.scope !== 'all' &&
      service.project !== filters.value.project
    )
      return false
    if (filters.value.runtime !== 'all' && service.runtimeId !== filters.value.runtime) return false
    const status = store.service?.serviceState(service.id)
    if (filters.value.health === 'issue' && !['issue', 'unknown'].includes(status?.key || ''))
      return false
    if (
      filters.value.health === 'running' &&
      !['healthy', 'running', 'complete'].includes(status?.key || '')
    )
      return false
    if (filters.value.health === 'external' && service.managed) return false
    const app = state.value.apps.find((item) => item.id === service.app)
    return `${serviceSearch(state.value.services, service.id)} ${service.id} ${app?.name || ''} ${service.image}`
      .toLowerCase()
      .includes(filters.value.search.toLowerCase())
  }),
)
const pageStart = computed(() => (filters.value.page - 1) * 12)
const pageServices = computed(() => filtered.value.slice(pageStart.value, pageStart.value + 12))
const groups = computed(() =>
  [...new Set(filtered.value.map((service) => service.project))].map((projectId) => ({
    id: projectId,
    name: state.value.projects.find((item) => item.id === projectId)?.name || projectId,
    environments: [
      ...new Set(
        filtered.value
          .filter((service) => service.project === projectId)
          .map((service) => service.env),
      ),
    ].map((environmentId) => ({
      id: environmentId,
      name:
        state.value.environments.find((item) => item.id === environmentId)?.name || environmentId,
      production:
        state.value.environments.find((item) => item.id === environmentId)?.kind === 'production',
      services: filtered.value.filter(
        (service) => service.project === projectId && service.env === environmentId,
      ),
    })),
  })),
)
function activeCount(services: Service[]) {
  return services.filter((service) => service.status !== 'REMOVED').length
}
function appCount(services: Service[]) {
  return new Set(
    services
      .filter((service) => service.status !== 'REMOVED')
      .map((service) => service.app)
      .filter(Boolean),
  ).size
}
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function setFilter(field: 'scope' | 'runtime' | 'health', event: Event) {
  store.service?.updateServiceFilter(field, (event.target as HTMLSelectElement).value)
}
function setSearch(event: Event) {
  store.service?.updateServiceSearch((event.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="breadcrumbs">
    <span class="current">{{ t('compose.services') }}</span>
  </div>
  <div class="x-head">
    <div>
      <div class="x-tagline">SERVICES</div>
      <h1>{{ t('compose.services') }}</h1>
      <p>{{ t('archive.message_53a2dc6770ff') }}</p>
    </div>
    <div class="hero-actions">
      <button
        class="btn"
        type="button"
        data-action="x-removed-toggle"
        data-vue-control="true"
        :class="{ active: filters.includeRemoved }"
        :aria-pressed="filters.includeRemoved"
        @click.stop="store.service?.toggleRemovedServices()"
      >
        {{ t('services.show_removed') }}
      </button>
      <button
        class="btn"
        type="button"
        data-action="x-all-services"
        data-vue-control="true"
        @click.stop="store.service?.showAllServices()"
      >
        <ViewContent :content="icon('layers')" />{{ t('archive.message_f0f1d33cd7ba') }}
      </button>
      <button
        class="btn primary"
        type="button"
        data-action="x-install"
        data-vue-control="true"
        @click.stop="store.service?.beginApplicationInstall()"
      >
        <ViewContent :content="icon('plus')" />{{ t('archive.message_3a4a165a145b') }}
      </button>
    </div>
  </div>
  <div v-if="filters.scope === 'all'" class="x-scope-global">
    <ViewContent :content="icon('globe', 'sm')" />{{
      t('archive.message_f57a21643c65', { v0: '' })
    }}
  </div>
  <div class="x-filterbar">
    <div class="search-field">
      <ViewContent :content="icon('search')" /><input
        id="x-service-search"
        :value="filters.search"
        :placeholder="t('archive.message_354c674e1d01')"
        :aria-label="t('archive.message_9eb591385b2e')"
        @input.stop="setSearch"
      />
    </div>
    <select
      id="x-service-scope"
      :value="filters.scope"
      :aria-label="t('archive.message_601bbef536e5')"
      @change.stop="setFilter('scope', $event)"
    >
      <option value="environment">{{ t('prototype.current_environment') }}</option>
      <option value="project">{{ t('archive.message_896801d732bf') }}</option>
      <option value="all">{{ t('archive.message_54a07cfdbe0f') }}</option>
    </select>
    <select
      id="x-service-runtime"
      :value="filters.runtime"
      :aria-label="t('archive.message_05ae214a4a36')"
      @change.stop="setFilter('runtime', $event)"
    >
      <option value="all">{{ t('archive.message_e58f5e3963bc') }}</option>
      <option v-for="runtime in state.runtimes" :key="runtime.id" :value="runtime.id">
        {{ runtime.name }}
      </option>
    </select>
    <select
      id="x-service-health"
      :value="filters.health"
      :aria-label="t('archive.message_75bed6a3a512')"
      @change.stop="setFilter('health', $event)"
    >
      <option value="all">{{ t('services.health_all') }}</option>
      <option value="running">{{ t('services.health_running') }}</option>
      <option value="issue">{{ t('services.health_issue') }}</option>
      <option value="external">{{ t('archive.message_2f030df1d103') }}</option>
    </select>
    <span class="spacer"></span>
    <div class="x-seg" role="group" :aria-label="t('archive.message_8b51e6d43e68')">
      <button
        type="button"
        data-action="x-layout"
        data-vue-control="true"
        data-id="grouped"
        :class="{ active: filters.layout === 'grouped' }"
        :aria-pressed="filters.layout === 'grouped'"
        @click.stop="store.service?.setServiceLayout('grouped')"
      >
        <ViewContent :content="icon('folder', 'sm')" />{{
          t('archive.message_aaf161a73ce9', { v13: '' })
        }}
      </button>
      <button
        type="button"
        data-action="x-layout"
        data-vue-control="true"
        data-id="flat"
        :class="{ active: filters.layout === 'flat' }"
        :aria-pressed="filters.layout === 'flat'"
        @click.stop="store.service?.setServiceLayout('flat')"
      >
        <ViewContent :content="icon('layers', 'sm')" />{{
          t('archive.message_0621915c2ff2', { v16: '' })
        }}
      </button>
    </div>
  </div>
  <section v-if="filters.layout === 'flat'" class="card">
    <ServicesTable :services="pageServices" />
    <div class="x-pagination">
      <span>{{
        t('archive.message_4df79f7f80b9', {
          v1: filtered.length ? pageStart + 1 : 0,
          v2: Math.min(pageStart + 12, filtered.length),
          v3: filtered.length,
        })
      }}</span>
      <div class="flex">
        <button
          class="btn sm"
          type="button"
          :disabled="filters.page === 1"
          @click.stop="store.service?.stepServicePage(-1)"
        >
          {{ t('console.previous') }}
        </button><button
          class="btn sm"
          type="button"
          :disabled="pageStart + 12 >= filtered.length"
          @click.stop="store.service?.stepServicePage(1)"
        >
          {{ t('console.next_page') }}
        </button>
      </div>
    </div>
  </section>
  <template v-else>
    <details v-for="group in groups" :key="group.id" class="card x-group" open>
      <summary>
        <ViewContent :content="icon('folder')" /><strong>{{ group.name }}</strong><span class="badge">{{ activeCount(group.environments.flatMap((item) => item.services))
        }}{{ t('archive.message_97827a9c3766') }}</span>
      </summary>
      <div v-for="environment in group.environments" :key="environment.id" class="x-env-group">
        <ViewContent :content="icon('layers', 'sm')" /><span
          class="badge"
          :class="environment.production ? 'green' : 'blue'"
        >{{ environment.name }}</span><span>{{
          t('archive.message_8ed67fa22b40', {
            v2: appCount(environment.services),
            v3: activeCount(environment.services),
          })
        }}</span><ServicesTable :services="environment.services" :show-scope="false" />
      </div>
    </details>
    <div v-if="!groups.length" class="card x-empty">{{ t('archive.message_5ae1a83b9ac5') }}</div>
  </template>
  <div class="x-info-note mt16">{{ t('archive.message_cabe1e52d6cb') }}</div>
  <div class="x-foot">{{ t('archive.message_04f70a4dbc01') }}</div>
</template>
