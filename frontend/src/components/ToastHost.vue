<script setup lang="ts">
import { useWorkspaceStore } from '../stores/workspace'
defineProps<{ revision: number }>()
const store = useWorkspaceStore()
</script>
<template>
  <div id="toasts" class="toast-wrap" role="status" aria-live="polite">
    <div v-for="toast in store.toasts" :key="toast.id" :class="['toast', { error: toast.error }]">
      <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path v-if="toast.error" d="m12 3 10 18H2L12 3ZM12 9v5M12 18h.01" /><template v-else><circle cx="12" cy="12" r="9" /><path d="m7 12 3 3 6-6" /></template></svg><span>{{ toast.message }}</span>
    </div>
  </div>
</template>
