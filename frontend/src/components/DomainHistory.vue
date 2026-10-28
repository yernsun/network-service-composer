<script setup>
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, errorText, session } from '../shared/api/client'
import { allPages } from '../data/pagination'
import { domainHistoryDetail, domainHistoryEntries } from '../data/domain-history'
import { i18n } from '../shared/i18n'

const props = defineProps({ poolId: { type: String, required: true }, domainId: { type: String, required: true }, routeIds: { type: Array, default: () => [] } })
const historyText = key => i18n.global.t(`domainHistory.${key}`)
const published = useQuery({ queryKey: computed(() => ['domain-history-published', props.poolId]),
  queryFn: ({ signal }) => allPages('/gateway-pools/{id}/nginx/versions', { id: props.poolId }, signal), refetchOnMount: 'always', refetchInterval: 30000 })
const drafts = useQuery({ queryKey: computed(() => ['domain-history-drafts', props.poolId]),
  queryFn: ({ signal }) => allPages('/gateway-pools/{id}/nginx/draft-versions', { id: props.poolId }, signal), refetchOnMount: 'always', refetchInterval: 30000 })
const config = useQuery({ queryKey: computed(() => ['domain-history-current', props.poolId]),
  queryFn: ({ signal }) => request('get', '/gateway-pools/{id}/nginx', { params: { path: { id: props.poolId } }, signal }), refetchOnMount: 'always', refetchInterval: 30000 })
const entries = computed(() => domainHistoryEntries(published.data.value || [], drafts.data.value || [], props.domainId, props.routeIds,
  { publishedComplete: published.isSuccess.value && !published.error.value }))
const selectedId = ref('')
const detailScope = ref('domain')
const selected = computed(() => entries.value.find(entry => entry.id === selectedId.value))
const changes = computed(() => selected.value ? domainHistoryDetail(selected.value, detailScope.value) : [])
const changeSections = computed(() => ['domain', 'global', 'pool', 'unknown']
  .map(scope => ({ scope, items: changes.value.filter(change => change.scope === scope) }))
  .filter(section => section.items.length))
const currentDraft = computed(() => config.data.value?.data?.draft)
const activeVersionId = computed(() => config.data.value?.data?.active?.version_id || '')
const loading = computed(() => published.isPending.value || drafts.isPending.value || config.isPending.value)
const errors = computed(() => [published.error.value, drafts.error.value, config.error.value].filter(Boolean))
const occurred = value => value ? new Intl.DateTimeFormat(session.value.locale, { dateStyle: 'medium', timeStyle: 'medium', timeZone: 'Asia/Singapore' }).format(new Date(value)) : '—'
function refresh() { void published.refetch(); void drafts.refetch(); void config.refetch() }
function toggleDetail(id) {
  selectedId.value = selectedId.value === id ? '' : id
  detailScope.value = 'domain'
}
function changeTitle(change) {
  if (change.type === 'access') return `${i18n.global.t('ipAccess.title')} · ${change.resourceId}`
  if (change.type === 'snippet') return `${change.snippetScope}${change.resourceId ? ` · ${change.resourceId}` : ''}`
  return `${change.path || historyText('route')} · ${change.key}`
}
</script>

