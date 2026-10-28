<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { errorText, request, session } from '../shared/api/client'
const router = useRouter()
const { t } = useI18n()
const error = ref('')
const busy = ref(false)
async function logout() {
  if (busy.value) return
  busy.value = true
  try {
    await request('post', '/auth/logout')
    session.value = { authenticated: false, locale: session.value.locale, supported_languages: session.value.supported_languages }
    window.dispatchEvent(new Event('nsc:unauthorized'))
    await router.replace('/login')
  } catch (cause) { error.value = errorText(cause) }
  finally { busy.value = false }
}
onMounted(logout)
</script>
<template>
  <main class="auth-page">
    <section class="card auth-card">
      <h1>{{ t('nav.sign_out') }}</h1>
      <p v-if="error" class="callout red" role="alert">{{ error }}</p>
      <button v-if="error" class="btn primary" :disabled="busy" @click="logout">{{ t('console.retry') }}</button>
    </section>
  </main>
</template>
