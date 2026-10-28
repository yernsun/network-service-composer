<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { asRow, asRows, errorText, request, session } from '../shared/api/client'
import { translate as t } from '../i18n'

type Key = { id: string; name: string; project_id: string; scopes: string[]; revision: number; revoked_at?: string }
type Project = { id: string; name: string; system?: boolean }
const props = withDefaults(defineProps<{ userId: string; enabled?: boolean }>(), { enabled: true })
const groups = [
  { name: 'services', actions: ['read', 'create', 'edit', 'deploy', 'operate'] },
  { name: 'project_variables', actions: ['read', 'edit'] },
  { name: 'environment_variables', actions: ['read', 'edit'] },
  { name: 'domains', actions: ['read', 'create', 'edit', 'delete', 'publish'] },
]
const readScopes = groups.map(group => `${group.name}.read`)
const keys = ref<Key[]>([]), projects = ref<Project[]>([])
const name = ref(''), projectId = ref(''), password = ref('')
const selected = ref<string[]>([...readScopes]), issued = ref(''), busy = ref(false), error = ref('')
const selfViewer = computed(() => session.value.role === 'viewer' && session.value.user_id === props.userId)
const canIssue = computed(() => props.enabled && !!name.value.trim() && !!projectId.value && !!password.value && selected.value.length > 0 && !busy.value)
async function load() {
  if (!props.userId) return
  try {
    const result = await request('get', '/users/{userID}/api-keys', { params: { path: { userID: props.userId } } })
    keys.value = asRows(result.data) as Key[]
    error.value = ''
  } catch (cause) { error.value = errorText(cause) }
}
async function loadProjects() {
  try {
    const all: Project[] = []
    let cursor = ''
    do {
      const result = await request('get', '/projects', { params: { query: { limit: 200, ...(cursor ? { cursor } : {}) } } })
      all.push(...(asRows(result.data) as Project[]).filter(project => !project.system))
      cursor = result.pagination?.next_cursor || ''
    } while (cursor)
    projects.value = all
    if (!projects.value.some(project => project.id === projectId.value)) projectId.value = projects.value[0]?.id || ''
  } catch (cause) { error.value = errorText(cause) }
}
function presetRead() { selected.value = [...readScopes] }
async function issue() {
  if (!canIssue.value) return
  busy.value = true; error.value = ''; issued.value = ''
  try {
    const result = await request('post', '/users/{userID}/api-keys', {
      params: { path: { userID: props.userId } },
      headers: { 'X-NSC-Reauthenticate': password.value },
      body: { name: name.value.trim(), project_id: projectId.value, scopes: selected.value },
    })
    issued.value = String(asRow(result.data).token || '')
    name.value = ''; password.value = ''
    await load()
  } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false }
}
async function revoke(key: Key) {
  if (!password.value || busy.value) { error.value = t('keys.passwordRequired'); return }
  busy.value = true; error.value = ''; issued.value = ''
  try {
    await request('delete', '/users/{userID}/api-keys/{keyID}', {
      params: { path: { userID: props.userId, keyID: key.id } },
      headers: { 'If-Match': `"rev-${key.revision}"`, 'X-NSC-Reauthenticate': password.value },
    })
    password.value = ''
    await load()
  } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false }
}
onMounted(() => { void loadProjects() })
watch(() => props.userId, () => { issued.value = ''; void load() }, { immediate: true })
</script>

<template>
  <section class="card key-panel">
    <h2>{{ t('keys.title') }}</h2>
    <p class="muted">{{ t('keys.boundary') }}</p>
    <div v-if="error" class="callout red" role="alert">{{ error }}</div>
    <div v-if="issued" class="callout amber" role="status">
      <strong>{{ t('keys.copyNow') }}</strong>
      <code class="key-secret">{{ issued }}</code>
      <button class="btn" type="button" @click="issued = ''">{{ t('keys.dismiss') }}</button>
    </div>
    <div class="key-form">
      <label>{{ t('keys.name') }} <input v-model="name" maxlength="100" autocomplete="off" /></label>
      <label>{{ t('keys.project') }}
        <select v-model="projectId"><option v-for="project in projects" :key="project.id" :value="project.id">{{ project.name }}</option></select>
      </label>
      <div class="key-scope-header"><strong>{{ t('keys.scopes') }}</strong><button class="btn" type="button" @click="presetRead">{{ t('keys.readPreset') }}</button></div>
      <div v-for="group in groups" :key="group.name" class="key-scope-group">
        <strong>{{ t(`keys.resource.${group.name}`) }}</strong>
        <label v-for="action in group.actions" :key="action" class="key-check" :class="{ muted: selfViewer && action !== 'read' }">
          <input v-model="selected" type="checkbox" :value="`${group.name}.${action}`" :disabled="selfViewer && action !== 'read'" />
          {{ t(`keys.action.${action}`) }}
        </label>
      </div>
      <label>{{ t('keys.password') }} <input v-model="password" type="password" autocomplete="current-password" /></label>
      <button class="btn primary" type="button" :disabled="!canIssue" @click="issue">{{ t('keys.issue') }}</button>
    </div>
    <h3>{{ t('keys.existing') }}</h3>
    <div v-if="!keys.length" class="muted">{{ t('keys.empty') }}</div>
    <div v-for="key in keys" :key="key.id" class="key-row">
      <div>
        <strong>{{ key.name }}</strong> · {{ projects.find(project => project.id === key.project_id)?.name || key.project_id }}
        <span v-if="key.revoked_at" class="badge">{{ t('keys.revoked') }}</span>
        <div class="muted">{{ (key.scopes || []).map(scope => t(`keys.resource.${scope.split('.')[0]}`) + ': ' + t(`keys.action.${scope.split('.')[1]}`)).join(' · ') }}</div>
      </div>
      <button v-if="!key.revoked_at" class="btn" type="button" :disabled="busy" @click="revoke(key)">{{ t('keys.revoke') }}</button>
    </div>
  </section>
</template>

<style scoped>
.key-panel { margin-top: 20px; padding: 22px; }
.key-form { display: grid; gap: 14px; max-width: 840px; margin: 22px 0; }
.key-form > label { display: grid; gap: 6px; }
.key-form input:not([type='checkbox']), .key-form select { width: 100%; }
.key-scope-header { display: flex; align-items: center; gap: 14px; }
.key-scope-group { display: flex; flex-wrap: wrap; align-items: center; gap: 14px; padding: 10px; border: 1px solid var(--line, #ddd); border-radius: 8px; }
.key-scope-group > strong { min-width: 160px; }
.key-check { display: inline-flex; align-items: center; gap: 5px; }
.key-row { display: flex; justify-content: space-between; align-items: center; gap: 18px; padding: 12px 0; border-top: 1px solid var(--line, #ddd); }
.key-secret { display: block; overflow-wrap: anywhere; margin: 10px 0; }
</style>
