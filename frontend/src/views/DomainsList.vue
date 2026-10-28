<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import type { Domain } from '../types/domain'
import ViewContent from '../components/ViewContent.vue'

const props = withDefaults(defineProps<{ domains: Domain[]; showProject?: boolean }>(), {
  showProject: false,
})
const store = useWorkspaceStore()
const state = computed(() => ({ ...store.state() }))
const search = computed(() => {
  void store.revision
  return String(store.service?.ui.search || '')
})
const filter = computed(() => {
  void store.revision
  return String(store.service?.ui.filter || 'all')
})
const issueCount = computed(
  () => props.domains.filter((domain) => store.service?.domainHasIssue(domain.id)).length,
)
const filtered = computed(() =>
  props.domains.filter(
    (domain) =>
      domain.name.toLowerCase().includes(search.value.toLowerCase()) &&
      (filter.value === 'all' ||
        (filter.value === 'draft' && Boolean(domain.draft)) ||
        (filter.value === 'issue' && Boolean(store.service?.domainHasIssue(domain.id)))),
  ),
)
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function coverage(id: string) {
  return store.service?.domainCoverage(id) || { known: 0, total: 0 }
}
function scopeName(domain: Domain) {
  const environment =
    state.value.environments.find((item) => item.id === domain.env)?.name || domain.env
  return props.showProject
    ? `${state.value.projects.find((item) => item.id === domain.project)?.name || domain.project} / ${environment}`
    : environment
}
function onSearch(event: Event) {
  store.service?.updateDomainListSearch((event.target as HTMLInputElement).value)
}
function openDomain(id: string) {
  store.service?.openDomain(id)
}
</script>

<template>
  <div class="filterbar">
    <div class="search-field">
      <ViewContent :content="icon('search')" /><input
        id="domain-search"
        :value="search"
        :placeholder="t('archive.message_24c58da52174')"
        :aria-label="t('archive.message_24c58da52174')"
        @input.stop="onSearch"
      />
    </div>
    <div class="chip-group">
      <button
        v-for="option in [
          { id: 'all', label: t('archive.message_db41a9bcde11') + domains.length },
          {
            id: 'draft',
            label:
              t('archive.message_3f27bd304dd3') + domains.filter((domain) => domain.draft).length,
          },
          { id: 'issue', label: t('archive.message_8930f1744c13') + issueCount },
        ]"
        :key="option.id"
        class="chip"
        :class="{ active: filter === option.id }"
        type="button"
        data-action="filter"
        :data-value="option.id"
        @click.stop="store.service?.updateDomainListFilter(option.id)"
      >
        {{ option.label }}
      </button>
    </div>
  </div>
  <section class="card">
    <div class="table-scroll">
      <table class="table">
        <thead>
          <tr>
            <th>Domain</th>
            <th>{{ t('archive.message_85b10455c513') }}</th>
            <th>Route / Service</th>
            <th>{{ t('archive.message_0f3c10407bc0') }}</th>
            <th>{{ t('archive.message_2a2fd29bd27a') }}</th>
            <th class="text-right">{{ t('community.action') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="domain in filtered" :key="domain.id">
            <td>
              <div class="domain-list-name">
                <div class="mini-domain"><ViewContent :content="icon('globe')" /></div>
                <div>
                  <button
                    class="row-link"
                    type="button"
                    data-action="open-domain"
                    :data-id="domain.id"
                    @click.stop="openDomain(domain.id)"
                  >
                    {{ domain.name }}
                  </button>
                  <div class="sub-cell">
                    {{ scopeName(domain) }} ·
                    {{
                      (domain.draft?.config || domain.active).mode === 'native'
                        ? t('archive.message_b85a16d528cd')
                        : t('archive.message_607c5530d3fe')
                    }}
                  </div>
                </div>
              </div>
            </td>
            <td>
              {{ (domain.draft?.config || domain.active).tls ? 'HTTPS :443' : 'HTTP :80' }}
              <div class="sub-cell">
                {{
                  (domain.draft?.config || domain.active).tls
                    ? 'Domain certificate'
                    : t('archive.message_6d9b72722344')
                }}
              </div>
            </td>
            <td>
              {{ (domain.draft?.config || domain.active).routes.length }} Routes ·
              {{
                new Set(
                  (domain.draft?.config || domain.active).routes
                    .filter((route) => route.kind === 'proxy')
                    .map((route) => route.service),
                ).size
              }}
              Services
            </td>
            <td>
              <ViewContent :content="store.service?.domainStatusView(domain.id)" />
              <div class="sub-cell">
                {{ coverage(domain.id).known }} / {{ coverage(domain.id).total }} Gateway
              </div>
            </td>
            <td>
              <span class="badge" :class="{ blue: domain.draft }">{{
                t(domain.draft ? 'archive.message_07901db4aad3' : 'archive.message_28ec083a3520')
              }}</span>
            </td>
            <td class="text-right">
              <button
                class="textbtn"
                type="button"
                data-action="open-domain"
                :data-id="domain.id"
                @click.stop="openDomain(domain.id)"
              >
                {{ t('archive.message_c771248e511f') }}
              </button>
            </td>
          </tr>
          <tr v-if="!filtered.length">
            <td colspan="6">
              <div class="empty">{{ t('archive.message_70960c118d57') }}</div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
