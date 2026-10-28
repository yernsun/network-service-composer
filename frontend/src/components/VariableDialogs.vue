<script setup lang="ts">
import { computed, ref } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

defineProps<{ kind: string }>()
const store = useWorkspaceStore()
const dialog = computed(() => {
  void store.revision
  return store.service?.variableDialogState()
})
const raw = ref('')
const importResult = ref<{ errors: string[]; names: string[]; overwrite: number } | null>(null)
function changeImport(event: Event) {
  raw.value = (event.target as HTMLTextAreaElement).value
  importResult.value = null
  store.service?.invalidateVariableImport()
}
function analyzeImport() {
  importResult.value = store.service?.analyzeVariableImport(raw.value) || null
}
</script>

<template>
  <template v-if="dialog">
    <div class="modal-body">
      <template v-if="kind === 'y-env-import'">
        <textarea
          id="y-import"
          class="y-env-area"
          data-vue-control="true"
          spellcheck="false"
          placeholder="IMAGE_TAG=1.3.0&#10;MEMORY_LIMIT=1G&#10;LOG_LEVEL=info"
          :value="raw"
          @input.stop="changeImport"
        ></textarea>
        <p class="hint">{{ t('archive.message_7eeeb1e88a0f') }}</p>
        <div id="y-import-result" role="status">
          <p v-for="error in importResult?.errors || []" :key="error" class="callout red">
            {{ error }}
          </p>
          <template v-if="importResult?.names.length">
            <div class="y-note mt12">
              {{
                t('archive.message_df6185140763', {
                  v0: importResult.names.length,
                  v1: importResult.overwrite,
                })
              }}
            </div>
            <div class="y-tags">
              <code v-for="name in importResult.names" :key="name">{{ name }}</code>
            </div>
          </template>
        </div>
      </template>
      <p v-else-if="kind === 'y-remove'" class="callout amber">
        {{
          dialog.layer === 'project'
            ? t('archive.message_a6815db95ff5') +
              dialog.removeReferences +
              t('archive.message_68c9cedea807')
            : t('archive.message_17ca48ca65bb')
        }}
      </p>
      <template v-else-if="kind === 'y-usage'">
        <div class="table-scroll">
          <table class="x-table">
            <thead>
              <tr>
                <th>{{ t('archive.message_63c73c4730f4') }}</th>
                <th>{{ t('prototype.environment_group') }}</th>
                <th>{{ t('archive.message_62881fcb1b69') }}</th>
                <th>{{ t('archive.message_5f70a21d01d5') }}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in dialog.usage" :key="item.id">
                <td>{{ item.application }}</td>
                <td>{{ item.environment }}</td>
                <td>{{ item.source }}</td>
                <td>
                  {{
                    item.shadowed
                      ? t('archive.message_ed87c67d2fb7')
                      : t('archive.message_5c88da6ce4bb')
                  }}
                </td>
                <td>
                  <button
                    class="btn sm"
                    type="button"
                    data-action="y-open-compose"
                    data-vue-control="true"
                    :data-id="item.id"
                    @click.stop="store.service?.openComposeDocument(item.id)"
                  >
                    {{ t('archive.message_ac05e8899ab4') }}
                  </button>
                </td>
              </tr>
              <tr v-if="dialog.usage.length === 0">
                <td colspan="5" class="x-empty">{{ t('archive.message_faf3d5ed5313') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
      <template v-else-if="kind === 'y-history'">
        <p class="callout blue">{{ t('archive.message_9bdde09f8294') }}</p>
        <div class="mt16">
          <details v-for="item in dialog.history" :key="item.version" class="y-history-item">
            <summary>
              {{
                t('archive.message_133b4627776b', {
                  v0: item.version,
                  v1: item.time,
                  v2: item.note,
                })
              }}
            </summary>
            <pre class="y-mini-code">{{ item.rows }}</pre>
          </details>
          <p v-if="dialog.history.length === 0">{{ t('archive.message_d0a722f4dc49') }}</p>
        </div>
      </template>
    </div>
    <div class="modal-foot">
      <button class="btn" type="button" @click="store.service?.closeModal()">
        {{
          kind === 'y-remove' || kind === 'y-env-import' ? t('console.cancel') : t('console.close')
        }}
      </button>
      <button
        v-if="kind === 'y-remove'"
        class="btn danger"
        type="button"
        data-action="y-var-remove-confirm"
        data-vue-control="true"
        :data-key="dialog.removeKey"
        @click.stop="store.service?.confirmVariableRemoval(dialog.removeKey)"
      >
        {{ t('variables.stage_change') }}
      </button>
      <button
        v-if="kind === 'y-env-import' && !importResult?.names.length"
        class="btn primary"
        type="button"
        data-action="y-import-analyze"
        data-vue-control="true"
        @click.stop="analyzeImport"
      >
        {{ t('archive.message_ed488445de43') }}
      </button>
      <button
        v-if="kind === 'y-env-import' && importResult?.names.length"
        class="btn primary"
        type="button"
        data-action="y-import-apply"
        data-vue-control="true"
        @click.stop="store.service?.applyVariableImport()"
      >
        {{ t('archive.message_0670158b4a30') }}
      </button>
    </div>
  </template>
</template>
