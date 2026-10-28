<script setup lang="ts">
import { computed } from 'vue'
import { useWorkspaceStore } from '../stores/workspace'
import ViewContent from '../components/ViewContent.vue'
const store = useWorkspaceStore()
const content = computed(() => store.content('page'))
</script>
<template><ViewContent :content="content" /></template>
