<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'
import DomainsList from './DomainsList.vue'
import DomainDraftForm from '../components/DomainDraftForm.vue'
import DNSProfilesPanel from '../components/DNSProfilesPanel.vue'
import { allPages } from '../data/pagination'
import { errorText, request, session } from '../shared/api/client'
import type { components } from '../shared/api/schema'

type Draft = components['schemas']['DomainDraft']

const route = useRoute()
const router = useRouter()
const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const projectId = computed(() => String(route.params.project || state.value.project))
const environmentId = computed(() => String(route.params.environment || state.value.env))
const availableEnvironments = computed(() => state.value.environments.filter(item => projectId.value === 'all' || item.project === projectId.value))
const drafts = ref<Draft[]>([]), loadingDrafts = ref(false), draftError = ref(''), draftNotice = ref(''), busyDraft = ref(false)
const editing = ref<Draft | null>(null), targetEnvironment = ref(''), showForm = ref(false), showProfiles = ref(false), choosingTarget = ref(false)
async function loadDrafts() {
  loadingDrafts.value = true; draftError.value = ''
  try {
    const ids = environmentId.value === 'all' ? availableEnvironments.value.map(item => item.id) : [environmentId.value]
    drafts.value = (await Promise.all(ids.map(id => allPages('/environments/{id}/domain-drafts', { id })))).flat() as Draft[]
  } catch (e) { draftError.value = errorText(e) } finally { loadingDrafts.value = false }
}
onMounted(() => { void loadDrafts() })
watch([projectId, environmentId], () => { showForm.value = false; choosingTarget.value = false; void loadDrafts() })
watch(() => route.query.create, value => {
  if (value !== '1') return
  addDraft()
  void router.replace({ path: route.path, query: { ...route.query, create: undefined } })
}, { immediate: true })
function addDraft() {
  editing.value = null
  targetEnvironment.value = environmentId.value === 'all' ? '' : environmentId.value
  choosingTarget.value = environmentId.value === 'all'
  showForm.value = !choosingTarget.value
}
function editDraft(draft: Draft) { editing.value = draft; targetEnvironment.value = draft.environment_id; showForm.value = true }
function savedDraft() { showForm.value = false; draftNotice.value = t('domainDraft.saved'); void loadDrafts() }
async function publishDraft(draft: Draft) {
  if (busyDraft.value) return
  busyDraft.value = true; draftError.value = ''; draftNotice.value = ''
  try {
    await request('post', '/domain-drafts/{id}/publish', { params: { path: { id: draft.id } }, headers: { 'If-Match': `"rev-${draft.revision}"` } })
    draftNotice.value = t('domainDraft.published'); await loadDrafts(); window.dispatchEvent(new Event('nsc:resources-changed'))
  } catch (e) { draftError.value = errorText(e) } finally { busyDraft.value = false }
}
async function deleteDraft(draft: Draft) {
  if (busyDraft.value || !window.confirm(t('domainDraft.deleteConfirm'))) return
  busyDraft.value = true; draftError.value = ''
  try {
    await request('delete', '/domain-drafts/{id}', { params: { path: { id: draft.id } }, headers: { 'If-Match': `"rev-${draft.revision}"` } })
    await loadDrafts()
  } catch (e) { draftError.value = errorText(e) } finally { busyDraft.value = false }
}
const domains = computed(() =>
  state.value.domains.filter(
    (domain) =>
      (projectId.value === 'all' || domain.project === projectId.value) &&
      (environmentId.value === 'all' || domain.env === environmentId.value || domain.environmentIds?.includes(environmentId.value)),
  ),
)
const environmentName = computed(() =>
  environmentId.value === 'all'
    ? t('workspace.all_environments')
    : state.value.environments.find((item) => item.id === environmentId.value)?.name ||
      environmentId.value,
)
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
</script>

