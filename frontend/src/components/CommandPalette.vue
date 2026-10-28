<script setup lang="ts">
import { computed, ref } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from './ViewContent.vue'

const store = useWorkspaceStore()
const search = ref('')
const items = computed(() => {
  void store.revision
  return store.service?.paletteItems(search.value)
})
const commands = computed(() =>
  [
    { id: 'config', label: t('archive.message_845e79578dbc'), icon: 'code' },
    { id: 'ops', label: t('prototype.operations_view'), icon: 'server' },
    { id: 'scenarios', label: t('archive.message_546805b0c669'), icon: 'play' },
    { id: 'help', label: t('archive.message_b6e77060db53'), icon: 'help' },
  ].filter(
    (command) =>
      !search.value || command.label.toLowerCase().includes(search.value.trim().toLowerCase()),
  ),
)
function icon(name: string) {
  return store.service?.iconView(name)
}
</script>

<template>
  <label id="modal-title" class="eyebrow" style="display: none">{{
    t('archive.message_609227730251')
  }}</label>
  <input
    id="palette-input"
    v-model="search"
    class="palette-input"
    :placeholder="t('archive.message_bc7cec45c14a')"
    :aria-label="t('archive.message_609227730251')"
  />
  <div id="palette-results" class="palette-results">
    <div class="palette-scope">{{ items?.scope }}</div>
    <button
      v-for="domain in items?.domains || []"
      :key="`domain:${domain.id}`"
      class="palette-item"
      type="button"
      data-action="resource-domain"
      :data-id="domain.id"
      @click.stop="store.service?.selectPaletteItem('domain', domain.id)"
    >
      <ViewContent :content="icon('globe')" />
      <div>
        <strong>{{ domain.name }}</strong>
        <p>Domain · {{ domain.scope }}</p>
      </div>
      <span class="spacer" /><ViewContent :content="icon('right')" />
    </button>
    <button
      v-for="service in items?.services || []"
      :key="`service:${service.id}`"
      class="palette-item"
      type="button"
      data-action="service-detail"
      :data-id="service.id"
      @click.stop="store.service?.selectPaletteItem('service', service.id)"
    >
      <ViewContent :content="icon('box')" />
      <div>
        <strong>{{ service.name }}</strong>
        <p>Service · {{ service.scope }}</p>
      </div>
    </button>
    <button
      v-for="command in commands"
      :key="command.id"
      class="palette-item"
      type="button"
      :data-action="
        command.id === 'config' ? 'palette-config' : command.id === 'ops' ? 'go-ops' : command.id
      "
      @click.stop="store.service?.selectPaletteItem(command.id)"
    >
      <ViewContent :content="icon(command.icon)" /><strong>{{ command.label }}</strong>
    </button>
    <div v-if="!items?.domains.length && !items?.services.length && !commands.length" class="empty">
      {{ t('archive.message_a4497ff87ef1') }}
    </div>
  </div>
</template>
