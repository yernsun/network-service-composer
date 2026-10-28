<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'

const store = useWorkspaceStore()
const templates = computed(() => store.service?.marketTemplates() || [])
const filters = computed(() => {
  void store.revision
  const workload = store.service?.workload || {}
  return {
    category: String(workload.marketCategory || 'all'),
    search: String(workload.marketSearch || ''),
  }
})
const categoryKeys: Record<string, string> = {
  业务应用: 'archive.message_d834c184c0e7',
  内容管理: 'archive.message_c7253a80c714',
  数据库: 'archive.message_263200d7d529',
  开发工具: 'archive.message_373febf03109',
}
const categories = computed(() => [
  { id: 'all', label: t('archive.message_5c55a67935af') },
  ...[...new Set(templates.value.map((item) => item.cat))].map((id) => ({
    id,
    label: categoryKeys[id] ? t(categoryKeys[id]) : id,
  })),
])
const results = computed(() =>
  templates.value.filter(
    (item) =>
      (filters.value.category === 'all' || item.cat === filters.value.category) &&
      `${item.name} ${item.desc}`.toLowerCase().includes(filters.value.search.toLowerCase()),
  ),
)
function icon(name: string, className = '') {
  return store.service?.iconView(name, className)
}
function search(event: Event) {
  store.service?.updateMarketSearch((event.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="breadcrumbs">
    <span class="current">{{ t('archive.message_f2e7caa9f119') }}</span>
  </div>
  <div class="x-head">
    <div>
      <div class="x-tagline">APPLICATION CATALOG</div>
      <h1>{{ t('archive.message_f2e7caa9f119') }}</h1>
      <p>{{ t('archive.message_117975376b1b') }}</p>
    </div>
    <div class="hero-actions">
      <button
        class="btn"
        type="button"
        data-action="x-install-file"
        data-vue-control="true"
        @click.stop="store.service?.beginApplicationInstall('file')"
      >
        <ViewContent :content="icon('file')" />{{ t('archive.message_432866e592b3') }}
      </button>
      <button
        class="btn"
        type="button"
        data-action="x-install-image"
        data-vue-control="true"
        @click.stop="store.service?.beginApplicationInstall('image')"
      >
        <ViewContent :content="icon('box')" />{{ t('archive.message_0318ec3e3a1e') }}
      </button>
    </div>
  </div>
  <div class="x-start-banner">
    <div class="x-appicon"><ViewContent :content="icon('layers', 'lg')" /></div>
    <div class="spacer">
      <h2>{{ t('archive.message_24d20c02559b') }}</h2>
      <p>{{ t('archive.message_809da8d96ce1') }}</p>
    </div>
    <span class="badge green">{{ t('archive.message_ceb463b82d43') }}</span>
  </div>
  <div class="x-filterbar">
    <div class="search-field">
      <ViewContent :content="icon('search')" /><input
        id="x-market-search"
        :value="filters.search"
        :placeholder="t('archive.message_be7dd833ac1f')"
        :aria-label="t('archive.message_23a2f7c7271a')"
        @input.stop="search"
      />
    </div>
    <div class="chip-group">
      <button
        v-for="category in categories"
        :key="category.id"
        class="chip"
        :class="{ active: filters.category === category.id }"
        type="button"
        data-action="x-category"
        data-vue-control="true"
        :data-id="category.id"
        @click.stop="store.service?.updateMarketCategory(category.id)"
      >
        {{ category.label }}
      </button>
    </div>
  </div>
  <div class="x-grid">
    <article
      v-for="item in results"
      :key="item.id"
      class="card x-template actionable-card"
      role="link"
      tabindex="0"
      @click="store.service?.openMarketTemplate(item.id)"
      @keydown.enter.prevent="store.service?.openMarketTemplate(item.id)"
      @keydown.space.prevent="store.service?.openMarketTemplate(item.id)"
    >
      <div class="flex">
        <div class="x-appicon" :class="item.color">{{ item.glyph }}</div>
        <div class="spacer">
          <h2>{{ item.name }}</h2>
          <div class="x-hint-inline">
            {{ categories.find((category) => category.id === item.cat)?.label || item.cat }} ·
            {{ item.version }}
          </div>
        </div>
        <span class="badge" :class="item.stateful ? 'amber' : 'blue'">{{
          t(item.stateful ? 'archive.message_9cf2e244f3a6' : 'archive.message_27b7a80b46d8')
        }}</span>
      </div>
      <p class="desc">{{ item.desc }}</p>
      <div class="flex wrap">
        <span v-if="item.modes.includes('compose')" class="badge">{{
          t('archive.message_ce2e77adba77')
        }}</span><span v-if="item.modes.includes('stack')" class="badge">{{
          t('archive.message_2fdc66106e25')
        }}</span><span v-if="item.modes.includes('container')" class="badge">{{
          t('archive.message_95e86cd83827')
        }}</span>
      </div>
      <div class="x-template-foot">
        <span>{{
          t('archive.message_34c247f3664e', {
            v10: item.services.length,
            v11: t(item.secret ? 'archive.message_b9012e9131d0' : 'archive.message_b8253afae454'),
          })
        }}</span>
        <div class="flex">
          <button
            class="btn sm"
            type="button"
            data-action="x-template-info"
            data-vue-control="true"
            :data-id="item.id"
            @click.stop="store.service?.openMarketTemplate(item.id)"
          >
            {{ t('console.details') }}
          </button><button
            class="btn sm primary"
            type="button"
            data-action="x-template"
            data-vue-control="true"
            :data-id="item.id"
            @click.stop="store.service?.installMarketTemplate(item.id)"
          >
            {{ t('archive.message_f75619a46f2f') }}
          </button>
        </div>
      </div>
    </article>
    <div v-if="!results.length" class="card x-empty">{{ t('archive.message_d3933087ddca') }}</div>
  </div>
  <div class="x-info-note mt16">{{ t('archive.message_3baeb3a16183') }}</div>
  <div class="x-foot">{{ t('archive.message_04f70a4dbc01') }}</div>
</template>