<template>
  <section class="card x-pad domain-history">
    <header class="domain-history-head">
      <div><h3>{{ historyText('title') }}</h3><p class="hint">{{ historyText('poolScope') }}</p></div>
      <button class="btn sm" type="button" @click="refresh">{{ historyText('refresh') }}</button>
    </header>
    <p v-if="currentDraft?.revision" class="domain-history-current">
      <strong>{{ historyText('currentDraft') }}</strong> · {{ historyText('revision') }} {{ currentDraft.revision }} · {{ occurred(currentDraft.updated_at) }}
    </p>
    <p v-if="loading && !entries.length" class="hint" role="status">{{ historyText('loading') }}</p>
    <p v-for="(error, index) in errors" :key="index" class="callout red" role="alert">{{ historyText('readFailed') }} {{ errorText(error) }}</p>
    <p v-if="!loading && !errors.length && !entries.length" class="hint">{{ historyText('empty') }}</p>
    <ol v-if="entries.length" class="domain-history-list">
      <li v-for="entry in entries" :key="entry.id" class="domain-history-item" :class="[entry.type, { 'is-open': selected?.id === entry.id }]">
        <div class="domain-history-entry">
          <div class="domain-history-entry-main">
            <span class="badge" :class="entry.type === 'published' ? 'blue' : 'amber'">{{ historyText(entry.type) }}</span>
            <strong class="domain-history-time">{{ occurred(entry.time) }}</strong>
            <span class="badge" :class="entry.scope === 'domain' ? 'green' : entry.scope === 'global' ? 'blue' : entry.scope === 'unknown' ? 'amber' : ''">{{ historyText(`scope_${entry.scope}`) }}</span>
            <span v-if="entry.id === activeVersionId" class="badge green">{{ historyText('controllerActive') }}</span>
          </div>
          <button class="btn sm domain-history-open" type="button" :aria-expanded="selectedId === entry.id" :aria-controls="`domain-history-detail-${entry.id}`" @click="toggleDetail(entry.id)">{{ historyText(selectedId === entry.id ? 'hideDiff' : 'viewDiff') }}</button>
        </div>
        <p class="hint domain-history-meta">ID {{ entry.id }}<template v-if="entry.actor"> · {{ entry.actor }}</template></p>
        <p v-if="entry.routePaths.length" class="hint">{{ historyText('routes') }} {{ entry.routePaths.join(', ') }}</p>
        <p v-if="entry.snippetChanged" class="hint">{{ historyText('snippetIncluded') }}</p>
        <section v-if="selected?.id === entry.id" :id="`domain-history-detail-${entry.id}`" class="domain-history-detail" role="region" :aria-label="historyText('detailTitle')">
          <p class="domain-history-source">{{ historyText('sourceNote') }}</p>
          <p class="domain-history-baseline"><strong>{{ historyText('baseline') }}</strong> <template v-if="entry.baselineAvailable">{{ historyText(entry.baselineType) }} · <code>{{ entry.baselineId }}</code></template><span v-else class="amber">{{ historyText('baselineUnavailable') }}</span></p>
          <p v-if="entry.description"><strong>{{ historyText('description') }}</strong> {{ entry.description }}</p>
          <div class="domain-history-switch" role="group" :aria-label="historyText('viewScope')">
            <button class="btn sm" type="button" :class="{ active: detailScope === 'domain' }" :aria-pressed="detailScope === 'domain'" @click="detailScope = 'domain'">{{ historyText('domainView') }}</button>
            <button class="btn sm" type="button" :class="{ active: detailScope === 'pool' }" :aria-pressed="detailScope === 'pool'" @click="detailScope = 'pool'">{{ historyText('poolView') }}</button>
          </div>
          <p v-if="!changes.length" class="hint domain-history-empty-detail">{{ historyText(entry.baselineAvailable ? 'noChanges' : 'noSnapshot') }}</p>
          <section v-for="section in changeSections" :key="section.scope" class="domain-history-section" :aria-label="historyText(`scope_${section.scope}`)">
            <h4 class="domain-history-section-title"><span>{{ historyText(`scope_${section.scope}`) }}</span><span class="domain-history-section-count">{{ section.items.length }}</span></h4>
            <div v-for="change in section.items" :key="`${change.type}:${change.key}`" class="domain-history-change">
              <div class="domain-history-change-head">
                <strong>{{ historyText(change.type) }} · {{ changeTitle(change) }}</strong>
                <span class="badge" :class="change.action === 'removed' || change.action === 'pendingRemoved' || change.action === 'publishedDelete' ? 'red' : change.action === 'snapshot' ? 'blue' : 'green'">{{ historyText(change.action) }}</span>
              </div>
              <p v-if="change.beforeUnavailable" class="domain-history-change-note amber">{{ historyText('routeBeforeUnavailable') }}</p>
              <p v-if="change.tooLarge" class="domain-history-change-note hint">{{ historyText('largeDiff') }}</p>
              <div v-if="change.lines !== null" class="domain-history-code" role="group" :aria-label="changeTitle(change)">
                <div v-for="(line, index) in change.lines" :key="index" class="domain-history-line" :class="line.type">
                  <span v-if="line.type !== 'same'" class="domain-history-a11y">{{ historyText(line.type === 'add' ? 'added' : 'removed') }}: </span>
                  <span aria-hidden="true">{{ line.type === 'add' ? '+' : line.type === 'remove' ? '−' : ' ' }}</span>
                  <span class="domain-history-line-number" aria-hidden="true">{{ line.beforeLine ?? '·' }} / {{ line.afterLine ?? '·' }}</span>
                  <code>{{ line.text || ' ' }}</code>
                </div>
              </div>
              <div v-else class="domain-history-snapshots">
                <div v-if="change.before !== null"><span class="hint">{{ historyText('before') }}</span><pre>{{ change.before }}</pre></div>
                <div v-if="change.after !== null"><span class="hint">{{ historyText('savedValue') }}</span><pre>{{ change.after }}</pre></div>
              </div>
            </div>
          </section>
        </section>
      </li>
    </ol>
    <p class="hint domain-history-note">{{ historyText('historyLimit') }}</p>
  </section>
