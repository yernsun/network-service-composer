<script setup lang="ts">
import { computed, reactive } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

const store = useWorkspaceStore()
const form = reactive({ name: '', key: '', description: '', runtime: '' })
const runtimes = computed(() =>
  store.state().runtimes.filter((runtime) => runtime.enabled !== false && runtime.canWrite),
)
const error = computed(() => {
  void store.revision
  return store.service?.formErrorMessage || ''
})
function submit() {
  store.service?.createProject({ ...form })
}
</script>

<template>
  <form @submit.prevent="submit">
    <div class="modal-body">
      <div class="field">
        <label class="field-label" for="p-name">{{ t('archive.message_867698fb7b0c') }}</label><input
          id="p-name"
          v-model="form.name"
          :placeholder="t('archive.message_b5201a5057c0')"
          maxlength="64"
          autofocus
        />
      </div>
      <div class="field">
        <label class="field-label" for="p-key">{{ t('delivery.slug') }}</label><input id="p-key" v-model="form.key" placeholder="customer-portal" maxlength="48" />
        <p class="hint">{{ t('archive.message_5af47de1957d') }}</p>
      </div>
      <div class="field">
        <label class="field-label" for="p-description">{{
          t('archive.message_19ad97a6aca8')
        }}</label><textarea
          id="p-description"
          v-model="form.description"
          rows="3"
          maxlength="240"
          :placeholder="t('archive.message_6e617d7dc278')"
        />
      </div>
      <div class="field project-runtime-field">
        <label class="field-label" for="p-runtime">{{ t('projectLifecycle.runtime') }}</label><select id="p-runtime" v-model="form.runtime" aria-describedby="p-runtime-hint">
          <option value="">{{ t('projectLifecycle.runtimeAutomatic') }}</option>
          <option v-for="runtime in runtimes" :key="runtime.id" :value="runtime.id">
            {{ runtime.name }}
          </option>
        </select>
        <p id="p-runtime-hint" class="hint">{{ t('projectLifecycle.createRuntimeHint') }}</p>
      </div>
      <div class="preset-summary">
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="m12 3 9 5-9 5-9-5 9-5Zm-9 5v9l9 5 9-5V8M12 13v9M7.5 5.5l9 5" />
        </svg>
        <div>
          <strong>{{ t('archive.message_f66af0cb337e') }}</strong>
          <p>{{ t('archive.message_8e64742a21a2') }}</p>
        </div>
      </div>
      <div id="p-form-error" class="mt12" role="alert">
        <p v-if="error" class="callout red">{{ error }}</p>
      </div>
    </div>
    <div class="modal-foot">
      <button class="btn" type="button" @click="store.service?.closeModal()">
        {{ t('console.cancel') }}
      </button><button
        class="btn primary"
        type="submit"
        data-action="p-project-create"
        data-vue-control="true"
      >
        {{ t('archive.message_43d7d2b51d75') }}
      </button>
    </div>
  </form>
</template>
