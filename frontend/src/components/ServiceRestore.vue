<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, errorText, session } from '../shared/api/client'
import { randomID } from '../shared/browser/random'
import { i18n } from '../shared/i18n'
import type { components } from '../shared/api/schema'
import StatusBadge from './StatusBadge.vue'

const props = defineProps<{ serviceId: string }>()
type Service = components['schemas']['Service']
type Revision = components['schemas']['ApplicationRevision']
type Review = components['schemas']['ApplicationRestoreReview']
const serviceQuery = useQuery({ queryKey: computed(() => ['restore-service', props.serviceId]), queryFn: async ({ signal }) =>
  (await request('get', '/services/{id}', { params: { path: { id: props.serviceId } }, signal })).data as Service })
const service = serviceQuery.data
const revisionsQuery = useQuery({ queryKey: computed(() => ['restore-revisions', service.value?.application_id]), enabled: computed(() => !!service.value?.application_id),
  queryFn: async ({ signal }) => (await request('get', '/applications/{id}/revisions', { params: { path: { id: service.value!.application_id! } }, signal })).data as Revision[] })
const revisions = computed(() => [...(revisionsQuery.data.value || [])].reverse())
const selected = ref('')
const review = ref<Review>()
const acknowledged = ref(false)
const busy = ref(false)
const error = ref('')
const queued = ref('')
const key = ref('')
const deleteKey = ref('')
const deleteQueued = ref('')
const writable = computed(() => session.value.role === 'administrator' && !!service.value?.managed && !service.value?.system && !!service.value?.application_id)
const canDelete = computed(() => writable.value && service.value?.status !== 'REMOVED' && !service.value?.removed_at)
const t = (name: string) => i18n.global.t(name)
watch(() => props.serviceId, () => { selected.value = ''; review.value = undefined; queued.value = ''; deleteQueued.value = ''; deleteKey.value = ''; error.value = '' })
async function deleteService() {
  if (!canDelete.value || busy.value || !service.value || !window.confirm(t('serviceDelete.confirm'))) return
  busy.value = true; error.value = ''
  try {
    deleteKey.value ||= randomID()
    const result = await request('post', '/services/{id}/delete', {
      params: { path: { id: props.serviceId } },
      headers: { 'If-Match': `"rev-${service.value.revision}"`, 'Idempotency-Key': deleteKey.value },
      body: { acknowledge_data_boundary: true },
    })
    deleteQueued.value = (result.data as components['schemas']['DeploymentOperation']).id
    window.dispatchEvent(new Event('nsc:resources-changed'))
  } catch (failure) { error.value = errorText(failure) }
  finally { busy.value = false }
}
async function preview() {
  review.value = undefined; acknowledged.value = false; queued.value = ''; error.value = ''; key.value = ''
  if (!selected.value || !writable.value) return
  busy.value = true
  try {
    review.value = (await request('post', '/services/{id}/revisions/{revisionID}/restore-preview', {
      params: { path: { id: props.serviceId, revisionID: selected.value } },
    })).data as Review
  } catch (failure) { error.value = errorText(failure) }
  finally { busy.value = false }
}
async function restore() {
  if (!review.value || !acknowledged.value || busy.value || !writable.value) return
  busy.value = true; error.value = ''
  try {
    key.value ||= randomID()
    const result = await request('post', '/services/{id}/restore', {
      params: { path: { id: props.serviceId } },
      headers: { 'If-Match': `"rev-${review.value.base_revision}"`, 'Idempotency-Key': key.value },
      body: { review_id: review.value.id, acknowledge_data_boundary: true },
    })
    queued.value = (result.data as components['schemas']['DeploymentOperation']).id
    window.dispatchEvent(new Event('nsc:resources-changed'))
  } catch (failure) { error.value = errorText(failure) }
  finally { busy.value = false }
}
</script>

<template>
  <section v-if="writable" class="card x-pad mt16 service-restore">
    <h3>{{ t('serviceRestore.title') }}</h3>
    <p class="hint">{{ t('serviceRestore.description') }}</p>
    <label class="field"><span class="field-label">{{ t('serviceRestore.revision') }}</span>
      <select v-model="selected" :disabled="busy" @change="preview">
        <option value="">{{ t('serviceRestore.choose') }}</option>
        <option v-for="revision in revisions" :key="revision.id" :value="revision.id">r{{ revision.sequence }}</option>
      </select>
    </label>
    <template v-if="review">
      <p class="y-note amber mt16">{{ t('serviceRestore.warning') }}</p>
      <pre class="y-mini-code">{{ review.preview?.resolved_yaml }}</pre>
      <label class="x-check-label"><input v-model="acknowledged" type="checkbox">{{ t('serviceRestore.acknowledge') }}</label>
      <button class="btn primary" type="button" :disabled="!acknowledged || busy || !!queued" @click="restore">{{ t('serviceRestore.publish') }}</button>
    </template>
    <p v-if="error" class="y-error" role="alert">{{ error }}</p>
    <p v-if="queued" class="y-note" role="status"><StatusBadge status="PENDING" context="operation" :label="t('serviceRestore.queued')" /> · {{ queued }}</p>
    <template v-if="canDelete || deleteQueued">
      <hr>
      <h3>{{ t('serviceDelete.title') }}</h3>
      <p class="hint">{{ t('serviceDelete.description') }}</p>
      <button v-if="canDelete" class="btn danger" type="button" :disabled="busy || !!deleteQueued" @click="deleteService">{{ t('serviceDelete.action') }}</button>
      <p v-if="deleteQueued" class="y-note" role="status"><StatusBadge status="PENDING" context="operation" :label="t('serviceDelete.queued')" /> · {{ deleteQueued }}</p>
    </template>
  </section>
</template>
