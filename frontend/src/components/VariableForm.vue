<script setup lang="ts">
import { reactive, ref } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

const store = useWorkspaceStore()
const original = store.service?.variableEditor()
const form = reactive({
  key: original?.key || '',
  type: original?.type || 'string',
  value: original?.value || '',
  note: original?.note || '',
})
const error = ref('')
const types = [
  { value: 'string', label: 'variables.text' },
  { value: 'integer', label: 'variables.integer' },
  { value: 'boolean', label: 'variables.boolean' },
  { value: 'memory', label: 'variables.memory' },
  { value: 'credential', label: 'variables.credential' },
]
function submit() {
  error.value = store.service?.stageVariable({ ...form }) || ''
}
</script>

<template>
  <form @submit.prevent="submit">
    <div class="modal-body">
      <div class="field">
        <label class="field-label" for="y-key">{{ t('archive.message_9d9d763731b4') }}</label><input
          id="y-key"
          v-model="form.key"
          :readonly="Boolean(original?.key)"
          :placeholder="t('archive.message_7903a5a9663f')"
          autocomplete="off"
          @input="error = ''"
        />
      </div>
      <div class="field">
        <label class="field-label" for="y-type">{{ t('archive.message_29566d835918') }}</label><select id="y-type" v-model="form.type" @change="error = ''">
          <option v-for="type in types" :key="type.value" :value="type.value">
            {{ t(type.label) }}
          </option>
        </select>
      </div>
      <div class="field">
        <label class="field-label" for="y-value">{{ t('archive.message_33fe0d5d2a91') }}</label><input
          id="y-value"
          v-model="form.value"
          :placeholder="t('archive.message_c873af2b84f9')"
          autocomplete="off"
          @input="error = ''"
        />
        <p class="hint">{{ t('archive.message_2f4073470a9a') }}</p>
      </div>
      <div class="field">
        <label class="field-label" for="y-note">{{ t('variables.usage_note') }}</label><input id="y-note" v-model="form.note" maxlength="160" @input="error = ''" />
      </div>
      <p class="callout blue">{{ t('archive.message_6a411e07f568') }}</p>
      <div id="y-var-error" class="mt12" role="alert">
        <p v-if="error" class="callout red">{{ error }}</p>
      </div>
    </div>
    <div class="modal-foot">
      <button class="btn" type="button" @click="store.service?.closeModal()">
        {{ t('console.cancel') }}
      </button><button class="btn primary" type="submit" data-action="y-var-apply" data-vue-control="true">
        {{ t('variables.stage_change') }}
      </button>
    </div>
  </form>
</template>
