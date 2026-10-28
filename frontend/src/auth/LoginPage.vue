<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { signIn, errorText, ApiRequestError } from '../shared/api/client'
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const username = ref('admin')
const password = ref('')
const busy = ref(false)
const error = ref('')
async function submit() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await signIn(username.value, password.value)
    const next = String(route.query.next || '/projects')
    await router.replace(next.startsWith('/') && !next.startsWith('//') && !next.includes('\\') && !next.startsWith('/login') && !next.startsWith('/logout') ? next : '/projects')
  } catch (e) {
    error.value = e instanceof ApiRequestError && e.status === 401 ? t('login.invalid_credentials') : errorText(e)
  } finally {
    password.value = ''
    busy.value = false
  }
}
</script>
<template>
  <main class="auth-page">
    <form class="card auth-card" @submit.prevent="submit">
      <h1>Network Service Composer</h1>
      <h2>{{ t('login.heading') }}</h2>
      <p v-if="error" class="callout red" role="alert">{{ error }}</p>
      <label for="username">{{ t('login.username') }}</label>
      <input id="username" v-model="username" name="username" autocomplete="username" required autofocus>
      <label for="password">{{ t('login.password') }}</label>
      <input id="password" v-model="password" name="password" type="password" autocomplete="current-password" required>
      <button class="btn primary" type="submit" :disabled="busy">{{ t('login.submit') }}</button>
    </form>
  </main>
</template>
