<script setup lang="ts">
import { computed } from 'vue'
import { statusTone } from '../data/status-tone'

const props = withDefaults(defineProps<{
  status?: string | null
  label?: string | null
  context?: 'resource' | 'operation'
  tone?: 'green' | 'blue' | 'amber' | 'red' | '' | null
}>(), { status: '', label: null, context: 'resource', tone: null })
const color = computed(() => props.tone ?? statusTone(props.status, props.context))
</script>

<template>
  <span class="badge" :class="color">{{ label ?? (status || '—') }}</span>
</template>
