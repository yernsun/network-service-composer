<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { errorText, request, session } from '../shared/api/client'
import { translate as t } from '../i18n'
import type { components } from '../shared/api/schema'

type Profile = components['schemas']['DNSProfile']
const emit = defineEmits<{ changed: [reference: string] }>()
const profiles = ref<Profile[]>([]), busy = ref(false), error = ref(''), notice = ref(''), open = ref(false)
const password = ref(''), zone = ref(''), token = ref('')
const form = reactive({ reference: '', provider: 'cloudflare', region: 'ovh-eu', apiToken: '', zoneToken: '', accessKey: '', secretKey: '', secretID: '', applicationKey: '', applicationSecret: '', consumerKey: '' })
const editing = ref<Profile | null>(null)
const canWrite = () => session.value.role === 'administrator'
async function load() {
  const result = await request('get', '/dns-provider-profiles')
  profiles.value = (result.data || []) as Profile[]
}
onMounted(() => { void load().catch(e => { error.value = errorText(e) }) })
function reset() {
  editing.value = null
  Object.assign(form, { reference: '', provider: 'cloudflare', region: 'ovh-eu', apiToken: '', zoneToken: '', accessKey: '', secretKey: '', secretID: '', applicationKey: '', applicationSecret: '', consumerKey: '' })
  zone.value = ''; token.value = ''; password.value = ''
}
function begin(profile?: Profile) {
  reset(); editing.value = profile || null
  if (profile) Object.assign(form, { reference: profile.reference, provider: profile.provider, region: profile.region || 'ovh-eu' })
  open.value = true; error.value = ''; notice.value = ''
}
function credentials() {
  switch (form.provider) {
    case 'cloudflare': return { api_token: form.apiToken, zone_api_token: form.zoneToken }
    case 'hurricane': return { tokens: { [zone.value.trim().toLowerCase()]: token.value } }
    case 'alidns': return { access_key: form.accessKey, secret_key: form.secretKey }
    case 'tencentcloud': return { secret_id: form.secretID, secret_key: form.secretKey }
    case 'ovh': return { application_key: form.applicationKey, application_secret: form.applicationSecret, consumer_key: form.consumerKey }
    default: return {}
  }
}
async function save() {
  if (busy.value || !canWrite()) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const body = { reference: form.reference.trim(), provider: form.provider, ...(form.provider === 'ovh' ? { region: form.region } : {}), credentials: credentials() }
    if (editing.value) {
      await request('put', '/dns-provider-profiles/{reference}', { params: { path: { reference: editing.value.reference } }, body,
        headers: { 'X-NSC-Reauthenticate': password.value, 'If-Match': `"rev-${editing.value.revision}"` } })
    } else {
      await request('post', '/dns-provider-profiles', { body, headers: { 'X-NSC-Reauthenticate': password.value } })
    }
    const reference = body.reference
    reset(); open.value = false; await load(); emit('changed', reference)
    notice.value = t('dnsProfiles.saved')
  } catch (e) { error.value = errorText(e) } finally { password.value = ''; busy.value = false }
}
async function remove(profile: Profile) {
  if (busy.value || !canWrite() || !window.confirm(t('dnsProfiles.deleteConfirm'))) return
  const reauth = window.prompt(t('dnsProfiles.password'))
  if (!reauth) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await request('delete', '/dns-provider-profiles/{reference}', { params: { path: { reference: profile.reference } },
      headers: { 'X-NSC-Reauthenticate': reauth, 'If-Match': `"rev-${profile.revision}"` } })
    await load(); emit('changed', ''); notice.value = t('dnsProfiles.deleted')
  } catch (e) { error.value = errorText(e) } finally { busy.value = false }
}
</script>

