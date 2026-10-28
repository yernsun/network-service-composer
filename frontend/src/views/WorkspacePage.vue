<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import HAOverview from './HAOverview.vue'
import ActivityHub from './ActivityHub.vue'
import IPAccessControl from '../components/IPAccessControl.vue'
import ServiceBindings from '../components/ServiceBindings.vue'
import ServiceRestore from '../components/ServiceRestore.vue'
import ApplicationNetwork from '../components/ApplicationNetwork.vue'
import DockerResources from '../components/DockerResources.vue'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'
import ProjectsList from './ProjectsList.vue'
import EnvironmentsList from './EnvironmentsList.vue'
import ProjectOverview from './ProjectOverview.vue'
import EnvironmentOverview from './EnvironmentOverview.vue'
import DomainsListPage from './DomainsListPage.vue'
import ApplicationsList from './ApplicationsList.vue'
import ServicesList from './ServicesList.vue'
import ApplicationMarket from './ApplicationMarket.vue'
import VariablesPage from './VariablesPage.vue'
import CertificatesListPage from './CertificatesListPage.vue'
const store = useWorkspaceStore()
const route = useRoute()
const serviceId = computed(() =>
  String(
    route.params.service ||
      (route.params.section === 'services' ? route.params.resource || '' : ''),
  ),
)
const applicationId = computed(() =>
  String(
    route.params.application ||
      (route.params.section === 'applications' ? route.params.resource || '' : ''),
  ),
)
const dockerResources = computed(
  () => Boolean(route.params.runtime) && route.query.tab === 'resources',
)
const environmentsPage = computed(() => /^\/projects\/[^/]+\/environments$/.test(route.path))
const projectPage = computed(() => /^\/projects\/[^/]+$/.test(route.path))
const environmentOverview = computed(() =>
  /^\/projects\/[^/]+\/environments\/[^/]+\/overview$/.test(route.path),
)
const domainsPage = computed(() =>
  /^\/projects\/[^/]+\/environments\/[^/]+\/domains$/.test(route.path),
)
const certificatesPage = computed(() =>
  /^\/projects\/[^/]+\/environments\/[^/]+\/certificates$/.test(route.path),
)
const applicationsPage = computed(() =>
  /^\/projects\/[^/]+\/environments\/[^/]+\/applications$/.test(route.path),
)
const servicesPage = computed(() =>
  /^\/projects\/[^/]+\/environments\/[^/]+\/services$/.test(route.path),
)
const marketPage = computed(() => route.path === '/market')
const variablesPage = computed(() =>
  /^\/projects\/[^/]+\/(?:variables|environments\/[^/]+\/variables)$/.test(route.path),
)
const content = computed(() => store.content('page'))
</script>
<template>
  <ActivityHub v-if="route.path === '/operations/activity'" /><HAOverview v-else-if="route.path === '/infrastructure/ha'" /><DockerResources
    v-else-if="dockerResources"
    :key="String(route.params.runtime)"
    :runtime-id="String(route.params.runtime)"
  /><ProjectsList v-else-if="route.path === '/projects'" /><ProjectOverview
    v-else-if="projectPage"
  /><EnvironmentsList v-else-if="environmentsPage" /><EnvironmentOverview
    v-else-if="environmentOverview"
  /><DomainsListPage v-else-if="domainsPage" /><CertificatesListPage v-else-if="certificatesPage" /><ApplicationsList
    v-else-if="applicationsPage"
  /><ServicesList v-else-if="servicesPage" /><ApplicationMarket v-else-if="marketPage" /><VariablesPage v-else-if="variablesPage" /><ViewContent v-else :content="content" /><IPAccessControl /><ServiceBindings
    v-if="serviceId"
    :key="serviceId"
    :service-id="serviceId"
  /><ServiceRestore
    v-if="serviceId"
    :key="serviceId"
    :service-id="serviceId"
  /><ApplicationNetwork
    v-if="applicationId"
    :key="applicationId"
    :application-id="applicationId"
  />
</template>
