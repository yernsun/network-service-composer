<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { errorText, request, session } from '../shared/api/client'
import { queryClient } from '../data/inventory-cache'
import { authMode, credentialLabels, providerFields, visibleFields, type CredentialKey } from '../data/dns-profiles'
import { translate as t } from '../i18n'
import type { components } from '../shared/api/schema'

type Profile = components['schemas']['DNSProfile']
type Detail = components['schemas']['DNSProfileDetailEnvelope']['data']
type TokenRow = { id: number; domain: string; value: string; existing: boolean; removed: boolean }
const emit = defineEmits<{ changed: [reference: string] }>()
const query = useQuery({ queryKey: ['dns-profiles'], queryFn: async ({ signal }) =>
  (await request('get', '/dns-provider-profiles', { signal })).data as Profile[] }, queryClient)
const profiles = computed(() => query.data.value || [])
const busy = ref(false), loadingDetail = ref(false), error = ref(''), notice = ref(''), open = ref(false), conflict = ref(false)
const password = ref(''), editing = ref<Profile | null>(null), configuredFields = ref<string[]>([]), etag = ref(''), originalMode = ref(''), modeChanged = ref(false), regionChanged = ref(false)
const form = reactive({ reference: '', provider: 'cloudflare', region: 'ovh-eu', mode: 'token' })
const values = reactive<Partial<Record<CredentialKey, string>>>({}), clear = reactive<Partial<Record<CredentialKey, boolean>>>({})
const rows = ref<TokenRow[]>([])
let nextRow = 0, generation = 0, detailAbort: AbortController | undefined
const fields = computed(() => visibleFields(form.provider, form.mode))
const canWrite = () => session.value.role === 'administrator'
function addToken(domain = '', existing = false) { rows.value.push({ id: ++nextRow, domain, value: '', existing, removed: false }) }
function clearInputs() { for (const key of Object.keys(credentialLabels) as CredentialKey[]) { values[key] = ''; clear[key] = false } }
function close() {
  generation++; detailAbort?.abort(); open.value = false; editing.value = null; configuredFields.value = []
  password.value = ''; modeChanged.value = false; regionChanged.value = false; etag.value = ''; rows.value = []; clearInputs(); loadingDetail.value = false; conflict.value = false
}
onBeforeUnmount(close)
watch(() => form.provider, () => {
  clearInputs(); rows.value = []; addToken(); form.mode = authMode(form.provider, [])
})
async function readDetail(retain = false) {
  const profile = editing.value
  if (!profile) return
  const attempt = ++generation
  detailAbort?.abort(); detailAbort = new AbortController(); loadingDetail.value = true; error.value = ''
  try {
    const result = await request('get', '/dns-provider-profiles/{reference}', { params: { path: { reference: profile.reference } }, signal: detailAbort.signal })
    if (attempt !== generation) return
    const detail = result.data as Detail
    if (detail.source !== 'database' || detail.provider !== form.provider) throw new Error(t('dnsProfiles.sourceChanged'))
    etag.value = result.response.headers.get('ETag') || `"rev-${detail.revision}"`
    configuredFields.value = detail.credential_fields || []
    originalMode.value = authMode(detail.provider, configuredFields.value)
    if (!retain) {
      form.mode = originalMode.value; form.region = detail.region || 'ovh-eu'; rows.value = []
      for (const domain of detail.token_domains || []) addToken(domain, true)
    } else {
      if (!regionChanged.value) form.region = detail.region || 'ovh-eu'
      const domains = new Set(detail.token_domains || [])
      // A concurrently removed domain must not discard the user's replacement.
      // Keep it as an explicit new row requiring a token or removal.
      for (const row of rows.value) if (row.existing && !domains.has(row.domain) && !row.removed) row.existing = false
      for (const domain of domains) if (!rows.value.some(row => row.domain === domain)) addToken(domain, true)
    }
    conflict.value = false
    if (retain) notice.value = t('dnsProfiles.reviewLatest')
  } catch (e) { if (attempt === generation) { etag.value = ''; error.value = errorText(e) } }
  finally { if (attempt === generation) loadingDetail.value = false }
}
async function begin(profile?: Profile) {
  close(); error.value = ''; notice.value = ''
  form.reference = profile?.reference || ''; form.provider = profile?.provider || 'cloudflare'; form.region = profile?.region || 'ovh-eu'
  form.mode = authMode(form.provider, []); editing.value = profile || null; open.value = true
  if (profile) await readDetail()
  else { rows.value = []; addToken() }
}
function credentials() {
  const result: components['schemas']['DNSProfilePatch']['credentials'] = {}
  for (const key of fields.value) {
    if (editing.value && clear[key]) result[key] = null
    else if (values[key]) result[key] = values[key]
  }
  if (editing.value && modeChanged.value && form.mode !== originalMode.value) {
    for (const key of providerFields[form.provider] || []) if (!fields.value.includes(key)) result[key] = null
  }
  if (form.provider === 'hurricane') {
    const tokens: Record<string, string | null> = {}, seen = new Set<string>()
    if (!rows.value.some(row => !row.removed)) throw new Error(t('dnsProfiles.lastToken'))
    for (const row of rows.value) {
      const domain = row.existing ? row.domain : row.domain.trim().toLowerCase()
      if (!domain || seen.has(domain) || !row.existing && !row.removed && !row.value) throw new Error(t('dnsProfiles.invalidTokens'))
      seen.add(domain)
      if (row.existing && row.removed) tokens[domain] = null
      else if (!row.removed && row.value) tokens[domain] = row.value
    }
    if (Object.keys(tokens).length) result.tokens = tokens
  }
  return result
}
async function save() {
  if (busy.value || loadingDetail.value || !canWrite() || editing.value && !etag.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const fields = credentials(), reference = form.reference.trim()
    if (editing.value) {
      const body: components['schemas']['DNSProfilePatch'] = { ...(Object.keys(fields).length ? { credentials: fields } : {}), ...(form.provider === 'ovh' && regionChanged.value ? { region: form.region } : {}) }
      if (!Object.keys(body).length) throw new Error(t('dnsProfiles.noChanges'))
      await request('patch', '/dns-provider-profiles/{reference}', { params: { path: { reference } }, body,
        headers: { 'X-NSC-Reauthenticate': password.value, 'If-Match': etag.value } })
    } else {
      // Creation has no null operations. The server validates required fields.
      const credentials = Object.fromEntries(Object.entries(fields).filter(([, value]) => value !== null))
      await request('post', '/dns-provider-profiles', { body: { reference, provider: form.provider, ...(form.provider === 'ovh' ? { region: form.region } : {}), credentials }, headers: { 'X-NSC-Reauthenticate': password.value } })
    }
    close(); emit('changed', reference); notice.value = t('dnsProfiles.saved'); await query.refetch()
  } catch (e) {
    error.value = errorText(e)
    if (e && typeof e === 'object' && 'status' in e && e.status === 412) { conflict.value = true; etag.value = '' }
  } finally { password.value = ''; busy.value = false }
}
async function remove(profile: Profile) {
  if (busy.value || !canWrite() || !window.confirm(t('dnsProfiles.deleteConfirm'))) return
  const reauth = window.prompt(t('dnsProfiles.password'))
  if (!reauth) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await request('delete', '/dns-provider-profiles/{reference}', { params: { path: { reference: profile.reference } },
      headers: { 'X-NSC-Reauthenticate': reauth, 'If-Match': `"rev-${profile.revision}"` } })
    emit('changed', ''); notice.value = t('dnsProfiles.deleted'); await query.refetch()
  } catch (e) { error.value = errorText(e) } finally { busy.value = false }
}
</script>

