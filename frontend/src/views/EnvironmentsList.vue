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
function gatewayNames(ids: string[]) {
  return ids
    .map((id) => state.value.gateways.find((gateway) => gateway.id === id)?.name || id)
    .join(' / ')
}
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function openDomain(id: string) {
  store.service?.openDomain(id)
}
</script>

<template>
  <div class="breadcrumbs">
    <button
      type="button"
      data-action="nav"
      data-page="project"
      @click.stop="store.service?.navigatePage('project')"
    >
      {{ project?.name }}
    </button><span class="current">{{ t('prototype.environments_domains') }}</span>
  </div>
  <div class="global-title">
    <div>
      <h1>{{ t('prototype.environments_domains') }}</h1>
      <p>{{ t('archive.message_20d481ad6317') }}</p>
    </div>
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
  <div class="environment-grid">
    <EnvironmentCard
      v-for="environment in environments"
      :key="environment.id"
      :environment="environment"
    />
  </div>
  <section class="card mt16">
    <div class="card-head">
      <div>
        <h3>{{ t('archive.message_b084ba9c31ec') }}</h3>
        <p>{{ t('archive.message_ad9a760f6b1e') }}</p>
      </div>
      <button
        class="btn sm"
        type="button"
        data-action="p-env-domain-add"
        data-vue-control="true"
        @click.stop="store.service?.addDomainFromEnvironments()"
      >
        {{ t('bindingUI.add') }}
      </button>
    </div>
    <div class="table-scroll">
      <table class="table">
        <thead>
          <tr>
            <th>{{ t('bindingUI.host') }}</th>
            <th>{{ t('archive.message_7d3137ac35ca') }}</th>
            <th>{{ t('archive.message_63d816a0b67e') }}</th>
            <th>{{ t('archive.message_0abfd282b40d') }}</th>
            <th>{{ t('delivery.status') }}</th>
            <th>{{ t('community.action') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="domain in domains" :key="domain.id">
            <td class="mono">{{ domain.name }}</td>
            <td>
              <span
                class="badge"
                :class="
                  state.environments.find((environment) => environment.id === domain.env)?.kind ===
                    'production'
                    ? 'green'
                    : 'blue'
                "
              >{{
                state.environments.find((environment) => environment.id === domain.env)?.name
              }}</span>
            </td>
            <td>{{ gatewayNames(domain.gateways) }}</td>
            <td>
              {{
                (domain.draft?.config || domain.active).mode === 'native'
                  ? t('archive.message_debe95b98471')
                  : t('archive.message_3465da773f5a')
              }}
            </td>
            <td><ViewContent :content="store.service?.domainStatusView(domain.id)" /></td>
            <td>
              <button
                class="textbtn"
                type="button"
                data-action="open-domain"
                :data-id="domain.id"
                @click.stop="openDomain(domain.id)"
              >
                {{ t('archive.message_c771248e511f') }}
              </button>
            </td>
          </tr>
          <tr v-if="!domains.length">
            <td colspan="6">
              <div class="empty">{{ t('archive.message_b85dbee92e5d') }}</div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
