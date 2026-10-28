<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { serviceName } from '../data/service-references'
import { serviceLifecycle } from '../data/service-lifecycle'
import { serviceVersionSummary } from '../data/service-version-view'
import { serviceInstances } from '../data/service-instances'
import { useWorkspaceStore } from '../stores/workspace'
import type { Service } from '../types/domain'
import ViewContent from '../components/ViewContent.vue'

withDefaults(defineProps<{ services: Service[]; showScope?: boolean }>(), { showScope: true })
const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const events = computed(
  () => (state.value.runtimeEvents || []) as { service: string; category: string }[],
)

function status(service: Service) {
  return store.service?.serviceState(service.id) || { name: '', tone: '', key: '', detail: '' }
}
function icon(name: string) {
  return store.service?.iconView(name)
}
function workloadKind(service: Service) {
  if (service.workloadKind === 'job') return t('archive.message_3950bad5b197')
  if (service.workloadKind === 'global') return t('archive.message_bb1d2cdf0559')
  if (service.mode === 'container') return t('archive.message_95e86cd83827')
  if (service.mode === 'compose') return t('cluster.kind.compose')
  return 'Swarm Service'
}
function runtime(service: Service) {
  return state.value.runtimes.find((item) => item.id === service.runtimeId)
}
function currentInstances(service: Service) {
  return serviceInstances(state.value.instances, service, runtime(service))
}
function faultCount(service: Service) {
  return events.value.filter((event) => event.service === service.id && event.category === 'fault')
    .length
}
function projectName(service: Service) {
  return state.value.projects.find((item) => item.id === service.project)?.name || service.project
}
function environmentName(service: Service) {
  return state.value.environments.find((item) => item.id === service.env)?.name || service.env
}
</script>

<template>
  <div class="table-scroll">
    <table class="x-table">
      <thead>
        <tr>
          <th>{{ t('compose.services') }}</th>
          <th v-if="showScope">{{ t('archive.message_67cfab374da6') }}</th>
          <th>{{ t('archive.message_45bbec4829aa') }}</th>
          <th>{{ t('archive.message_083259ac4fb6') }}</th>
          <th>{{ t('archive.message_34199558565f') }}</th>
          <th>{{ t('prototype.runtime_records') }}</th>
          <th>{{ t('service.lifecycle') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="service in services" :key="service.id" :data-service-id="service.id">
          <td>
            <div class="flex">
              <ViewContent :content="icon('box')" /><button
                class="row-link x-name"
                type="button"
                data-action="x-service"
                data-vue-control="true"
                :data-id="service.id"
                @click.stop="store.service?.openService(service.id)"
              >
                {{ serviceName(state.services, service.id) }}
              </button>
            </div>
            <small>{{ workloadKind(service) }} ·
              {{
                t(service.managed ? 'archive.message_bd50041740c9' : 'archive.message_e654a5974688')
              }}</small>
          </td>
          <td v-if="showScope">
            {{ projectName(service) }}<small>{{ environmentName(service) }}</small>
          </td>
          <td>
            <button
              v-if="service.app"
              class="row-link"
              type="button"
              data-action="x-app"
              data-vue-control="true"
              :data-id="service.app"
              @click.stop="store.service?.openApplication(service.app)"
            >
              {{ state.apps.find((app) => app.id === service.app)?.name }}
            </button><span v-else class="muted">{{ t('archive.message_ec2435c76826') }}</span>
          </td>
          <td>
            {{ runtime(service)?.name || service.runtime
            }}<small>{{
              serviceVersionSummary(service, runtime(service)?.connection === 'online')
            }}</small>
          </td>
          <td>
            <span class="badge" :class="status(service).tone">{{ status(service).name }}</span><small>{{ status(service).detail }}</small>
          </td>
          <td>
            <button
              v-if="faultCount(service)"
              class="textbtn"
              type="button"
              data-action="x-service-events"
              data-vue-control="true"
              :data-id="service.id"
              @click.stop="store.service?.openService(service.id, 'events')"
            >
              {{ t('archive.message_aafb70b7aab2') }}{{ faultCount(service)
              }}{{ t('archive.message_f81526efce19') }}
            </button><span v-else class="muted">{{ t('archive.message_9ec522187dda') }}</span><small>{{
              currentInstances(service).some((item: { kind: string }) => item.kind === 'container')
                ? t('archive.message_db6c542ca496')
                : t('archive.message_c78704a5b8f9')
            }}</small>
          </td>
          <td class="service-lifecycle">
            {{ serviceLifecycle(service).primary
            }}<small>{{ serviceLifecycle(service).secondary }}</small>
          </td>
          <td>
            <button
              class="textbtn"
              type="button"
              data-action="x-service"
              data-vue-control="true"
              :data-id="service.id"
              @click.stop="store.service?.openService(service.id)"
            >
              {{ t('console.details') }}
            </button>
          </td>
        </tr>
        <tr v-if="!services.length">
          <td :colspan="showScope ? 8 : 7" class="x-empty">
            {{ t('archive.message_533245438e21') }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