<template>
  <section class="dns-profiles card">
    <header class="flex"><h3>{{ t('dnsProfiles.title') }}</h3><span class="spacer"></span><button v-if="canWrite()" type="button" class="btn" @click="open ? (open = false, reset()) : begin()">{{ open ? t('console.cancel') : t('dnsProfiles.add') }}</button></header>
    <p class="hint">{{ t('dnsProfiles.description') }}</p>
    <p v-if="error" class="callout red" role="alert">{{ error }}</p><p v-if="notice" class="callout blue" role="status">{{ notice }}</p>
    <div v-for="profile in profiles" :key="profile.reference" class="profile-row flex"><strong>{{ profile.reference }}</strong><span>{{ profile.provider }}</span><span class="spacer"></span><small>{{ profile.source === 'config' ? t('dnsProfiles.configuredInFile') : t('dnsProfiles.database') }}</small><template v-if="profile.source === 'database' && canWrite()"><button type="button" class="btn sm" :disabled="busy" @click="begin(profile)">{{ t('dnsProfiles.rotate') }}</button><button type="button" class="btn sm" :disabled="busy" @click="remove(profile)">{{ t('dnsProfiles.delete') }}</button></template></div>
    <form v-if="open && canWrite()" class="profile-form" autocomplete="off" @submit.prevent="save">
      <label>{{ t('dnsProfiles.reference') }}<input v-model.trim="form.reference" required maxlength="128" :disabled="!!editing" autocomplete="off"></label>
      <label>{{ t('dnsProfiles.provider') }}<select v-model="form.provider" :disabled="!!editing"><option value="cloudflare">Cloudflare</option><option value="hurricane">HE.net / Hurricane</option><option value="alidns">Aliyun / Alidns</option><option value="tencentcloud">Tencent Cloud</option><option value="ovh">OVH</option></select></label>
      <template v-if="form.provider === 'cloudflare'"><label>API Token<input v-model="form.apiToken" type="password" required autocomplete="new-password"></label><label>Zone Token (optional)<input v-model="form.zoneToken" type="password" autocomplete="new-password"></label></template>
      <template v-if="form.provider === 'hurricane'"><label>{{ t('dnsProfiles.zone') }}<input v-model.trim="zone" required autocomplete="off" placeholder="example.com"></label><label>DDNS Key / Token<input v-model="token" type="password" required autocomplete="new-password"></label></template>
      <template v-if="form.provider === 'alidns'"><label>Access Key<input v-model="form.accessKey" type="password" required autocomplete="new-password"></label><label>Secret Key<input v-model="form.secretKey" type="password" required autocomplete="new-password"></label></template>
      <template v-if="form.provider === 'tencentcloud'"><label>Secret ID<input v-model="form.secretID" type="password" required autocomplete="new-password"></label><label>Secret Key<input v-model="form.secretKey" type="password" required autocomplete="new-password"></label></template>
      <template v-if="form.provider === 'ovh'"><label>Endpoint<select v-model="form.region"><option value="ovh-eu">OVH EU</option><option value="ovh-ca">OVH CA</option></select></label><label>Application Key<input v-model="form.applicationKey" type="password" required autocomplete="new-password"></label><label>Application Secret<input v-model="form.applicationSecret" type="password" required autocomplete="new-password"></label><label>Consumer Key<input v-model="form.consumerKey" type="password" required autocomplete="new-password"></label></template>
      <label>{{ t('dnsProfiles.password') }}<input v-model="password" type="password" required autocomplete="current-password"></label>
      <p class="hint">{{ t('dnsProfiles.writeOnly') }}</p><button class="btn primary" type="submit" :disabled="busy">{{ t('dnsProfiles.save') }}</button>
    </form>
  </section>
</template>

<style scoped>
.dns-profiles { padding: 20px; margin: 20px 0; }.dns-profiles h3 { margin: 0; }.profile-row { gap: 12px; border-top: 1px solid var(--border); padding: 12px 0; }.profile-form { display: grid; gap: 12px; margin-top: 16px; }.profile-form label { display: grid; gap: 5px; }.profile-form input, .profile-form select { width: 100%; }
</style>
