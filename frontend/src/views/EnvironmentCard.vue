<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import type { Environment } from '../types/domain'
import ViewContent from '../components/ViewContent.vue'

const props = defineProps<{ environment: Environment }>()
const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const domains = computed(() =>
  state.value.domains.filter(
    (domain) => domain.project === props.environment.project && domain.env === props.environment.id,
  ),
)
const services = computed(() =>
  state.value.services.filter(
    (service) =>
      service.project === props.environment.project &&
      service.env === props.environment.id &&
      service.status !== 'REMOVED',
  ),
)
const gatewayNames = computed(() =>
  props.environment.gateways
    .map((id) => state.value.gateways.find((gateway) => gateway.id === id)?.name || id)
    .join(' / '),
)
const kindLabels: Record<string, string> = {
  production: 'archive.message_76ab7d3b41f2',
  development: 'archive.message_9ec428625fee',
  testing: 'archive.message_6aa8f49cc992',
  sandbox: 'archive.message_2a8ce2b15d65',
  custom: 'archive.message_4eafa9e925b3',
}
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function openEnvironment() {
  store.service?.selectContextEnvironment(props.environment.id)
}
function openDomain(id: string) {
  store.service?.openDomain(id)
}
</script>

<template>
  <article
    class="card environment-card actionable-card"
    :class="{ production: environment.kind === 'production' }"
    role="link"
    tabindex="0"
    data-action="p-env"
    data-vue-control="true"
    :data-id="environment.id"
    @click.stop="openEnvironment"
    @keydown.enter.prevent="openEnvironment"
    @keydown.space.prevent="openEnvironment"
  >
    <div class="environment-head">
      <div class="environment-symbol" :class="environment.kind">
        <ViewContent
          :content="
            icon(
              environment.kind === 'production'
                ? 'shield'
                : environment.kind === 'sandbox'
                  ? 'box'
                  : 'layers',
            )
          "
        />
      </div>
      <div class="spacer">
        <h3>{{ environment.name }}</h3>
        <p>{{ environment.slug }}</p>
      </div>
      <span class="badge" :class="environment.kind === 'production' ? 'green' : 'blue'">{{
        t(kindLabels[environment.kind] || 'archive.message_4eafa9e925b3')
      }}</span>
    </div>
    <div class="domain-links">
      <button
        v-for="domain in domains.slice(0, 3)"
        :key="domain.id"
        type="button"
        data-action="open-domain"
        :data-id="domain.id"
        @click.stop="openDomain(domain.id)"
      >
        <ViewContent :content="icon('globe', 'sm')" /><span>{{ domain.name }}</span><ViewContent :content="icon('right', 'sm')" />
      </button>
      <div v-if="!domains.length" class="empty-domain">{{ t('archive.message_dfbd23b5868c') }}</div>
      <span v-if="domains.length > 3" class="hint">{{
        t('archive.message_28b5adb7ecb0', { v0: domains.length - 3 })
      }}</span>
    </div>
    <div class="env-card-metrics">
      <span><b>{{ domains.length }}</b>{{ t('archive.message_f06739c149c6') }}</span><span><b>{{ services.length }}</b>{{ t('archive.message_109ecd0e044d') }}</span><span><b>{{ domains.filter((domain) => domain.draft).length }}</b>{{ t('archive.message_51d8ce3cf644') }}</span>
    </div>
    <div class="env-target">
      <ViewContent :content="icon('server', 'sm')" /><span>{{
        environment.gateways.length ? gatewayNames : t('archive.message_527783f4eb2f')
      }}</span>
    </div>
    <div class="card-foot">
      <button
        class="textbtn"
        type="button"
        data-action="p-env-settings"
        data-vue-control="true"
        :data-id="environment.id"
        @click.stop="store.service?.editEnvironment(environment.id)"
      >
        {{ t('archive.message_161ff2fabefe', { v14: '' }) }}
      </button><button
        class="btn sm"
        type="button"
        data-action="p-env"
        data-vue-control="true"
        :data-id="environment.id"
        @click.stop="openEnvironment"
      >
        {{ t('prototype.enter_environment') }}
      </button>
    </div>
  </article>
</template>
