<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

const store = useWorkspaceStore()
const variables = computed(() => {
  void store.revision
  return store.service?.variableState()
})
const rows = computed(
  () =>
    variables.value?.rows.filter((row) =>
      row.name.toLowerCase().includes(variables.value?.query.toLowerCase() || ''),
    ) || [],
)
const typeLabels: Record<string, string> = {
  string: 'variables.text',
  integer: 'variables.integer',
  boolean: 'variables.boolean',
  memory: 'variables.memory',
  credential: 'variables.credential',
}
const layers = [
  { key: 'project', label: 'prototype.project_default' },
  { key: 'environment', label: 'prototype.environment_override' },
  { key: 'application', label: 'variables.application_overrides' },
]
function selectApp(event: Event) {
  store.service?.selectVariableApplication((event.target as HTMLSelectElement).value)
}
function search(event: Event) {
  store.service?.updateVariableSearch((event.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="breadcrumbs">
    <span class="current">{{ variables?.projectName }} / {{ variables?.environmentName }}</span>
  </div>
  <div class="x-head">
    <div>
      <div class="x-tagline">WORKSPACE / 1.3</div>
      <h1>{{ t('prototype.variables_configuration') }}</h1>
      <p>{{ t('prototype.variables_intro') }}</p>
    </div>
    <div class="hero-actions">
      <button
        class="btn"
        type="button"
        data-action="y-var-history"
        data-vue-control="true"
        @click.stop="store.service?.openVariableHistory()"
      >
        {{ t('archive.message_e122144e1587') }}
      </button>
      <button
        class="btn"
        type="button"
        data-action="y-env-import"
        data-vue-control="true"
        @click.stop="store.service?.openVariableImport()"
      >
        {{ t('variables.import_env') }}
      </button>
      <button
        class="btn"
        type="button"
        data-action="y-var-add"
        data-vue-control="true"
        @click.stop="store.service?.openVariableEditor()"
      >
        {{ t('delivery.add_variable') }}
      </button>
      <button
        class="btn primary"
        type="button"
        data-action="y-vars-save"
        data-vue-control="true"
        :disabled="!variables?.dirty"
        @click.stop="store.service?.saveVariables()"
      >
        {{ t('prototype.save_variables') }}
      </button>
    </div>
  </div>
  <div class="y-note">
    <b>{{ t('prototype.variable_save_boundary_title') }}</b>{{ t('archive.message_367ef72ac4fe') }}
  </div>
  <section v-if="variables" class="card mt16">
    <div class="y-scopebar">
      <div class="y-segments">
        <button
          v-for="layer in layers"
          :key="layer.key"
          type="button"
          data-action="y-layer"
          data-vue-control="true"
          :data-id="layer.key"
          :class="{ selected: variables.layer === layer.key }"
          :aria-pressed="variables.layer === layer.key"
          @click.stop="store.service?.selectVariableLayer(layer.key)"
        >
          {{ t(layer.label) }}
        </button>
      </div>
      <select
        v-if="variables.layer === 'application'"
        id="y-var-app"
        data-vue-control="true"
        :value="variables.app"
        :aria-label="t('archive.message_46e5eb29c7d4')"
        @change.stop="selectApp"
      >
        <option v-for="app in variables.apps" :key="app.id" :value="app.id">{{ app.name }}</option>
      </select>
      <span v-else class="muted">{{
        variables.layer === 'project'
          ? t('archive.message_f97a3af28a78')
          : variables.environmentName + t('archive.message_eaaefdaa60d7')
      }}</span>
      <span class="spacer"></span>
      <span class="badge">{{ t('archive.message_1aa7826fd0fb') }}{{ variables.version }}</span>
      <span :class="['badge', variables.dirty ? 'amber' : 'green']">{{
        variables.dirty ? t('archive.message_b8eabf48e188') : t('prototype.saved')
      }}</span>
    </div>
    <div class="y-priority">
      <span>{{ t('prototype.priority') }}</span><b>{{ t('variables.application_overrides') }}</b><span>›</span><b>{{ t('prototype.environment_override') }}</b><span>›</span><b>{{ t('prototype.project_default') }}</b><span>›</span><span>{{ t('prototype.file_default') }}</span><small>{{ t('prototype.priority_boundary') }}</small>
    </div>
    <div class="y-toolbar">
      <input
        id="y-var-search"
        data-vue-control="true"
        :value="variables.query"
        :placeholder="t('archive.message_4756dce9666d')"
        :aria-label="t('archive.message_d21ed6602317')"
        @input.stop="search"
      />
      <span class="muted">{{
        t('archive.message_a93a9dbf4057', { v2: rows.length, v3: variables.ownCount })
      }}</span>
    </div>
    <div class="table-scroll">
      <table class="x-table y-var-table">
        <thead>
          <tr>
            <th>{{ t('archive.message_a66429702f0f') }}</th>
            <th>{{ t('archive.message_1220fd233a03') }}</th>
            <th>{{ t('archive.message_ba40014ff496') }}</th>
            <th>{{ t('delivery.source') }}</th>
            <th>{{ t('variables.references') }}</th>
            <th>{{ t('community.action') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.name">
            <td>
              <code>{{ row.name }}</code><small>{{
                row.note ||
                  t('archive.message_6460a352bfdb') + row.name + t('archive.message_ae500aacbd91')
              }}</small>
            </td>
            <td>
              <span v-if="row.type === 'credential'" class="y-secret">{{
                t('archive.message_9c984eba974c', { v0: '🔒' })
              }}</span><code v-else class="y-value">{{
                row.value || t('archive.message_4b8da304f324')
              }}</code>
            </td>
            <td>{{ t(typeLabels[row.type] || 'variables.text') }}</td>
            <td>
              <span :class="['badge', row.own ? 'green' : 'blue']">{{ row.source }}</span><small>{{
                row.own ? t('archive.message_da1b9bc35340') : t('archive.message_808f51b588e2')
              }}</small>
            </td>
            <td>
              <button
                class="row-link"
                type="button"
                data-action="y-var-usage"
                data-vue-control="true"
                :data-key="row.name"
                @click.stop="store.service?.openVariableUsage(row.name)"
              >
                {{ t('archive.message_a9c01779b28f', { v7: row.references }) }}
              </button>
            </td>
            <td>
              <div class="flex">
                <button
                  class="btn sm"
                  type="button"
                  data-action="y-var-edit"
                  data-vue-control="true"
                  :data-key="row.name"
                  @click.stop="store.service?.openVariableEditor(row.name)"
                >
                  {{ row.own ? t('console.edit') : t('variables.override_here') }}
                </button><button
                  v-if="row.own"
                  class="iconbtn"
                  type="button"
                  data-action="y-var-remove"
                  data-vue-control="true"
                  :data-key="row.name"
                  :aria-label="
                    variables.layer === 'project'
                      ? t('archive.message_ba6b00df1df2')
                      : t('archive.message_159d6f5759c5')
                  "
                  @click.stop="store.service?.requestVariableRemoval(row.name)"
                >
                  ×
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="rows.length === 0">
            <td colspan="6" class="x-empty">{{ t('archive.message_6cbf3ffa275c') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
  <div class="y-two mt16">
    <section class="card x-pad">
      <h3>{{ t('prototype.compose_parameters') }}</h3>
      <p class="muted mt8">{{ t('prototype.compose_parameters_help') }}</p>
      <pre class="y-mini-code">
image: "demo/api:${IMAGE_TAG}"
deploy:
  replicas: ${REPLICAS:-1}</pre>
    </section>
    <section class="card x-pad">
      <h3>{{ t('compose.runtime_view') }}</h3>
      <p class="muted mt8">{{ t('prototype.container_environment_help') }}</p>
      <pre class="y-mini-code">
environment:
  LOG_LEVEL: "${LOG_LEVEL:-info}"
env_file:
  - runtime.env</pre>
    </section>
  </div>
</template>
