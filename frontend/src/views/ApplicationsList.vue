<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { translate as t } from '../i18n'
import { serviceName } from '../data/service-references'
import { serviceInstances } from '../data/service-instances'
import { useWorkspaceStore } from '../stores/workspace'
import type { Application, Service } from '../types/domain'
import ViewContent from '../components/ViewContent.vue'

const store = useWorkspaceStore()
const route = useRoute()
const state = computed(() => ({ ...store.state() }))
const projectId = computed(() => String(route.params.project || state.value.project))
const environmentId = computed(() => String(route.params.environment || state.value.env))
const allEnvironments = computed(() => projectId.value === 'all' || environmentId.value === 'all')
const apps = computed(() => state.value.apps.filter(inScope))
const services = computed(() =>
  state.value.services.filter((service) => service.app && inScope(service)),
)
const drafts = computed(() => ((state.value.appDrafts || []) as Application[]).filter(inScope))
const running = computed(() =>
  services.value.reduce((count, service) => count + runningCount(service), 0),
)
const issues = computed(() => apps.value.filter((app) => appStatus(app).tone === 'amber').length)
const metrics = computed(() => [
  {
    value: apps.value.length,
    label: t('archive.message_63c73c4730f4'),
    note: scopeLabel.value,
    page: 'applications',
    health: '',
  },
  {
    value: services.value.length,
    label: t('archive.message_79b491ade724'),
    note: t('archive.message_4747c4dd0fdf'),
    page: 'services',
    health: '',
  },
  {
    value: running.value,
    label: t('cluster.running'),
    note: t('archive.message_7450121a1072'),
    page: 'services',
    health: 'running',
  },
  {
    value: issues.value,
    label: t('community.active_issue'),
    note: t('archive.message_f64ca4539852'),
    page: 'services',
    health: 'issue',
  },
])
const scopeLabel = computed(() => {
  const project =
    projectId.value === 'all'
      ? t('workspace.all_projects')
      : state.value.projects.find((item) => item.id === projectId.value)?.name || projectId.value
  const environment = allEnvironments.value
    ? t('workspace.all_environments')
    : state.value.environments.find((item) => item.id === environmentId.value)?.name ||
      environmentId.value
  return `${project} / ${environment}`
})

function inScope(item: { project: string; env: string; status?: string }) {
  return (
    item.status !== 'REMOVED' &&
    (projectId.value === 'all' || item.project === projectId.value) &&
    (allEnvironments.value || item.env === environmentId.value)
  )
}
function appServices(app: Application) {
  return state.value.services.filter(
    (service) => service.app === app.id && service.status !== 'REMOVED',
  )
}
function runningCount(service: Service) {
  const runtime = state.value.runtimes.find((item) => item.id === service.runtimeId)
  return serviceInstances(state.value.instances, service, runtime).filter(
    (item: { state: string }) => item.state === 'RUNNING',
  ).length
}
function routeCount(app: Application) {
  const ids = new Set(appServices(app).map((service) => service.id))
  return state.value.domains.filter((domain) =>
    (domain.draft?.config || domain.active).routes.some((rule) => ids.has(rule.service)),
  ).length
}
function appStatus(app: Application) {
  return store.service?.applicationState(app.id) || { name: '', tone: '' }
}
function runtimeName(app: Application) {
  return state.value.runtimes.find((runtime) => runtime.id === app.runtime)?.name || ''
}
function environmentName(app: Application) {
  const name = state.value.environments.find((item) => item.id === app.env)?.name || app.env
  return projectId.value === 'all'
    ? `${state.value.projects.find((item) => item.id === app.project)?.name || app.project} / ${name}`
    : name
}
function modeLabel(mode: string) {
  const labels: Record<string, string> = {
    stack: 'archive.message_0010dedd5bfc',
    compose: 'archive.message_018576a0d8d9',
    container: 'archive.message_6d5e3e86d2c0',
    swarmservice: 'archive.message_50fd33e93b06',
    external: 'archive.message_27b97fffee25',
  }
  return labels[mode] ? t(labels[mode]) : mode
}
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function navigate(page: string, health = '') {
  if (page === 'services') store.service?.navigateServices(health)
  else store.service?.navigatePage(page)
}
function open(app: Application) {
  store.service?.openApplication(app.id)
}
</script>

