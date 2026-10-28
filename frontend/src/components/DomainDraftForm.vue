<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { errorText, request } from '../shared/api/client'
import { allPages } from '../data/pagination'
import { translate as t } from '../i18n'
import type { components } from '../shared/api/schema'
import SelfSignedCertificateForm from './SelfSignedCertificateForm.vue'
import DNSProfilesPanel from './DNSProfilesPanel.vue'

type Draft = components['schemas']['DomainDraft']
type Row = Record<string, unknown>
const props = defineProps<{ environmentId: string; draft?: Draft | null; serviceId?: string }>()
const emit = defineEmits<{ saved: [draft: Draft]; close: [] }>()
const services = ref<Row[]>([]), gateways = ref<Row[]>([]), certificates = ref<Row[]>([]), profiles = ref<components['schemas']['DNSProfile'][]>([])
const busy = ref(false), loading = ref(true), initialized = ref(false), error = ref(''), showProfiles = ref(false)
const form = reactive({ host: props.draft?.domain.host || '', serviceID: props.draft?.service_id || props.serviceId || '', path: props.draft?.route.path || '/', port: props.draft?.route.backend.port || 80,
  protocol: props.draft?.route.backend.protocol || 'http', serverName: props.draft?.route.backend.server_name || '',
  tlsMode: props.draft?.domain.tls.mode || 'managed', dnsManaged: props.draft?.domain.tls.acme_challenge === 'dns-01',
  provider: props.draft?.domain.tls.dns_provider_ref || '', certificateID: props.draft?.domain.tls.certificate_id || '',
  redirect: props.draft?.domain.redirect_http_to_https || false, http3: props.draft?.domain.http3_enabled || false })
const poolId = ref('')
const selectedService = computed(() => services.value.find(s => s.id === form.serviceID))
async function load() {
  loading.value = true
  try {
    const env = (await request('get', '/environments/{id}', { params: { path: { id: props.environmentId } } })).data as Row
    const runtime = (await request('get', '/runtimes/{id}', { params: { path: { id: String(env.runtime_id) } } })).data as Row
    poolId.value = String(runtime.pool_id)
    const [allServices, allGateways, allCertificates, profileResult] = await Promise.all([
      allPages('/services'), allPages('/gateways'), allPages('/certificates'), request('get', '/dns-provider-profiles'),
    ])
    services.value = allServices.filter(s => (s.environment as Row | undefined)?.id === env.id && !s.system && s.status !== 'REMOVED')
    gateways.value = allGateways.filter(g => g.pool_id === runtime.pool_id && g.enabled)
    certificates.value = allCertificates.filter(c => c.pool_id === runtime.pool_id && (c.provider === 'custom' || c.provider === 'self-signed') && c.status === 'ACTIVE')
    profiles.value = (profileResult.data || []) as components['schemas']['DNSProfile'][]
    if (form.serviceID && !services.value.some(s => s.id === form.serviceID)) form.serviceID = ''
    if (!props.draft && !initialized.value && selectedService.value) form.port = Number(selectedService.value.target_port_override || selectedService.value.discovered_port || 80)
    initialized.value = true
  } catch (e) { error.value = errorText(e) } finally { loading.value = false }
}
onMounted(() => { void load() })
watch(() => form.serviceID, () => { if (!props.draft && selectedService.value) form.port = Number(selectedService.value.target_port_override || selectedService.value.discovered_port || 80) })
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const tls = form.tlsMode === 'disabled' ? { mode: 'disabled' } : form.tlsMode === 'custom'
      ? { mode: 'custom', certificate_id: form.certificateID }
      : form.dnsManaged ? { mode: 'managed', acme_challenge: 'dns-01', dns_provider_ref: form.provider }
        : { mode: 'managed', acme_challenge: 'http-01' }
    const domain = { host: form.host.trim(), entrypoints: form.tlsMode === 'disabled' ? ['http'] : form.redirect ? ['http', 'https'] : ['https'],
      redirect_http_to_https: form.tlsMode !== 'disabled' && form.redirect, http3_enabled: form.tlsMode !== 'disabled' && form.http3, tls }
    const body = { domain, service_id: form.serviceID, route: { path: form.path || '/', backend: { port: Number(form.port), protocol: form.protocol, ...(form.serverName ? { server_name: form.serverName } : {}) } } }
    const result = props.draft
      ? await request('put', '/domain-drafts/{id}', { params: { path: { id: props.draft.id } }, body, headers: { 'If-Match': `"rev-${props.draft.revision}"` } })
      : await request('post', '/environments/{id}/domain-drafts', { params: { path: { id: props.environmentId } }, body })
    emit('saved', result.data as Draft)
    window.dispatchEvent(new Event('nsc:resources-changed'))
  } catch (e) { error.value = errorText(e) } finally { busy.value = false }
}
</script>

