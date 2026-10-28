<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'
import EnvironmentCard from './EnvironmentCard.vue'

const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const project = computed(() => state.value.projects.find((item) => item.id === state.value.project))
const environments = computed(() =>
  state.value.environments.filter((item) => item.project === state.value.project),
)
const domains = computed(() =>
  state.value.domains.filter((item) => item.project === state.value.project),
)
const services = computed(() =>
  state.value.services.filter(
    (item) => item.project === state.value.project && item.status !== 'REMOVED',
  ),
)
const metrics = computed(() => [
  {
    value: environments.value.length,
    label: t('prototype.environment_group'),
    note: t(
      environments.value.length === 1
        ? 'archive.message_fffea17422ae'
        : 'archive.message_80ce47b09b4f',
    ),
    icon: 'layers',
    page: 'environments',
  },
  {
    value: domains.value.length,
    label: t('archive.message_62f345897b3a'),
    note: t('archive.message_c5369faf24ef'),
    icon: 'globe',
    page: 'domains',
  },
  {
    value: services.value.length,
    label: t('archive.message_16356b120797'),
    note: t('archive.message_428fbf538000'),
    icon: 'box',
    page: 'services',
  },
  {
    value: domains.value.filter((domain) => domain.draft).length,
    label: t('prototype.pending_drafts'),
    note: t('archive.message_7908539937fa'),
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
  <template v-if="project">
    <div class="breadcrumbs">
      <button
        type="button"
        data-action="nav"
        data-page="projects"
        @click.stop="navigate('projects')"
      >
        {{ t('prototype.all_projects') }}
      </button><span class="current">{{ project.name }}</span>
    </div>
    <div class="project-hero">
      <div class="project-large-symbol"><ViewContent :content="icon('folder')" /></div>
      <div class="spacer">
        <div class="flex wrap">
          <h1>{{ project.name }}</h1>
          <span class="badge">{{ project.key }}</span>
        </div>
        <p>{{ project.description || t('prototype.project_overview_help') }}</p>
      </div>
      <button
        class="btn"
        type="button"
        data-action="p-copy"
        data-vue-control="true"
        :disabled="environments.length < 2"
        :title="environments.length < 2 ? t('archive.message_60045a5b5adf') : undefined"
        @click.stop="store.service?.copyProjectConfiguration()"
      >
        <ViewContent :content="icon('copy')" />{{ t('prototype.copy_configuration') }}
      </button>
      <button
        class="btn primary"
        type="button"
        data-action="p-env-add"
        data-vue-control="true"
        @click.stop="store.service?.createEnvironment()"
      >
        <ViewContent :content="icon('plus')" />{{ t('archive.message_a2a1229f025d') }}
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
        <span class="metric-info">{{ metric.note }}</span>
      </button>
    </div>
    <div class="section-heading">
      <div>
        <h2>{{ t('prototype.project_environments') }}</h2>
        <p>
          {{
            t(
              environments.length === 1
                ? 'archive.message_3b01372c772f'
                : 'archive.message_8c65f07d5ab6',
            )
          }}
        </p>
      </div>
      <button
        class="textbtn"
        type="button"
        data-action="nav"
        data-page="environments"
        @click.stop="navigate('environments')"
      >
        {{ t('prototype.view_all_bindings') }}
      </button>
    </div>
    <div class="environment-grid">
      <EnvironmentCard
        v-for="environment in environments"
        :key="environment.id"
        :environment="environment"
      />
      <button
        v-if="environments.length === 1"
        class="add-environment-card"
        type="button"
        data-action="p-env-add"
        data-vue-control="true"
        @click.stop="store.service?.createEnvironment()"
      >
        <ViewContent :content="icon('plus')" />
        <h3>{{ t('prototype.add_environment_later') }}</h3>
        <p>{{ t('archive.message_864c14a17e0f') }}<br />{{ t('archive.message_2424c4c47d54') }}</p>
      </button>
    </div>
    <div class="project-bottom-grid">
      <section class="card">
        <div class="card-head">
          <div>
            <h3>{{ t('archive.message_33a3ccb27972') }}</h3>
            <p>{{ t('archive.message_ef683b384378') }}</p>
          </div>
        </div>
        <div class="card-body">
          <div class="resource-model">
            <div>
              <ViewContent :content="icon('folder')" /><b>{{ project.name }}</b><small>{{ t('prototype.project_group') }}</small>
            </div>
            <span>→</span>
            <div>
              <ViewContent :content="icon('layers')" /><b>{{ t('prototype.environment_group') }}</b><small>{{ t('archive.message_d366c2e2a4c1') }}</small>
            </div>
            <span>→</span>
            <div>
              <ViewContent :content="icon('globe')" /><b>{{ t('archive.message_da9cd1113c9c') }}</b><small>{{ t('archive.message_973c936a655c') }}</small>
            </div>
          </div>
          <p class="footer-tip mt16">{{ t('archive.message_7bd1f14b5d81') }}</p>
        </div>
      </section>
      <section class="card">
        <div class="card-head">
          <div>
            <h3>{{ t('archive.message_e0782ab38fa3') }}</h3>
            <p>{{ t('archive.message_6d3d4d3458af') }}</p>
          </div>
        </div>
        <div class="quick-project-actions">
          <button
            type="button"
            data-action="p-env-domain-add"
            data-vue-control="true"
            @click.stop="store.service?.addDomainFromEnvironments()"
          >
            <ViewContent :content="icon('globe')" />
            <div>
              <strong>{{ t('archive.message_e5802a74568a') }}</strong><span>{{ t('archive.message_ec1328d22da4') }}</span>
            </div>
            <ViewContent :content="icon('right', 'sm')" />
          </button><button
            type="button"
            data-action="p-copy"
            data-vue-control="true"
            :disabled="environments.length < 2"
            :title="environments.length < 2 ? t('archive.message_60045a5b5adf') : undefined"
            @click.stop="store.service?.copyProjectConfiguration()"
          >
            <ViewContent :content="icon('copy')" />
            <div>
              <strong>{{ t('archive.message_3c8814efef7b') }}</strong><span>{{ t('archive.message_57da16dfb678') }}</span>
            </div>
            <ViewContent :content="icon('right', 'sm')" />
          </button>
        </div>
      </section>
    </div>
  </template>
</template>
