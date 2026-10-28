<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

const store = useWorkspaceStore()
const editor = computed(() => {
  void store.revision
  return store.service?.composeEditorState()
})
const references = computed(() => {
  const entries = editor.value?.compiled.refs || []
  return [...new Map(entries.map((entry) => [entry.key, entry])).values()].map((entry) => ({
    ...entry,
    count: entries.filter((ref) => ref.key === entry.key).length,
  }))
})
const previewName = computed(
  () =>
    editor.value?.environments.find(
      (environment) => environment.id === editor.value?.previewEnvironment,
    )?.name ||
    editor.value?.previewEnvironment ||
    '',
)
const views = [
  { key: 'source', label: 'compose.source_view' },
  { key: 'resolved', label: 'compose.resolved_view' },
  { key: 'runtime', label: 'compose.runtime_view' },
  { key: 'diff', label: 'compose.diff_view' },
]
function selectPreview(event: Event) {
  store.service?.setComposePreviewEnvironment((event.target as HTMLSelectElement).value)
}
function updateSource(event: Event) {
  store.service?.updateComposeSource((event.target as HTMLTextAreaElement).value)
}
function insertIndent(event: KeyboardEvent) {
  const input = event.target as HTMLTextAreaElement
  input.setRangeText('  ', input.selectionStart, input.selectionEnd, 'end')
  store.service?.updateComposeSource(input.value)
}
</script>

