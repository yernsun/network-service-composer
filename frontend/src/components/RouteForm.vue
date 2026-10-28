<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import type { RouteRule } from '../types/domain'
import ViewContent from './ViewContent.vue'

const store = useWorkspaceStore()
const initial = store.service?.routeEditor()
if (!initial) throw new Error('Route editor requires an active route')
const domainName = initial.domainName
const form = reactive({ ...initial.route })
const targetMode = ref(initial.targetMode)
const error = computed(() => {
  void store.revision
  return store.service?.routeEditor()?.error || ''
})
const upstreamPath = computed(() =>
  form.strip ? '/users' : `${form.path.replace(/\/$/, '')}/users`,
)
const codes = computed(() =>
  form.kind === 'return' ? [200, 204, 403, 404, 503] : [301, 302, 307, 308],
)
function icon(name: string) {
  return store.service?.iconView(name)
}
function patch(values: Partial<RouteRule>) {
  Object.assign(form, values)
  store.service?.updateRouteForm(values)
}
function input(field: keyof RouteRule, event: Event) {
  patch({ [field]: (event.target as HTMLInputElement).value })
}
function numberInput(field: 'timeout' | 'connect' | 'code', event: Event) {
  patch({ [field]: Number((event.target as HTMLInputElement).value) })
}
function changeKind(kind: RouteRule['kind']) {
  patch(
    kind === 'return'
      ? { kind, code: 200, value: 'ok' }
      : kind === 'redirect'
        ? { kind, code: 308, value: `https://${domainName}/` }
        : { kind },
  )
}
function changeTargetMode(mode: string) {
  targetMode.value = mode
  if (mode === 'service') patch({ externalUrl: '' })
  store.service?.updateRouteTargetMode(mode)
}
function toggle(field: 'strip' | 'websocket' | 'enabled') {
  patch({ [field]: !form[field] })
}
</script>

