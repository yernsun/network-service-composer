<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useRoute } from 'vue-router'
import { useWorkspaceStore } from '../stores/workspace'
import { translate as t } from '../i18n'
import type { components } from '../shared/api/schema'
import SelfSignedCertificateForm from '../components/SelfSignedCertificateForm.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { allPages } from '../data/pagination'

type Certificate = components['schemas']['Certificate']
const route = useRoute(), store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const project = computed(() => String(route.params.project || state.value.project))
const environment = computed(() => String(route.params.environment || state.value.env))
const domainFilter = ref('')
const issuing = new Set(['PENDING', 'AUTHORIZING', 'VALIDATING', 'ISSUED', 'RENEWING'])
const { data, error, isFetching, refetch } = useQuery<Certificate[]>({
  queryKey: ['certificate-assets'],
  queryFn: async ({ signal }) => await allPages('/certificates', {}, signal) as Certificate[],
  initialData: () => state.value.certificates as Certificate[] || [],
  staleTime: 0,
  refetchOnWindowFocus: 'always',
  refetchInterval: query => query.state.data?.some(certificate => certificate.provider === 'acme' && issuing.has(certificate.status)) ? 5000 : 30000,
})
const scopedDomains = computed(() => state.value.domains.filter(domain =>
  (project.value === 'all' || domain.project === project.value) &&
  (environment.value === 'all' || domain.env === environment.value || domain.environmentIds?.includes(environment.value)),
))
function related(certificate: Certificate) { return scopedDomains.value.filter(domain => domain.tls?.certificate_id === certificate.id) }
function names(certificate: Certificate) { return [...new Set([certificate.primary_domain, ...certificate.domains].filter(Boolean))] }
const scopedCertificates = computed(() => (data.value || []).filter(certificate =>
  project.value === 'all' && environment.value === 'all' || related(certificate).length > 0,
))
const certificates = computed(() => {
  const filter = domainFilter.value.trim().toLowerCase()
  return scopedCertificates.value.filter(certificate => !filter || [...names(certificate), ...related(certificate).map(domain => domain.name)].some(name => {
    const host = name.toLowerCase()
    return host.includes(filter) || host.startsWith('*.') && filter.split('.').length === host.split('.').length && filter.endsWith(host.slice(1))
  }))
})
function title(certificate: Certificate) { return certificate.primary_domain || certificate.domains?.join(', ') || certificate.id }
function expiry(certificate: Certificate) { return certificate.not_after ? new Date(certificate.not_after).toLocaleString() : '—' }
</script>

<template>
  <div class="breadcrumbs"><span>{{ environment === 'all' ? t('workspace.all_environments') : state.environments.find(item => item.id === environment)?.name }}</span><span class="slash">/</span><span class="current">{{ t('prototype.tls_certificates') }}</span></div>
  <div class="global-title"><div><h1>{{ t('prototype.tls_certificates') }}</h1><p>{{ t('certAssets.description') }}</p></div></div>
  <SelfSignedCertificateForm @created="refetch()" />
  <div class="certificate-toolbar">
    <label for="certificate-domain-filter">{{ t('certAssets.domainFilter') }}<input id="certificate-domain-filter" v-model="domainFilter" type="search" :placeholder="t('certAssets.domainPlaceholder')"></label>
    <button type="button" class="btn" :disabled="isFetching" data-testid="certificate-refresh" @click="refetch()">{{ error ? t('console.retry') : t('certAssets.refresh') }}</button>
    <span class="hint">{{ t('certAssets.autoRefresh') }}</span>
    <span v-if="isFetching" role="status">{{ t('bindingUI.loading') }}</span>
  </div>
  <p v-if="error" class="callout amber" role="alert">{{ t('refresh.failed') }}</p>
  <div class="card certificate-assets"><table><thead><tr><th>{{ t('certAssets.name') }}</th><th>{{ t('certAssets.status') }}</th><th>{{ t('certAssets.challenge') }}</th><th>{{ t('certAssets.expires') }}</th><th>{{ t('certAssets.domains') }}</th><th>{{ t('certAssets.linkedDomains') }}</th></tr></thead><tbody><tr v-for="certificate in certificates" :key="certificate.id" :data-certificate-id="certificate.id"><td><strong>{{ title(certificate) }}</strong><small>{{ certificate.provider === 'self-signed' ? t('selfSigned.label') : certificate.provider === 'custom' ? t('bindingUI.custom') : t('certAssets.automatic') }}</small></td><td><StatusBadge :status="certificate.status" /><p v-if="certificate.last_error" class="error-text">{{ certificate.last_error }}</p></td><td>{{ certificate.acme_challenge || '—' }}<span v-if="certificate.dns_provider_ref"> · {{ certificate.dns_provider_ref }}</span></td><td>{{ expiry(certificate) }}</td><td><div v-for="name in names(certificate)" :key="name">{{ name }}</div></td><td><div v-for="domain in related(certificate)" :key="domain.id">{{ domain.name }} <small v-if="environment === 'all'">· {{ state.environments.find(item => item.id === domain.env)?.name }}</small></div><span v-if="!related(certificate).length" class="hint">{{ t('certAssets.unbound') }}</span></td></tr></tbody></table><p v-if="!certificates.length" class="hint empty">{{ domainFilter.trim() ? t('certAssets.emptyFiltered') : t('certAssets.empty') }}</p></div>
</template>

<style scoped>
.certificate-assets { overflow-x: auto; }.certificate-assets table { width: 100%; border-collapse: collapse; }.certificate-assets th, .certificate-assets td { padding: 14px; text-align: left; border-bottom: 1px solid var(--border); vertical-align: top; }.certificate-assets small { display: block; color: var(--muted); }.error-text { color: #b4233d; max-width: 28ch; overflow-wrap: anywhere; }.empty { padding: 20px; }
.certificate-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; margin-bottom: 16px; }.certificate-toolbar label { display: flex; align-items: center; gap: 8px; }.certificate-toolbar input { width: min(300px, 55vw); }
</style>
