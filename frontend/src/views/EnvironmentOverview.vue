<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'
import DomainsList from './DomainsList.vue'
import EnvironmentCapabilities from '../components/EnvironmentCapabilities.vue'
import ImageUpdates from '../components/ImageUpdates.vue'

const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const environment = computed(() =>
  state.value.environments.find((item) => item.id === state.value.env),
)
const project = computed(() => state.value.projects.find((item) => item.id === state.value.project))
const domains = computed(() =>
  state.value.domains.filter(
    (item) => item.project === state.value.project && item.env === state.value.env,
  ),
)
const services = computed(() =>
  state.value.services.filter(
    (item) =>
      item.project === state.value.project &&
      item.env === state.value.env &&
      item.status !== 'REMOVED',
  ),
)
const gatewayNames = computed(
  () =>
    environment.value?.gateways
      .map((id) => state.value.gateways.find((gateway) => gateway.id === id)?.name || id)
      .join(' / ') || '',
)
const kindLabels: Record<string, string> = {
  production: 'archive.message_76ab7d3b41f2',
  development: 'archive.message_9ec428625fee',
  testing: 'archive.message_6aa8f49cc992',
  sandbox: 'archive.message_2a8ce2b15d65',
  custom: 'archive.message_4eafa9e925b3',
}
const metrics = computed(() => [
  { value: domains.value.length, label: t('bindingUI.host'), icon: 'globe', page: 'domains' },
  { value: services.value.length, label: t('compose.services'), icon: 'box', page: 'services' },
  {
    value: domains.value.reduce(
      (sum, domain) => sum + (domain.draft?.config || domain.active).routes.length,
      0,
    ),
    label: t('archive.message_b9670c85a4ab'),
    icon: 'route',
    page: 'routes',
  },
  {
    value: domains.value.filter((domain) => domain.draft).length,
    label: t('prototype.pending_drafts'),
    icon: 'edit',
    page: 'domains',
  },
])
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function navigate(page: string) {
  store.service?.navigatePage(page)
}
</script>

<template>
  <template v-if="environment && project">
    <div class="breadcrumbs">
      <button type="button" data-action="nav" data-page="project" @click.stop="navigate('project')">
        {{ project.name }}
      </button><button
        type="button"
        data-action="nav"
        data-page="environments"
        @click.stop="navigate('environments')"
      >
        {{ environment.name }}
      </button><span class="current">{{ t('prototype.environment_overview') }}</span>
    </div>
    <div class="global-title">
      <div>
        <div class="flex">
          <h1>{{ environment.name }}</h1>
          <span class="badge" :class="environment.kind === 'production' ? 'green' : 'blue'">{{
            t(kindLabels[environment.kind] || 'archive.message_4eafa9e925b3')
          }}</span>
        </div>
        <p>{{ t('archive.message_be655ad3cd21', { v3: project.name, v4: environment.slug }) }}</p>
      </div>
      <button
        class="btn"
        type="button"
        data-action="p-env-settings"
        data-vue-control="true"
        :data-id="environment.id"
        @click.stop="store.service?.editEnvironment(environment.id)"
      >
        <ViewContent :content="icon('settings')" />{{ t('archive.message_f812dc4f80ae') }}
      </button><button
        class="btn primary"
        type="button"
        data-action="domain-add"
        data-vue-control="true"
        @click.stop="store.service?.addDomain()"
      >
        <ViewContent :content="icon('plus')" />{{ t('bindingUI.add') }}
      </button>
    </div>
    <div class="project-metrics">
      <button
        v-for="metric in metrics"
        :key="metric.page + metric.label"
        class="metric actionable-card"
        type="button"
        data-action="nav"
        :data-page="metric.page"
        @click.stop="navigate(metric.page)"
      >
        <div class="metric-icon"><ViewContent :content="icon(metric.icon)" /></div>
        <div>
          <div class="metric-number">{{ metric.value }}</div>
          <div class="metric-label">{{ metric.label }}</div>
        </div>
      </button>
    </div>
    <EnvironmentCapabilities :key="environment.id" :environment-id="environment.id" @configure="navigate" />
    <ImageUpdates :key="`updates-${environment.id}`" :environment-id="environment.id" />
    <section v-if="!domains.length" class="card setup-card">
      <div class="project-large-symbol"><ViewContent :content="icon('layers')" /></div>
      <h2>{{ t('archive.message_fbacfc672dbd') }}</h2>
      <p>{{ t('archive.message_4058ea2eac8c') }}</p>
      <div class="setup-steps">
        <button
          type="button"
          data-action="p-env-settings"
          data-vue-control="true"
          :data-id="environment.id"
          @click.stop="store.service?.editEnvironment(environment.id)"
        >
          <b>1</b><strong>{{ t('archive.message_51f3aca891b2') }}</strong><span>{{ environment.gateways.length }}{{ t('archive.message_0ebc410c386d') }}</span>
        </button><button
          type="button"
          data-action="p-service-add"
          data-vue-control="true"
          @click.stop="store.service?.addService()"
        >
          <b>2</b><strong>{{ t('archive.message_f5f29235d952') }}</strong><span>{{ services.length }}{{ t('archive.message_97827a9c3766') }}</span>
        </button><button
          type="button"
          data-action="domain-add"
          data-vue-control="true"
          @click.stop="store.service?.addDomain()"
        >
          <b>3</b><strong>{{ t('archive.message_2f0a5ba0f3c7') }}</strong><span>{{ t('archive.message_34097415cf7a') }}</span>
        </button>
      </div>
    </section>
    <DomainsList v-else :domains="domains" />
    <div class="ribbon mt16">
      <ViewContent :content="icon('info', 'sm')" />{{
        t('archive.message_286409d8c7ef', {
          v12: '',
          v13: gatewayNames || t('archive.message_1182c5454f11'),
        })
      }}
    </div>
  </template>
</template>

<style scoped>
.global-title { flex-wrap: wrap; align-items: flex-start; }
.global-title > div { flex: 1 1 260px; min-width: 0; }
.global-title .flex { flex-wrap: wrap; }
.global-title h1, .global-title p { overflow-wrap: anywhere; max-width: 100%; }
.global-title > button { max-width: 100%; white-space: normal; }
.breadcrumbs { flex-wrap: wrap; overflow-wrap: anywhere; }
.breadcrumbs button { min-width: 0; max-width: 100%; text-align: left; }
.ribbon { overflow-wrap: anywhere; }
</style>