<template>
  <div class="backdrop domain-draft-backdrop" @click.self="emit('close')">
    <section class="modal drawer domain-draft-modal" role="dialog" aria-modal="true" :aria-label="t('domainDraft.title')">
      <div class="modal-head"><div><h2>{{ t('domainDraft.title') }}</h2><p>{{ t('domainDraft.saveHint') }}</p></div><button type="button" class="iconbtn" :aria-label="t('common.close')" @click="emit('close')">×</button></div>
      <form class="modal-body domain-draft-fields" @submit.prevent="save">
        <p v-if="error" class="callout red" role="alert">{{ error }}</p>
        <p v-if="loading">{{ t('bindingUI.loading') }}</p>
        <label>{{ t('domainDraft.host') }}<input v-model.trim="form.host" type="text" required autocomplete="off" placeholder="app.example.com"></label>
        <label>{{ t('domainDraft.service') }}<select v-model="form.serviceID"><option value="">{{ t('domainDraft.noService') }}</option><option v-for="service in services" :key="String(service.id)" :value="String(service.id)">{{ service.name }} · {{ service.discovered_port || service.target_port_override || '—' }}</option></select></label>
        <p v-if="!form.serviceID" class="callout amber">{{ t('domainDraft.serviceRequiredToPublish') }}</p>
        <div class="draft-two"><label>{{ t('bindingUI.path') }}<input v-model="form.path" required></label><label>{{ t('bindingUI.port') }}<input v-model.number="form.port" type="number" required min="1" max="65535"></label></div>
        <label>{{ t('bindingUI.protocol') }}<select v-model="form.protocol"><option value="http">HTTP</option><option value="https">HTTPS</option></select></label>
        <label v-if="form.protocol === 'https'">{{ t('bindingUI.serverName') }}<input v-model="form.serverName" autocomplete="off"></label>
        <label>{{ t('domainDraft.tlsMode') }}<select v-model="form.tlsMode"><option value="managed">{{ t('domainDraft.acme') }}</option><option value="disabled">{{ t('domainDraft.httpOnly') }}</option><option value="custom">{{ t('bindingUI.custom') }}</option></select></label>
        <template v-if="form.tlsMode === 'managed'">
          <label class="draft-checkbox"><input v-model="form.dnsManaged" type="checkbox">{{ t('domainDraft.managedDNS') }}</label>
          <p v-if="!form.dnsManaged" class="callout blue">{{ t('domainDraft.httpChallenge') }}</p>
          <template v-else>
            <label>{{ t('bindingUI.provider') }}<select v-model="form.provider" required><option disabled value="">{{ t('bindingUI.select') }}</option><option v-for="profile in profiles" :key="profile.reference" :value="profile.reference">{{ profile.reference }} · {{ profile.provider }}</option></select></label>
            <button type="button" class="btn" @click="showProfiles = !showProfiles">{{ t('dnsProfiles.manage') }}</button>
          </template>
        </template>
        <label v-else-if="form.tlsMode === 'custom'">{{ t('bindingUI.certificate') }}<select v-model="form.certificateID" required><option disabled value="">{{ t('bindingUI.select') }}</option><option v-for="certificate in certificates" :key="String(certificate.id)" :value="String(certificate.id)">{{ certificate.primary_domain || certificate.id }}</option></select></label>
        <SelfSignedCertificateForm v-if="form.tlsMode === 'custom' && poolId" :pool-id="poolId" :host="form.host" @created="certificate => { certificates.push(certificate); form.certificateID = certificate.id }" />
        <template v-if="form.tlsMode !== 'disabled'"><label class="draft-checkbox"><input v-model="form.redirect" type="checkbox">{{ t('bindingUI.redirect') }}</label><label class="draft-checkbox"><input v-model="form.http3" type="checkbox">HTTP/3</label></template>
        <div><strong>{{ t('domainDraft.gateways') }}</strong><p class="hint">{{ t('domainDraft.poolHint') }}</p><p v-for="gateway in gateways" :key="String(gateway.id)">{{ gateway.name }} · {{ gateway.address }}</p><p v-if="!gateways.length" class="callout amber">{{ t('domainDraft.noGateway') }}</p></div>
        <div class="flex"><button type="submit" class="btn primary" :disabled="busy || loading">{{ t('domainDraft.save') }}</button><button type="button" class="btn" @click="emit('close')">{{ t('console.cancel') }}</button></div>
      </form>
      <DNSProfilesPanel v-if="showProfiles" @changed="load" />
    </section>
  </div>
</template>

<style scoped>
.domain-draft-backdrop { position: fixed; inset: 0; z-index: 90; }.domain-draft-modal { margin-left: auto; height: 100%; max-height: 100%; overflow-y: auto; }.domain-draft-fields { display: grid; gap: 16px; padding: 24px; }.domain-draft-fields label { display: grid; gap: 6px; }.domain-draft-fields input, .domain-draft-fields select { width: 100%; }.draft-two { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }.domain-draft-fields .draft-checkbox { display: flex; align-items: center; gap: 8px; }.draft-checkbox input { width: auto; }
</style>