<template>
  <div class="modal-body">
    <div class="section-title">
      <span class="num">1</span>{{ t('archive.message_291db957ff0b') }}
    </div>
    <div class="field">
      <label class="field-label" for="rf-path">{{ t('archive.message_0979645cfe4f') }} <span class="required">*</span></label><input
        id="rf-path"
        :value="form.path"
        data-rf="path"
        placeholder="/api/"
        autocomplete="off"
        @input.stop="input('path', $event)"
      />
      <div class="hint">{{ t('archive.message_8f67fb22c8e7') }}</div>
    </div>
    <div class="field">
      <label class="field-label" for="rf-match">{{ t('archive.message_e1f0f1cb1ab4') }}</label><select
        id="rf-match"
        :value="form.match"
        data-rf="match"
        @change.stop="input('match', $event)"
      >
        <option v-for="option in initial.matches" :key="option.value" :value="option.value">
          {{ option.label }}{{ option.value === 'prefix' ? t('archive.message_4b399ad51401') : '' }}
        </option>
      </select>
      <p class="hint">{{ t('archive.message_6be1e68277c7') }}</p>
    </div>
    <div class="section-title">
      <span class="num">2</span>{{ t('archive.message_9101b3c545e4') }}
    </div>
    <div class="radio-cards mb16">
      <button
        v-for="kind in initial.kinds"
        :key="kind.value"
        class="radio-card"
        :class="{ selected: form.kind === kind.value }"
        type="button"
        data-action="route-kind"
        :data-value="kind.value"
        @click.stop="changeKind(kind.value as RouteRule['kind'])"
      >
        <ViewContent
          :content="
            icon(
              kind.value === 'proxy' ? 'box' : kind.value === 'return' ? 'checkcircle' : 'external',
            )
          "
        />{{ kind.label }}
      </button>
    </div>
    <template v-if="form.kind === 'proxy'">
      <div v-if="initial.supportsCustomTarget" class="field route-target-choice">
        <label id="rf-target-mode-label" class="field-label">{{
          t('domainPublication.targetMode')
        }}</label>
        <div class="y-segments" role="group" aria-labelledby="rf-target-mode-label">
          <button
            type="button"
            :class="{ selected: targetMode === 'service' }"
            data-action="route-target-mode"
            data-value="service"
            :aria-pressed="targetMode === 'service'"
            @click.stop="changeTargetMode('service')"
          >
            {{ t('domainPublication.internalService') }}
          </button><button
            type="button"
            :class="{ selected: targetMode === 'custom' }"
            data-action="route-target-mode"
            data-value="custom"
            :aria-pressed="targetMode === 'custom'"
            @click.stop="changeTargetMode('custom')"
          >
            {{ t('domainPublication.customTarget') }}
          </button>
        </div>
      </div>
      <div v-if="targetMode === 'custom'" class="field">
        <label class="field-label" for="rf-external-url">{{ t('domainPublication.externalTarget') }} <span class="required">*</span></label><input
          id="rf-external-url"
          :value="form.externalUrl || ''"
          data-rf="externalUrl"
          type="url"
          required
          placeholder="https://upstream.example/"
          @input.stop="input('externalUrl', $event)"
        />
        <p class="hint">{{ t('domainPublication.externalRisk') }}</p>
      </div>
      <div v-else class="field">
        <label class="field-label" for="rf-service">{{ t('archive.message_3f7c6a289dd9') }} <span class="required">*</span></label><select
          id="rf-service"
          :value="form.service"
          data-rf="service"
          @change.stop="input('service', $event)"
        >
          <option v-for="service in initial.services" :key="service.id" :value="service.id">
            {{ service.name }} · {{ service.port }} · {{ service.ready }} Ready
          </option>
        </select>
        <p class="hint">{{ t('archive.message_5277b019c8a9') }}</p>
      </div>
      <div class="field-row">
        <div class="field">
          <label class="field-label" for="rf-timeout">{{ t('archive.message_fc131fa065ee') }}</label><input
            id="rf-timeout"
            :value="form.timeout"
            data-rf="timeout"
            type="number"
            min="1"
            max="3600"
            @input.stop="numberInput('timeout', $event)"
          />
          <p class="hint">proxy_read_timeout</p>
        </div>
        <div class="field">
          <label class="field-label" for="rf-connect">{{ t('archive.message_e5b80032fecf') }}</label><input
            id="rf-connect"
            :value="form.connect"
            data-rf="connect"
            type="number"
            min="1"
            max="300"
            @input.stop="numberInput('connect', $event)"
          />
          <p class="hint">proxy_connect_timeout</p>
        </div>
      </div>
      <div class="switch-row">
        <div>
          <label>{{ t('archive.message_146b618bcb8a') }}</label>
          <p>{{ t('archive.message_66d2cc875d36') }}</p>
        </div>
        <button
          class="switch"
          :class="{ on: form.strip }"
          type="button"
          data-action="route-toggle"
          data-key="strip"
          role="switch"
          :aria-checked="form.strip"
          :aria-label="t('archive.message_146b618bcb8a')"
          @click.stop="toggle('strip')"
        />
      </div>
      <div class="switch-row">
        <div>
          <label>WebSocket Upgrade</label>
          <p>{{ t('archive.message_14309bc3f429') }}</p>
        </div>
        <button
          class="switch"
          :class="{ on: form.websocket }"
          type="button"
          data-action="route-toggle"
          data-key="websocket"
          role="switch"
          :aria-checked="form.websocket"
          :aria-label="t('archive.message_1e7e9211b7e2')"
          @click.stop="toggle('websocket')"
        />
      </div>
      <div id="route-path-preview" class="route-preview mt12">
        {{ t('archive.message_ae79e8ce246e', { v7: form.path, v8: upstreamPath }) }}
      </div>
    </template>
    <div v-else class="field-row">
      <div class="field">
        <label class="field-label" for="rf-code">{{ t('archive.message_327607f80ee2') }}</label><select
          id="rf-code"
          :value="form.code"
          data-rf="code"
          @change.stop="numberInput('code', $event)"
        >
          <option v-for="code in codes" :key="code" :value="code">{{ code }}</option>
        </select>
      </div>
      <div class="field">
        <label class="field-label" for="rf-value">{{
          t(
            form.kind === 'redirect'
              ? 'archive.message_6ee2b0cd4724'
              : 'archive.message_e4c5dead4d24',
          )
        }}</label><input
          id="rf-value"
          :value="form.value"
          data-rf="value"
          :placeholder="form.kind === 'redirect' ? 'https://example.com/' : 'ok'"
          @input.stop="input('value', $event)"
        />
      </div>
    </div>
    <div class="section-title">
      <span class="num">3</span>{{ t('archive.message_c67e0c31a62d') }}
    </div>
    <div class="switch-row">
      <div>
        <label>{{ t('archive.message_5433e7bcb314') }}</label>
        <p>{{ t('archive.message_8e458176b84b') }}</p>
      </div>
      <button
        class="switch"
        :class="{ on: form.enabled }"
        type="button"
        data-action="route-toggle"
        data-key="enabled"
        role="switch"
        :aria-checked="form.enabled"
        :aria-label="t('archive.message_5433e7bcb314')"
        @click.stop="toggle('enabled')"
      />
    </div>
    <div class="callout neutral mt16">{{ t('archive.message_aebb4cb52171') }}</div>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </div>
  <div class="modal-foot">
    <button
      v-if="initial.existing"
      class="btn danger sm"
      type="button"
      data-action="route-delete"
      @click.stop="store.service?.requestRouteDelete()"
    >
      {{ t('archive.message_e3ee7318f5a0') }}
    </button><button
      class="btn ghost"
      type="button"
      data-action="close"
      @click.stop="store.service?.closeModal()"
    >
      {{ t('console.cancel') }}
    </button><button
      class="btn primary"
      type="button"
      data-action="route-apply"
      @click.stop="store.service?.applyRouteForm()"
    >
      {{ t('archive.message_2354c137ceb3') }}
    </button>
  </div>
</template>