<template>
  <template v-if="editor">
    <div class="breadcrumbs">
      <span class="current">{{ editor.projectName }} / {{ editor.environmentName }} / {{ editor.name }}</span>
    </div>
    <div class="x-head">
      <div>
        <div class="x-tagline">WORKSPACE / 1.3</div>
        <h1>{{ editor.name }}{{ t('archive.message_f013e8b3c155') }}</h1>
        <p>{{ t('prototype.compose_intro') }}</p>
      </div>
      <div class="hero-actions">
        <button
          class="btn"
          type="button"
          data-action="x-app"
          data-vue-control="true"
          :data-id="editor.id"
          @click.stop="store.service?.openApplication(editor.id)"
        >
          {{ t('prototype.application_details') }}
        </button>
        <button
          class="btn"
          type="button"
          data-action="y-compose-save"
          data-vue-control="true"
          :disabled="!editor.managed"
          @click.stop="store.service?.saveComposeDocument()"
        >
          {{ t('prototype.save_draft') }}
        </button>
        <button
          class="btn primary"
          type="button"
          data-action="y-compose-review"
          data-vue-control="true"
          :disabled="!editor.managed || editor.previewEnvironment !== editor.environment"
          @click.stop="store.service?.reviewComposeDocument()"
        >
          {{ t('compose.review_deployment') }}
        </button>
      </div>
    </div>
    <div class="y-scopebar card">
      <strong>{{ editor.runtimeName }}</strong><span class="badge blue">{{
        editor.runtimeType === 'swarm' ? 'Swarm Stack' : 'Docker Compose'
      }}</span><span class="spacer"></span>
      <label for="y-preview-env">{{ t('compose.preview_environment') }}</label>
      <select
        id="y-preview-env"
        data-vue-control="true"
        :value="editor.previewEnvironment"
        @change.stop="selectPreview"
      >
        <option
          v-for="environment in editor.environments"
          :key="environment.id"
          :value="environment.id"
        >
          {{ environment.name
          }}{{ environment.id === editor.environment ? t('archive.message_d0a752fd2a0b') : '' }}
        </option>
      </select>
      <span :class="['badge', editor.dirty ? 'amber' : 'green']">{{
        editor.dirty
          ? t('archive.message_b8eabf48e188')
          : editor.savedDraft
            ? t('compose.saved')
            : t('prototype.saved')
      }}</span>
    </div>
    <div v-if="editor.previewEnvironment !== editor.environment" class="y-note amber mt12">
      {{ t('archive.message_5b3a6221bede', { v0: previewName }) }}
    </div>
    <div class="y-compose-layout mt16">
      <section class="card y-editor-card">
        <div class="y-tabs" role="tablist">
          <button
            v-for="view in views"
            :key="view.key"
            role="tab"
            type="button"
            data-action="y-view"
            data-vue-control="true"
            :data-id="view.key"
            :aria-selected="editor.view === view.key"
            :class="{ active: editor.view === view.key }"
            @click.stop="store.service?.setComposeView(view.key)"
          >
            {{ t(view.label) }}
          </button>
        </div>
        <template v-if="editor.view === 'source'">
          <div class="y-code-toolbar">
            <div class="y-files">
              <button
                type="button"
                data-action="y-file"
                data-vue-control="true"
                data-id="compose"
                :class="{ active: editor.file === 'compose' }"
                @click.stop="store.service?.setComposeFile('compose')"
              >
                docker-compose.yml
              </button><button
                type="button"
                data-action="y-file"
                data-vue-control="true"
                data-id="runtime"
                :class="{ active: editor.file === 'runtime' }"
                @click.stop="store.service?.setComposeFile('runtime')"
              >
                runtime.env
              </button>
            </div>
            <span class="badge blue">{{
              editor.managed ? t('archive.message_d4d60b4189aa') : t('archive.message_175bd630e1df')
            }}</span>
          </div>
          <textarea
            id="y-compose-code"
            class="y-editor"
            data-vue-control="true"
            spellcheck="false"
            :aria-label="
              editor.file === 'compose'
                ? t('archive.message_e74968fbfb85')
                : t('archive.message_f63e28851e18')
            "
            :readonly="!editor.managed"
            :value="editor.source"
            @input.stop="updateSource"
            @keydown.tab.prevent="insertIndent"
          ></textarea>
          <div :class="['y-editor-status', { changed: editor.dirty }]">
            <span>{{
              editor.file === 'compose'
                ? t('archive.message_b7a111a91c5d')
                : t('archive.message_d3733dc79547')
            }}</span><span>{{ t('archive.message_c0ceeb704316') }}</span>
          </div>
        </template>
        <template v-else-if="editor.view === 'resolved'">
          <div class="y-code-toolbar">
            <b>{{ t('archive.message_811b3a7ac58d') }}</b><span class="badge amber">{{ t('archive.message_b6477faec180') }}</span>
          </div>
          <pre class="y-rendered">{{
            editor.compiled.text || editor.compiled.errors.join('\n')
          }}</pre>
        </template>
        <div v-else-if="editor.view === 'runtime'" class="y-runtime-env">
          <section v-for="runtime in editor.compiled.runtime" :key="runtime.service" class="mb16">
            <h3>{{ runtime.service }}</h3>
            <table class="x-table mt12">
              <thead>
                <tr>
                  <th>{{ t('archive.message_5e72f45926be') }}</th>
                  <th>{{ t('compose.declared_value') }}</th>
                  <th>{{ t('delivery.source') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="[key, value] in Object.entries(runtime.env)" :key="key">
                  <td>
                    <code>{{ key }}</code>
                  </td>
                  <td>
                    <code>{{ value.value }}</code>
                  </td>
                  <td>{{ value.source }}</td>
                </tr>
                <tr v-if="Object.keys(runtime.env).length === 0">
                  <td colspan="3">{{ t('archive.message_504cad3a55c6') }}</td>
                </tr>
              </tbody>
            </table>
            <p class="hint">{{ t('archive.message_6200d64fbd66') }}</p>
          </section>
        </div>
        <div v-else class="y-diff">
          <section>
            <h4>{{ t('archive.message_3793bd951865') }}</h4>
            <pre>{{ editor.lastRendered || t('archive.message_015aa8d5cc3a') }}</pre>
          </section>
          <section>
            <h4>{{ t('archive.message_b3b2c1e6ac98') }}</h4>
            <pre>{{ editor.compiled.text || t('archive.message_f9602c0ca6c4') }}</pre>
          </section>
        </div>
      </section>
      <aside class="y-inspector">
        <section class="card x-pad">
          <h3>{{ t('compose.parameter_check') }}</h3>
          <div id="y-compile-summary">
            <div :class="['y-check-result', { 'has-error': editor.compiled.errors.length }]">
              <strong>{{
                editor.compiled.errors.length
                  ? editor.compiled.errors.length + t('archive.message_346cc6fd9b0a')
                  : t('archive.message_d00bfbb98e27')
              }}</strong><span>{{
                editor.compiled.errors.length
                  ? t('archive.message_5d66b62e957f')
                  : t('archive.message_fbfd99aabebe')
              }}</span>
            </div>
            <p v-for="(error, index) in editor.compiled.errors" :key="index" class="y-error">
              {{ error }}
            </p>
            <details v-if="editor.compiled.warnings.length" class="mt12">
              <summary>
                {{ t('archive.message_d59b21968d5d', { v4: editor.compiled.warnings.length }) }}
              </summary>
              <p v-for="(warning, index) in editor.compiled.warnings" :key="index" class="hint">
                {{ warning }}
              </p>
            </details>
          </div>
          <p class="hint">
            {{
              t('archive.message_8029ca8a9b8e', {
                v8: editor.versions.project,
                v9: editor.versions.environment,
                v10:
                  editor.versions.application === null
                    ? ''
                    : t('archive.message_59208d70cb75') + editor.versions.application,
              })
            }}
          </p>
        </section>
        <section class="card x-pad mt16">
          <div class="flex">
            <h3>{{ t('compose.file_references') }}</h3>
            <span class="spacer"></span><span class="kbd">{{ references.length }}</span>
          </div>
          <div class="y-ref-list">
            <div v-for="reference in references" :key="reference.key">
              <code>{{ reference.key }}</code><b>{{ reference.value || t('archive.message_4d69dcfa86c3') }}</b><small>{{
                t('archive.message_edd0ec2a74d9', { v2: reference.source, v3: reference.count })
              }}</small>
            </div>
            <p v-if="references.length === 0" class="hint">
              {{ t('archive.message_ba8c8e2d4979') }}
            </p>
          </div>
          <button
            class="btn sm"
            type="button"
            data-action="y-vars-from-compose"
            data-vue-control="true"
            @click.stop="store.service?.openComposeVariables()"
          >
            {{ t('archive.message_da7167137306') }}
          </button>
        </section>
        <section class="card x-pad mt16">
          <h3>{{ t('compose.change_boundary') }}</h3>
          <p class="hint">{{ t('archive.message_3b9c34338542') }}</p>
          <p class="hint">{{ t('archive.message_4394fa412ad7') }}</p>
        </section>
      </aside>
    </div>
  </template>
  <div v-else class="x-empty">{{ t('archive.message_bf106a8a47d7') }}</div>
</template>