<template>
  <section class="dns-profiles card">
    <header class="flex"><h3>{{ t('dnsProfiles.title') }}</h3><span class="spacer"></span><button v-if="canWrite()" type="button" class="btn" :disabled="busy" @click="open ? close() : begin()">{{ open ? t('console.cancel') : t('dnsProfiles.add') }}</button></header>
    <p class="hint">{{ t('dnsProfiles.description') }}</p>
    <p v-if="error || query.error.value" class="callout red" role="alert">{{ error || errorText(query.error.value) }}</p><p v-if="notice" class="callout blue" role="status">{{ notice }}</p>
    <div v-for="profile in profiles" :key="profile.reference" class="profile-row">
      <div class="flex"><strong>{{ profile.reference }}</strong><span>{{ profile.provider }}</span><span class="spacer"></span><small>{{ profile.source === 'config' ? t('dnsProfiles.configuredInFile') : t('dnsProfiles.database') }} · {{ profile.configured ? t('dnsProfiles.configured') : t('dnsProfiles.unconfigured') }}</small><template v-if="profile.source === 'database' && canWrite()"><button type="button" class="btn sm" :disabled="busy || loadingDetail" @click="begin(profile)">{{ t('dnsProfiles.rotate') }}</button><button type="button" class="btn sm" :disabled="busy" @click="remove(profile)">{{ t('dnsProfiles.delete') }}</button></template></div>
      <p v-if="profile.source === 'config'" class="hint">{{ t('dnsProfiles.fileHint') }}</p>
    </div>
    <form v-if="open && canWrite()" class="profile-form" autocomplete="off" @submit.prevent="save">
      <p v-if="loadingDetail" role="status">{{ t('bindingUI.loading') }}</p>
      <button v-if="editing && !etag && !loadingDetail" type="button" class="btn" @click="readDetail(true)">{{ conflict ? t('dnsProfiles.reloadLatest') : t('bindingUI.retry') }}</button>
      <fieldset :disabled="busy || loadingDetail">
        <label>{{ t('dnsProfiles.reference') }}<input v-model.trim="form.reference" name="reference" required maxlength="128" :disabled="!!editing" autocomplete="off"></label>
        <label>{{ t('dnsProfiles.provider') }}<select v-model="form.provider" name="provider" :disabled="!!editing"><option value="cloudflare">Cloudflare</option><option value="hurricane">HE.net / Hurricane</option><option value="alidns">Aliyun / Alidns</option><option value="tencentcloud">Tencent Cloud</option><option value="ovh">OVH</option></select></label>
        <label v-if="form.provider === 'cloudflare' || form.provider === 'alidns'">{{ t('dnsProfiles.authMode') }}<select v-model="form.mode" name="auth-mode" @change="modeChanged = true"><template v-if="form.provider === 'cloudflare'"><option value="token">API Token</option><option value="key">Email + API Key</option></template><template v-else><option value="key">Access Key + Secret Key</option><option value="role">RAM Role</option></template></select></label>
        <label v-if="form.provider === 'ovh'">Endpoint<select v-model="form.region" name="region" @change="regionChanged = true"><option value="ovh-eu">OVH EU</option><option value="ovh-ca">OVH CA</option></select></label>
        <div v-for="key in fields" :key="key" class="credential-field">
          <label>{{ credentialLabels[key] }}<input v-model="values[key]" :name="key" type="password" :required="!editing && !['zone_api_token', 'security_token', 'session_token'].includes(key)" :disabled="!!clear[key]" :placeholder="editing && configuredFields.includes(key) ? t('dnsProfiles.keepExisting') : ''" autocomplete="new-password"></label>
          <label v-if="editing && configuredFields.includes(key)" class="checkbox"><input v-model="clear[key]" type="checkbox" :name="`clear-${key}`">{{ t('dnsProfiles.clearField') }}</label>
        </div>
        <template v-if="form.provider === 'hurricane'">
          <div v-for="row in rows" :key="row.id" class="token-row" :class="{ removed: row.removed }">
            <label>{{ t('dnsProfiles.zone') }}<input v-model="row.domain" name="token-domain" :readonly="row.existing" :required="!row.removed" :disabled="row.removed" autocomplete="off" placeholder="example.com"></label>
            <label>DDNS Key / Token<input v-model="row.value" name="token-value" type="password" :required="!row.existing && !row.removed" :disabled="row.removed" :placeholder="row.existing ? t('dnsProfiles.keepExisting') : ''" autocomplete="new-password"></label>
            <button type="button" class="btn sm" @click="row.existing ? row.removed = !row.removed : rows.splice(rows.indexOf(row), 1)">{{ row.removed ? t('dnsProfiles.undoRemove') : t('dnsProfiles.removeToken') }}</button>
          </div>
          <button type="button" class="btn" data-add-token @click="addToken()">{{ t('dnsProfiles.addToken') }}</button>
        </template>
        <label>{{ t('dnsProfiles.password') }}<input v-model="password" name="password" type="password" required autocomplete="current-password"></label>
        <p class="hint">{{ t('dnsProfiles.writeOnly') }}</p><button class="btn primary" type="submit" :disabled="busy || loadingDetail || !!editing && !etag">{{ t('dnsProfiles.save') }}</button>
      </fieldset>
    </form>
  </section>
</template>

<style scoped>
.dns-profiles { padding: 20px; margin: 20px 0; }.dns-profiles h3 { margin: 0; }.profile-row { border-top: 1px solid var(--border); padding: 12px 0; }.profile-row .flex { gap: 12px; flex-wrap: wrap; }
.profile-form { margin-top: 16px; }.profile-form fieldset { display: grid; gap: 16px; border: 0; padding: 0; min-width: 0; }.profile-form label { display: grid; gap: 6px; }.profile-form input, .profile-form select { width: 100%; }
.profile-form .checkbox { display: flex; align-items: center; margin-top: 6px; }.checkbox input { width: auto; }.token-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto; gap: 12px; align-items: end; }.token-row.removed { opacity: .65; }
@media (max-width: 640px) { .token-row { grid-template-columns: minmax(0, 1fr); } }
</style>
