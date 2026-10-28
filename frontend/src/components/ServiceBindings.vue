<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { ApiRequestError, errorText, session } from '../shared/api/client'
import { translate as t } from '../i18n'
import { bindingInput, bindingWriter, certificateCovers, domainInput, readBindings, type Binding, type BindingForm } from '../data/service-bindings'
import DomainDraftForm from './DomainDraftForm.vue'
import DNSProfilesPanel from './DNSProfilesPanel.vue'
import StatusBadge from './StatusBadge.vue'

const props = defineProps<{ serviceId: string }>()
const query = useQuery({ queryKey: computed(() => ['service-bindings', props.serviceId]), queryFn: ({ signal }) => readBindings(props.serviceId, signal), refetchInterval: 5000 })
const data = query.data
const editing = ref<Binding>(), opened = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const certificatePEM = ref(''), privateKeyPEM = ref(''), importing = ref(false)
const draftOpen = ref(false), profilesOpen = ref(false)
const form = reactive<BindingForm>({ domainID: '', host: '', path: '/', port: 0, protocol: 'http', serverName: '', http3: false, tls: 'http-01', provider: '', certificateID: '', redirect: false })
const write = bindingWriter()
const writable = computed(() => session.value.role === 'administrator' && !data.value?.service.system && !!data.value?.service.managed && !!data.value?.management)
const certificates = computed(() => data.value?.certificates.filter(c => certificateCovers(c, form.host)) || [])
let baseline = ''
const dirty = computed(() => opened.value && (JSON.stringify(form) !== baseline || !!certificatePEM.value || !!privateKeyPEM.value))
onBeforeRouteLeave(() => !dirty.value || window.confirm(t('bindingUI.discard')))
onBeforeRouteUpdate(() => !dirty.value || window.confirm(t('bindingUI.discard')))
watch(() => props.serviceId, () => { opened.value = false; draftOpen.value = false; editing.value = undefined; certificatePEM.value = ''; privateKeyPEM.value = ''; error.value = '' })
function edit(binding?: Binding) {
  if (dirty.value && !window.confirm(t('bindingUI.discard'))) return
  editing.value = binding
  const domain = binding?.domain, route = binding?.route
  Object.assign(form, { domainID: '', host: domain?.host || '', path: route?.path || '/', port: route?.backend.port || data.value?.service.target_port_override || data.value?.service.discovered_port || 0,
    protocol: route?.backend.protocol || 'http', serverName: route?.backend.server_name || '',
    http3: domain?.http3_enabled || false, tls: domain?.tls.mode === 'custom' ? 'custom' : domain?.tls.acme_challenge || 'http-01', provider: domain?.tls.dns_provider_ref || '', certificateID: domain?.tls.certificate_id || '', redirect: domain?.redirect_http_to_https || false })
  baseline = JSON.stringify(form); error.value = ''; notice.value = ''; certificatePEM.value = ''; privateKeyPEM.value = ''; opened.value = true
}
function close() {
  if (busy.value || dirty.value && !window.confirm(t('bindingUI.discard'))) return
  opened.value = false; certificatePEM.value = ''; privateKeyPEM.value = ''
}
function addExisting() {
  const first = data.value?.domains[0]
  if (!first) return
  edit(); form.domainID = first.id; baseline = JSON.stringify(form)
}
async function attempt(work: () => Promise<void>) {
  if (busy.value || !writable.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await work(); await query.refetch(); window.dispatchEvent(new Event('nsc:resources-changed')) } catch (e) { error.value = errorText(e); if (e instanceof ApiRequestError) { await nextTick(); const name = e.fields[0]?.pointer.split('/').at(-1); if (name) document.querySelector<HTMLElement>(`[data-testid=service-bindings] [name="${CSS.escape(name)}"]`)?.focus() } } finally { busy.value = false }
}
async function save() {
  await attempt(async () => {
    const existing = editing.value
    if (existing) {
      await write('patch', '/services/{id}/domain-bindings/{bindingID}', { id: props.serviceId, bindingID: existing.id },
        { domain_revision: existing.domain.revision, domain: domainInput(form), route: bindingInput(form).route }, existing.route.revision)
    } else if (form.domainID) {
      await write('post', '/services/{id}/domain-bindings', { id: props.serviceId }, bindingInput(form))
    } else {
      throw new Error(t('domainDraft.saveFirst'))
    }
    opened.value = false; notice.value = t('bindingUI.submitted')
  })
}
async function remove(binding: Binding) {
  if (!window.confirm(t('bindingUI.removeConfirm'))) return
  await attempt(async () => {
    await write('delete', '/services/{id}/domain-bindings/{bindingID}', { id: props.serviceId, bindingID: binding.id }, undefined, binding.route.revision)
    notice.value = t('bindingUI.submitted')
  })
}
async function retry(binding: Binding) {
 await attempt(async () => {
  await write('post', '/services/{id}/domain-bindings/{bindingID}/retry', { id: props.serviceId, bindingID: binding.id }, {}, binding.route.revision)
  notice.value = t('bindingUI.submitted')
 })
}
function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value) { event.preventDefault(); event.returnValue = '' } }
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
async function importCertificate() {
  await attempt(async () => {
    const result = await write('post', '/certificates', {}, { pool_id: data.value!.runtime.pool_id, certificate_pem: certificatePEM.value, private_key_pem: privateKeyPEM.value })
    const certificate = result.data as { id: string }
    form.certificateID = certificate.id; certificatePEM.value = ''; privateKeyPEM.value = ''; importing.value = false
  })
}
</script>

