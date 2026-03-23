<script setup lang="ts">
import { computed } from 'vue'
import { useTestStore } from '../stores/testStore'

const store = useTestStore()

const total = computed(() => store.allCases.length || 1)
const completed = computed(() => store.allCases.filter(c => c.status === 'pass' || c.status === 'fail' || c.status === 'skip').length)
const percent = computed(() => Math.round((completed.value / total.value) * 100))
const hasFailure = computed(() => store.allCases.some(c => c.status === 'fail'))
</script>

<template>
  <div v-if="store.running || completed > 0" class="h-1 bg-gray-800 w-full">
    <div class="h-full transition-all duration-300 ease-out"
         :class="hasFailure ? 'bg-red-500' : 'bg-green-500'"
         :style="{ width: percent + '%' }" />
  </div>
</template>