</template>

<style scoped>
.domain-history { min-width: 0; }
.domain-history-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
.domain-history-head h3 { margin: 0 0 5px; }
.domain-history-head p { margin: 0; }
.domain-history-current { padding: 12px 14px; border: 1px solid var(--line); border-radius: 8px; background: var(--blue-soft); overflow-wrap: anywhere; }
.domain-history-list { display: grid; gap: 12px; list-style: none; margin: 16px 0 0; padding: 0; }
.domain-history-item { min-width: 0; padding: 16px; border: 1px solid var(--line); border-left: 3px solid var(--blue); border-radius: 10px; background: var(--panel); overflow-wrap: anywhere; }
.domain-history-item.draft { border-left-color: var(--amber); }
.domain-history-item.is-open { box-shadow: 0 5px 18px #17324e0c; }
.domain-history-entry { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.domain-history-entry-main { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-width: 0; }
.domain-history-time { font-size: 13px; }
.domain-history-item > p { margin: 7px 0 0; }
.domain-history-meta { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.domain-history-note { margin: 16px 0 0; }
.domain-history-open { flex: none; white-space: nowrap; }
.domain-history-item.is-open .domain-history-open { border-color: var(--green); color: var(--green-dark); }
.domain-history-detail { min-width: 0; margin-top: 16px; padding: 16px; border: 1px solid var(--line); border-radius: 10px; background: #fbfcfe; }
.domain-history-detail > p { margin: 0 0 10px; }
.domain-history-detail > .domain-history-source { padding: 10px 12px; border-left: 3px solid var(--blue); border-radius: 6px; background: var(--blue-soft); color: var(--ink); }
.domain-history-baseline code { overflow-wrap: anywhere; }
.domain-history-switch { display: inline-flex; flex-wrap: wrap; gap: 3px; margin: 10px 0 2px; padding: 3px; border: 1px solid var(--line); border-radius: 9px; background: #edf2f6; }
.domain-history-switch .btn { border-color: transparent; background: transparent; }
.domain-history-switch .active { color: var(--green-dark); background: var(--panel); box-shadow: 0 1px 4px #17324e17; }
.domain-history-section { min-width: 0; margin-top: 18px; }
.domain-history-section-title { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; color: var(--ink); font-size: 12px; }
.domain-history-section-count { min-width: 20px; padding: 1px 6px; border-radius: 6px; background: #eaf0f5; color: var(--muted); text-align: center; font-size: 10px; }
.domain-history-change { min-width: 0; margin-top: 8px; border: 1px solid var(--line); border-radius: 8px; background: var(--panel); overflow: hidden; }
.domain-history-change-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 10px; padding: 10px 12px; background: #f4f7fa; }
.domain-history-change-head strong { min-width: 0; overflow-wrap: anywhere; }
.domain-history-change-head .badge { flex: none; }
.domain-history-change-note { margin: 0; padding: 9px 12px; border-top: 1px solid var(--line); background: var(--amber-soft); }
.domain-history-change-note.hint { background: var(--blue-soft); }
.domain-history-empty-detail { margin-top: 12px !important; }
.domain-history-code { max-height: 380px; overflow: auto; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 12px; }
.domain-history-line { display: grid; grid-template-columns: 20px 75px max-content; gap: 8px; min-width: max-content; padding: 2px 12px; white-space: pre; }
.domain-history-line.add { color: var(--green-dark); background: var(--green-soft); }
.domain-history-line.remove { color: var(--red); background: var(--red-soft); }
.domain-history-line-number { color: var(--muted); user-select: none; }
.domain-history-a11y { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; }
.domain-history-snapshots { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; min-width: 0; padding: 10px 12px; }
.domain-history-snapshots > div { min-width: 0; }
.domain-history-snapshots pre { max-height: 380px; margin: 4px 0 0; padding: 10px; overflow: auto; background: var(--bg); border-radius: 6px; font-size: 12px; }
@media (max-width: 600px) {
  .domain-history-entry { flex-direction: column; }
  .domain-history-open { align-self: flex-end; }
  .domain-history-item { padding: 12px; }
  .domain-history-detail { padding: 12px; }
  .domain-history-switch { display: flex; }
  .domain-history-switch .btn { flex: 1; }
  .domain-history-line { font-size: 11px; }
}
</style>