<template>
  <section class="card binding-panel" data-testid="service-bindings">
    <header class="flex"><h2>{{ t('bindingUI.title') }}</h2><span class="spacer"></span><button class="btn" :disabled="!writable || busy || !data?.domains.length" @click="addExisting">{{ t('domainDraft.bindExisting') }}</button><button class="btn primary" :disabled="!writable || busy || !data?.service.environment?.id" @click="draftOpen = true">{{ t('bindingUI.add') }}</button></header>
    <p class="hint">{{ t('bindingUI.networkHint') }}</p>
    <p v-if="data && !data.service.managed" class="callout amber">{{ t('bindingUI.adopt') }}</p>
    <p v-else-if="data && !data.management" class="callout amber">{{ t('bindingUI.management') }}</p>
    <p v-if="query.isPending.value">{{ t('bindingUI.loading') }}</p>
    <p v-if="error || query.error.value" role="alert" class="callout red">{{ error || errorText(query.error.value) }}</p>
    <p v-if="notice" role="status" class="callout blue">{{ notice }}</p>
    <form v-if="opened" class="binding-form" @submit.prevent="save">
      <h3>{{ t(editing ? 'bindingUI.edit' : 'bindingUI.add') }}</h3>
      <p v-if="editing" class="callout amber">{{ t('bindingUI.sharedTLS') }}</p>
      <fieldset :disabled="busy">
        <label v-if="!editing">{{ t('bindingUI.existing') }}<select v-model="form.domainID"><option v-for="domain in data?.domains" :key="domain.id" :value="domain.id">{{ domain.host }}</option></select></label>
        <label v-if="!form.domainID">{{ t('bindingUI.host') }}<input v-model="form.host" required autocomplete="off" :readonly="!!editing" name="host"></label>
        <label>{{ t('bindingUI.path') }}<input v-model="form.path" required pattern="/.*" name="path"></label>
        <label>{{ t('bindingUI.port') }}<input v-model.number="form.port" type="number" required min="1" max="65535" name="port"></label>
        <label>{{ t('bindingUI.protocol') }}<select v-model="form.protocol"><option value="http">HTTP</option><option value="https">HTTPS</option></select></label>
        <label v-if="form.protocol === 'https'">{{ t('bindingUI.serverName') }}<input v-model="form.serverName" autocomplete="off"></label>
        <template v-if="!form.domainID">
          <label>{{ t('bindingUI.tls') }}<select v-model="form.tls" name="tls"><option value="dns-01">ACME DNS-01</option><option value="http-01">ACME HTTP-01</option><option value="custom">{{ t('bindingUI.custom') }}</option></select></label>
          <template v-if="form.tls === 'dns-01'">
            <label>{{ t('bindingUI.provider') }}<select v-model="form.provider" required><option disabled value="">{{ t('bindingUI.select') }}</option><option v-for="provider in data?.providers" :key="provider" :value="provider">{{ provider }}</option></select></label>
            <p v-if="!data?.providers.length" class="callout amber">{{ t('bindingUI.noProvider') }}</p>
            <button type="button" class="btn" @click="profilesOpen = !profilesOpen">{{ t('dnsProfiles.manage') }}</button>
          </template>
          <p v-else-if="form.tls === 'http-01'" class="callout amber">{{ t('bindingUI.httpChallenge') }}</p>
          <template v-else>
            <label>{{ t('bindingUI.certificate') }}<select v-model="form.certificateID" required><option disabled value="">{{ t('bindingUI.select') }}</option><option v-for="certificate in certificates" :key="certificate.id" :value="certificate.id">{{ certificate.domains.join(', ') }} · {{ certificate.not_after }} · {{ certificate.fingerprint }}</option></select></label>
            <button type="button" class="btn" @click="importing = !importing">{{ t('bindingUI.import') }}</button>
            <div v-if="importing" class="binding-import">
              <label>{{ t('bindingUI.chain') }}<textarea v-model="certificatePEM" rows="5" autocomplete="off" spellcheck="false"></textarea></label>
              <label>{{ t('bindingUI.key') }}<textarea v-model="privateKeyPEM" rows="5" autocomplete="off" spellcheck="false"></textarea></label>
              <button type="button" class="btn" :disabled="!certificatePEM || !privateKeyPEM" @click="importCertificate">{{ t('bindingUI.import') }}</button>
            </div>
            <p class="hint">{{ t('bindingUI.customHint') }}</p>
          </template>
          <label class="binding-checkbox"><input v-model="form.redirect" type="checkbox">{{ t('bindingUI.redirect') }}</label>
        </template>
        <div class="flex"><button class="btn primary" type="submit" :disabled="!writable">{{ t('bindingUI.save') }}</button><button class="btn" type="button" @click="close">{{ t('console.cancel') }}</button></div>
      </fieldset>
    </form>
    <DNSProfilesPanel v-if="profilesOpen" @changed="query.refetch()" />
    <article v-for="binding in data?.bindings" :key="binding.id" class="binding-row">
      <div class="flex"><strong>{{ binding.domain.host }}{{ binding.route.path }}</strong><span class="spacer"></span><button class="btn sm" :disabled="!writable || busy || !binding.editable || binding.status === 'DISABLED'" @click="edit(binding)">{{ t('bindingUI.edit') }}</button><button class="btn sm" :disabled="!writable || busy || !binding.editable || binding.status === 'DISABLED'" @click="remove(binding)">{{ t('bindingUI.remove') }}</button></div>
      <p>{{ t('bindingUI.routeState') }}: <StatusBadge :status="binding.status" /> · {{ t('bindingUI.certificate') }}: <StatusBadge :status="data?.certificates.find(c => c.id === binding.domain.tls.certificate_id)?.status || ''" /> · {{ binding.domain.tls.mode === 'custom' ? t('bindingUI.custom') : binding.domain.tls.acme_challenge }}</p>
      <button v-if="binding.status === 'ERROR'" class="btn sm" :disabled="!writable || busy" @click="retry(binding)">{{ t('bindingUI.retry') }}</button>
      <p v-if="binding.route.last_error" class="callout red">{{ binding.route.last_error }}</p>
      <p v-if="binding.domain.last_error" class="callout red">{{ binding.domain.last_error }}</p>
      <p v-if="data?.certificates.find(c => c.id === binding.domain.tls.certificate_id)?.last_error" class="callout red">{{ data?.certificates.find(c => c.id === binding.domain.tls.certificate_id)?.last_error }}</p>
      <p v-for="target in binding.dns_targets" :key="target.gateway_id" class="hint">{{ target.gateway_name }}: {{ target.record_type }} → {{ target.value }}</p>
    </article>
    <details v-if="data?.operations.length"><summary>{{ t('bindingUI.operations') }}</summary><p v-for="operation in data.operations" :key="operation.id"><a :href="`/operations/${operation.id}`">{{ operation.id }}</a> · {{ operation.type }} · <StatusBadge :status="operation.status" context="operation" /> {{ operation.error_message }}</p></details>
    <details v-if="data?.gateways.length"><summary>{{ t('bindingUI.gateways') }}</summary><p class="hint">{{ t('bindingUI.evidenceHint') }}</p><p v-for="gateway in data.gateways" :key="gateway.id">{{ gateway.name }}: <StatusBadge :status="gateway.status" /> · <StatusBadge :status="gateway.evidence?.configuration_state || 'UNKNOWN'" /> · {{ gateway.applied_config_revision }} / {{ gateway.desired_config_revision }} · {{ gateway.evidence?.observed_at || '—' }}</p></details>
    <DomainDraftForm v-if="draftOpen && data?.service.environment?.id" :environment-id="data.service.environment.id" :service-id="props.serviceId" @saved="draftOpen = false; notice = t('domainDraft.saved'); query.refetch()" @close="draftOpen = false" />
  </section>
</template>

<style scoped>
.binding-panel { margin-top: 24px; padding: 20px; }
.binding-panel h2 { margin: 0; }
.binding-form { margin: 16px 0; }
.binding-form fieldset { border: 0; padding: 0; display: grid; gap: 14px; }
.binding-form label { display: grid; gap: 6px; }
.binding-form input, .binding-form select, .binding-form textarea { width: 100%; }
.binding-form .binding-checkbox { display: flex; align-items: center; }
.binding-checkbox input { width: auto; }
.binding-row { border-top: 1px solid var(--border); padding: 16px 0; overflow-wrap: anywhere; }
.binding-import { display: grid; gap: 12px; }
</style>
