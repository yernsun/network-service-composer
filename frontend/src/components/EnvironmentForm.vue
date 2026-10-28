<script setup lang="ts">
import { computed, reactive } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

const props = defineProps<{ envId?: string | undefined }>()
const store = useWorkspaceStore()
const original = store.state().environments.find((environment) => environment.id === props.envId)
const form = reactive({
  name: original?.name || '',
  slug: original?.slug || '',
  kind: original?.kind || 'custom',
  gateways: [...(original?.gateways || [])],
  revision: original?.revision,
})
const choices = [
  { id: 'production', label: 'archive.message_76ab7d3b41f2' },
  { id: 'development', label: 'archive.message_9ec428625fee' },
  { id: 'testing', label: 'archive.message_6aa8f49cc992' },
  { id: 'sandbox', label: 'archive.message_2a8ce2b15d65' },
  { id: 'custom', label: 'archive.message_4eafa9e925b3' },
]
const gateways = computed(() => store.state().gateways || [])
const environments = computed(() => store.state().environments)
const error = computed(() => {
  void store.revision
  return store.service?.formErrorMessage || ''
})
function submit() {
  store.service?.saveEnvironment({ ...form, gateways: [...form.gateways] })
}
</script>

<template>
  <form @submit.prevent="submit">
    <div class="modal-body">
      <div class="field">
        <label class="field-label" for="p-env-name">{{ t('archive.message_ece0ecfa2b4a') }}</label><input
          id="p-env-name"
          v-model="form.name"
          :placeholder="t('archive.message_750d90c4d05a')"
          maxlength="40"
          autofocus
        />
      </div>
      <div class="field">
        <label class="field-label" for="p-env-slug">{{ t('archive.message_c71c47dba2f0') }}</label><input
          id="p-env-slug"
          v-model="form.slug"
          placeholder="acceptance"
          :disabled="Boolean(envId)"
        />
        <p class="hint">{{ t('archive.message_315c46d997bc') }}</p>
      </div>
      <div class="field">
        <label class="field-label" for="p-env-kind">{{ t('archive.message_331d62d0994b') }}</label><select id="p-env-kind" v-model="form.kind">
          <option v-for="choice in choices" :key="choice.id" :value="choice.id">
            {{ t(choice.label) }}
          </option>
        </select>
        <p class="hint">{{ t('archive.message_3bcef52886e4') }}</p>
      </div>
      <div class="section-title">{{ t('archive.message_68d89fbaf4bc') }}</div>
      <div class="gateway-choices">
        <label v-for="gateway in gateways" :key="gateway.id" class="gateway-choice"><input v-model="form.gateways" type="checkbox" name="p-gateway" :value="gateway.id" />
          <div>
            <strong>{{ gateway.name }}</strong><span>{{
              t('archive.message_13980c2eb510', {
                v4: gateway.runtime,
                v5: environments.filter((environment) => environment.gateways.includes(gateway.id))
                  .length,
              })
            }}</span>
          </div></label>
      </div>
      <p class="hint mt12">{{ t('archive.message_f6db334323ff') }}</p>
      <p class="callout blue">{{ t('archive.message_2f19902d6b52') }}</p>
      <div id="p-form-error" class="mt12" role="alert">
        <p v-if="error" class="callout red">{{ error }}</p>
      </div>
    </div>
    <div class="modal-foot">
      <button
        v-if="envId"
        class="btn danger"
        type="button"
        data-action="p-env-delete"
        data-vue-control="true"
        :data-id="envId"
        @click="store.service?.deleteEnvironment(envId)"
      >
        {{ t('archive.message_ad8665a2fbc1') }}
      </button>
      <button v-else class="btn" type="button" @click="store.service?.closeModal()">
        {{ t('console.cancel') }}
      </button>
      <button class="btn primary" type="submit" data-action="p-env-save" data-vue-control="true">
        {{ t(envId ? 'archive.message_fc7085c7e729' : 'archive.message_006c14521a30') }}
      </button>
    </div>
  </form>
</template>