<template>
  <div class="breadcrumbs">
    <span class="current">{{ t('archive.message_63c73c4730f4') }}</span>
  </div>
  <div class="x-head">
    <div>
      <div class="x-tagline">APPLICATIONS</div>
      <h1>{{ t('archive.message_63c73c4730f4') }}</h1>
      <p>{{ t('archive.message_a89c4c939f74') }}</p>
    </div>
    <div class="hero-actions">
      <button
        class="btn"
        type="button"
        data-action="nav"
        data-page="market"
        @click.stop="navigate('market')"
      >
        <ViewContent :content="icon('grid')" />{{ t('archive.message_bf198256f2ac') }}
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
  <div class="x-stats">
    <button
      v-for="metric in metrics"
      :key="metric.label"
      class="x-stat actionable-card"
      type="button"
      @click.stop="navigate(metric.page, metric.health)"
    >
      <label>{{ metric.label }}</label><strong>{{ metric.value }}</strong><small>{{ metric.note }}</small>
    </button>
  </div>
  <div v-if="drafts.length" class="callout blue mb16">
    <ViewContent :content="icon('edit')" />
    <div>
      {{
        t('archive.message_3b869505d94f', {
          v1: drafts.length,
          v2: t('archive.message_ff5cbaf2d08d'),
        })
      }}
    </div>
    <button
      class="textbtn"
      type="button"
      data-action="x-resume-wizard"
      data-vue-control="true"
      @click.stop="store.service?.resumeApplicationDraft()"
    >
      {{ t('archive.message_ff5cbaf2d08d') }}
    </button>
  </div>
  <div class="x-grid">
    <article
      v-for="app in apps"
      :key="app.id"
      class="card x-template actionable-card"
      role="link"
      tabindex="0"
      @click="open(app)"
      @keydown.enter.prevent="open(app)"
      @keydown.space.prevent="open(app)"
    >
      <div class="flex">
        <div class="x-appicon" :class="{ blue: app.mode === 'compose' }">
          <ViewContent :content="icon('box', 'lg')" />
        </div>
        <div class="spacer">
          <h2>{{ app.name }}</h2>
          <span
            v-if="allEnvironments"
            class="badge"
            :class="
              state.environments.find((item) => item.id === app.env)?.kind === 'production'
                ? 'green'
                : 'blue'
            "
          >{{ environmentName(app) }}</span>
          <div class="x-hint-inline">{{ app.key }} · r{{ app.system ? '—' : app.version }}</div>
        </div>
        <span class="badge" :class="appStatus(app).tone">{{ appStatus(app).name }}</span>
      </div>
      <p class="desc">
        {{ modeLabel(app.mode) }}<br />{{ runtimeName(app) }} ·
        {{ t(app.managed ? 'archive.message_5409a6d1a936' : 'archive.message_028e66d9d4b9') }}
      </p>
      <div class="flex wrap">
        <span
          v-for="service in appServices(app).slice(0, 4)"
          :key="service.id"
          class="badge"
          :class="store.service?.serviceState(service.id)?.key === 'issue' ? 'amber' : ''"
        >{{ serviceName(state.services, service.id) }}</span>
      </div>
      <div class="x-count-row">
        <span><strong>{{ appServices(app).length }}</strong>{{ t('compose.services') }}</span><span><strong>{{
          appServices(app).reduce((count, service) => count + runningCount(service), 0)
        }}</strong>{{ t('cluster.running') }}</span><span><strong>{{ routeCount(app) }}</strong>{{ t('bindingUI.host') }}</span>
      </div>
      <div class="x-template-foot">
        <span>{{
          t(app.managed ? 'archive.message_54afafe42f30' : 'archive.message_242d7f7ffc53')
        }}</span><button
          class="btn sm"
          type="button"
          data-action="x-app"
          data-vue-control="true"
          :data-id="app.id"
          @click.stop="open(app)"
        >
          {{ t('archive.message_8924b9035a5d') }}<ViewContent :content="icon('right')" />
        </button>
      </div>
    </article>
    <section v-if="!apps.length" class="card x-empty">
      <div class="x-appicon" style="margin: auto"><ViewContent :content="icon('box')" /></div>
      <h3>
        {{ t(allEnvironments ? 'archive.message_05b29782b4a8' : 'archive.message_b52461f6e013') }}
      </h3>
      <p>{{ t('archive.message_b04373e1b633') }}</p>
      <button
        class="btn primary"
        type="button"
        @click.stop="store.service?.beginApplicationInstall()"
      >
        <ViewContent :content="icon('plus')" />{{ t('archive.message_153841e57e59') }}
      </button>
    </section>
  </div>
  <div class="x-info-note mt16">{{ t('archive.message_e91ab52d3432') }}</div>
  <div class="x-foot">{{ t('archive.message_04f70a4dbc01') }}</div>
</template>
