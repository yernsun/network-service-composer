<script setup lang="ts">
import { ref } from 'vue'
import ApiKeysPanel from '../components/ApiKeysPanel.vue'
import NotificationSettings from '../components/NotificationSettings.vue'
import NotificationRouting from '../components/NotificationRouting.vue'
import { asRow, errorText, request, session } from '../shared/api/client'
import { translate as t } from '../i18n'

const current = ref(''), replacement = ref(''), confirm = ref(''), busy = ref(false), error = ref(''), success = ref(false)
async function changePassword() {
  if (busy.value) return
  error.value = ''; success.value = false
  if (replacement.value !== confirm.value) { error.value = t('account.passwordMismatch'); return }
  busy.value = true
  try {
    const response = await request('post', '/me/password', { body: { current_password: current.value, new_password: replacement.value } })
    const csrf = asRow(response.data).csrf_token
    if (typeof csrf === 'string') session.value = { ...session.value, csrf_token: csrf }
    current.value = ''; replacement.value = ''; confirm.value = ''; success.value = true
  } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <div class="x-head"><div><h1>{{ t('account.title') }}</h1><p>{{ session.username }}</p></div></div>
  <section class="card account-card">
    <h2>{{ t('account.changePassword') }}</h2>
    <p class="muted">{{ t('account.sessionNotice') }}</p>
    <div v-if="error" class="callout red" role="alert">{{ error }}</div>
    <div v-if="success" class="callout green" role="status">{{ t('account.passwordChanged') }}</div>
    <form class="account-form" @submit.prevent="changePassword">
      <label>{{ t('account.currentPassword') }}<input v-model="current" type="password" autocomplete="current-password" required /></label>
      <label>{{ t('account.newPassword') }}<input v-model="replacement" type="password" autocomplete="new-password" minlength="16" maxlength="72" required /></label>
      <label>{{ t('account.confirmPassword') }}<input v-model="confirm" type="password" autocomplete="new-password" required /></label>
      <button class="btn primary" type="submit" :disabled="busy">{{ t('account.savePassword') }}</button>
    </form>
  </section>
  <ApiKeysPanel v-if="session.user_id" :user-id="session.user_id" />
  <NotificationSettings v-if="session.role === 'administrator'" />
  <NotificationRouting v-if="session.role === 'administrator'" />
</template>

<style scoped>
.account-card { padding: 22px; }
.account-form { display: grid; gap: 14px; max-width: 480px; margin-top: 18px; }
.account-form label { display: grid; gap: 6px; }
</style>