<template>
  <div class="breadcrumbs">
    <span>{{ environmentName }}</span><span class="slash">/</span><span class="current">{{ t('archive.message_222952431147') }}</span>
  </div>
  <div class="global-title">
    <div>
      <h1>{{ t('archive.message_222952431147') }}</h1>
      <p>{{ t('archive.message_dd040c6c3e30') }}</p>
    </div>
    <button
      class="btn primary"
      type="button"
      data-action="domain-add"
      data-vue-control="true"
      :disabled="session.role === 'viewer'"
      @click.stop="addDraft"
    >
      <ViewContent :content="icon('plus')" />{{ t('archive.message_d28c9d6f5ce9') }}
    </button>
  </div>
  <div class="domain-draft-section">
    <div v-if="choosingTarget" class="card draft-scope-picker"><label>{{ t('domainDraft.selectEnvironment') }}<select v-model="targetEnvironment"><option value="" disabled>{{ t('bindingUI.select') }}</option><option v-for="item in availableEnvironments" :key="item.id" :value="item.id">{{ state.projects.find(project => project.id === item.project)?.name }} / {{ item.name }}</option></select></label><button type="button" class="btn primary" :disabled="!targetEnvironment" @click="choosingTarget = false; showForm = true">{{ t('domainDraft.continue') }}</button><button type="button" class="btn" @click="choosingTarget = false">{{ t('console.cancel') }}</button></div>
    <div class="flex"><h2>{{ t('domainDraft.drafts') }}</h2><span class="spacer"></span><button type="button" class="btn" @click="showProfiles = !showProfiles">{{ t('dnsProfiles.manage') }}</button></div>
    <DNSProfilesPanel v-if="showProfiles" />
    <p v-if="draftError" class="callout red" role="alert">{{ draftError }}</p><p v-if="draftNotice" class="callout blue" role="status">{{ draftNotice }}</p>
    <p v-if="loadingDrafts">{{ t('bindingUI.loading') }}</p>
    <article v-for="draft in drafts.filter(item => item.status === 'draft')" :key="draft.id" class="card draft-card">
      <div class="flex"><strong>{{ draft.domain.host }}</strong><span class="spacer"></span><span>{{ availableEnvironments.find(item => item.id === draft.environment_id)?.name }}</span></div>
      <p>{{ draft.service_id ? state.services.find(item => item.id === draft.service_id)?.name || draft.service_id : t('domainDraft.noService') }} · {{ draft.domain.tls.mode === 'managed' ? draft.domain.tls.acme_challenge : draft.domain.tls.mode }}</p>
      <div class="flex"><button type="button" class="btn sm" :disabled="busyDraft || session.role === 'viewer'" @click="editDraft(draft)">{{ t('bindingUI.edit') }}</button><button type="button" class="btn sm primary" :disabled="busyDraft || !draft.service_id || session.role === 'viewer'" @click="publishDraft(draft)">{{ t('domainDraft.publish') }}</button><button type="button" class="btn sm" :disabled="busyDraft || session.role === 'viewer'" @click="deleteDraft(draft)">{{ t('bindingUI.remove') }}</button></div>
    </article>
    <DomainDraftForm v-if="showForm && targetEnvironment" :key="editing?.id || targetEnvironment" :environment-id="targetEnvironment" :draft="editing" @saved="savedDraft" @close="showForm = false" />
  </div>
  <DomainsList :domains="domains" :show-project="projectId === 'all'" />
  <div class="statusbar">
    <span>{{ t('archive.message_09e55d2e0b87') }}</span><span>{{ t('archive.message_f21ba3b57961') }}</span>
  </div>
</template>

<style scoped>
.domain-draft-section { margin: 24px 0; }.domain-draft-section h2 { margin: 0; }.draft-card { padding: 16px; margin: 10px 0; }.draft-card .flex { gap: 10px; }.draft-scope-picker { display: flex; align-items: end; gap: 10px; padding: 16px; }.draft-scope-picker label { display: grid; gap: 5px; flex: 1; }.draft-scope-picker select { width: 100%; }
</style>
