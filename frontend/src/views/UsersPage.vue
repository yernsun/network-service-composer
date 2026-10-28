<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import ApiKeysPanel from '../components/ApiKeysPanel.vue'
import { asRows, errorText, request, session } from '../shared/api/client'
import { translate as t } from '../i18n'

type User = { id: string; username: string; role: 'administrator' | 'viewer'; enabled: boolean; revision: number }
const users = ref<User[]>([]), selectedId = ref(''), busy = ref(false), error = ref(''), notice = ref('')
const form = reactive({ username: '', role: 'viewer' as User['role'], enabled: true, password: '', operatorPassword: '' })
function select(user: User) {
  selectedId.value = user.id
  form.username = user.username; form.role = user.role; form.enabled = user.enabled; form.password = ''; form.operatorPassword = ''
}
function addNew() {
  selectedId.value = ''
  form.username = ''; form.role = 'viewer'; form.enabled = true; form.password = ''; form.operatorPassword = ''
}
async function load() {
  try {
    users.value = asRows((await request('get', '/users')).data) as User[]
    const selected = users.value.find(user => user.id === selectedId.value)
    if (selected) select(selected)
  } catch (cause) { error.value = errorText(cause) }
}
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const body = { username: form.username.trim(), role: form.role, enabled: form.enabled, ...(form.password ? { password: form.password } : {}) }
    const headers = { 'X-NSC-Reauthenticate': form.operatorPassword }
    if (selectedId.value) {
      const current = users.value.find(user => user.id === selectedId.value)
      if (!current) return
      await request('put', '/users/{userID}', { params: { path: { userID: current.id } }, headers: { ...headers, 'If-Match': `"rev-${current.revision}"` }, body })
    } else {
      await request('post', '/users', { headers, body })
    }
    notice.value = t('users.saved')
    form.password = ''; form.operatorPassword = ''
    await load()
  } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false }
}
onMounted(() => { if (session.value.role === 'administrator') void load() })
watch(selectedId, () => { notice.value = ''; error.value = '' })
</script>

<template>
  <div class="x-head"><div><h1>{{ t('users.title') }}</h1><p>{{ t('users.description') }}</p></div></div>
  <div v-if="session.role !== 'administrator'" class="callout red">{{ t('users.adminOnly') }}</div>
  <template v-else>
    <div v-if="error" class="callout red" role="alert">{{ error }}</div>
    <div v-if="notice" class="callout green" role="status">{{ notice }}</div>
    <div class="users-layout">
      <section class="card users-list">
        <h2>{{ t('users.members') }}</h2>
        <button class="btn" type="button" @click="addNew">{{ t('users.add') }}</button>
        <button v-for="user in users" :key="user.id" class="user-choice" :class="{ selected: selectedId === user.id }" type="button" @click="select(user)">
          <strong>{{ user.username }}</strong><span>{{ t(`users.role.${user.role}`) }}</span><span v-if="!user.enabled" class="badge">{{ t('users.disabled') }}</span>
        </button>
      </section>
      <section class="card users-editor">
        <h2>{{ selectedId ? t('users.edit') : t('users.add') }}</h2>
        <form class="user-form" @submit.prevent="save">
          <label>{{ t('users.username') }}<input v-model="form.username" maxlength="100" required /></label>
          <label>{{ t('users.roleLabel') }}<select v-model="form.role"><option value="viewer">{{ t('users.role.viewer') }}</option><option value="administrator">{{ t('users.role.administrator') }}</option></select></label>
          <label class="inline"><input v-model="form.enabled" type="checkbox" />{{ t('users.enabled') }}</label>
          <label>{{ selectedId ? t('users.resetPassword') : t('users.initialPassword') }}<input v-model="form.password" type="password" autocomplete="new-password" :required="!selectedId" minlength="16" maxlength="72" /></label>
          <label>{{ t('users.operatorPassword') }}<input v-model="form.operatorPassword" type="password" autocomplete="current-password" required /></label>
          <p class="muted">{{ t('users.keyRevocationNotice') }}</p>
          <button class="btn primary" type="submit" :disabled="busy">{{ t('users.save') }}</button>
        </form>
      </section>
    </div>
    <ApiKeysPanel v-if="selectedId" :user-id="selectedId" :enabled="users.find(user => user.id === selectedId)?.enabled ?? false" />
  </template>
</template>

<style scoped>
.users-layout { display: grid; grid-template-columns: minmax(220px, 300px) minmax(350px, 1fr); gap: 20px; }
.users-list, .users-editor { padding: 22px; }
.user-choice { width: 100%; display: flex; gap: 8px; text-align: left; align-items: center; padding: 10px; border: 0; border-bottom: 1px solid var(--line, #ddd); background: transparent; cursor: pointer; }
.user-choice.selected { font-weight: bold; background: var(--surface-2, #eee); }
.user-form { display: grid; gap: 14px; max-width: 520px; }
.user-form label:not(.inline) { display: grid; gap: 6px; }
.user-form .inline { display: flex; gap: 8px; align-items: center; }
@media (max-width: 760px) { .users-layout { grid-template-columns: 1fr; } }
</style>
