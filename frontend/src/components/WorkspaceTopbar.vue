<script setup lang="ts">
import { computed } from 'vue'
import { translate as t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'

const props = defineProps<{ revision: number }>()
const store = useWorkspaceStore()
const searchLabel = computed(() => {
  void props.revision
  return store.service?.ui.allProjects
    ? t('archive.message_94114d55e162')
    : t('prototype.search_project')
})
</script>

<template>
  <header class="topbar">
    <div class="flex">
      <button
        class="iconbtn mobile-menu"
        type="button"
        data-action="menu"
        :aria-label="t('archive.message_38e4f2ee1b9a')"
        @click.stop="store.service?.toggleMenu()"
      >
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M3 6h18M3 12h18M3 18h18" />
        </svg>
      </button>
      <div class="brand">
        <div class="logo">N</div>
        <div>
          <div class="brand-name">Network Service Composer</div>
          <div class="brand-sub">COMPOSE YOUR SERVICES. CONTROL YOUR EDGE.</div>
        </div>
      </div>
    </div>
    <div class="top-actions">
      <button
        class="search-btn"
        type="button"
        data-action="palette"
        :aria-label="t('archive.message_f1c10da84402')"
        @click.stop="store.service?.openPalette()"
      >
        <svg class="icon sm" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="10.5" cy="10.5" r="6.5" />
          <path d="m16 16 5 5" />
        </svg>
        <span class="label">{{ searchLabel }}</span><span class="kbd">⌘ K</span>
      </button>
      <button
        class="iconbtn helpbtn"
        type="button"
        data-action="help"
        :aria-label="t('archive.message_b6e77060db53')"
        @click.stop="store.service?.openHelp()"
      >
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M9.5 8.5a2.5 2.5 0 1 1 3 2.5c-.5.2-.5 1-.5 2M12 17h.01" />
        </svg>
      </button>
      <button
        class="iconbtn"
        type="button"
        data-action="nav"
        data-page="events"
        :aria-label="t('archive.message_f98dfe0b4d54')"
        @click.stop="store.service?.navigatePage('events')"
      >
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4" />
        </svg>
      </button>
      <button
        class="avatar"
        type="button"
        data-action="about"
        :aria-label="t('archive.message_02b92fd69f81')"
        @click.stop="store.service?.openAbout()"
      >
        GS
      </button>
    </div>
  </header>
</template>
