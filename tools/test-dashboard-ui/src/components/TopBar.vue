<script setup lang="ts">
import { Play, Square, Wifi, WifiOff } from 'lucide-vue-next'
import { useTestStore } from '../stores/testStore'

const store = useTestStore()

defineProps<{
  connected: boolean
}>()

const emit = defineEmits<{
  run: [suite?: string]
  stop: []
}>()
</script>

<template>
  <header class="flex items-center justify-between px-4 py-2 bg-gray-900 border-b border-gray-700 text-white">
    <div class="flex items-center gap-3">
      <h1 class="text-lg font-semibold tracking-tight">IClude Test Dashboard</h1>
      <span class="flex items-center gap-1 text-xs" :class="connected ? 'text-green-400' : 'text-red-400'">
        <component :is="connected ? Wifi : WifiOff" class="w-3.5 h-3.5" />
        {{ connected ? 'Connected' : 'Disconnected' }}
      </span>
    </div>

    <div class="flex items-center gap-3">
      <div v-if="!store.running && (store.totalPassed > 0 || store.totalFailed > 0)"
           class="text-sm flex gap-3">
        <span class="text-green-400">{{ store.totalPassed }} passed</span>
        <span v-if="store.totalFailed > 0" class="text-red-400">{{ store.totalFailed }} failed</span>
      </div>

      <button v-if="!store.running"
              @click="emit('run')"
              :disabled="!connected"
              class="flex items-center gap-1.5 px-3 py-1.5 bg-green-600 hover:bg-green-500 disabled:bg-gray-600 disabled:cursor-not-allowed rounded text-sm font-medium transition-colors">
        <Play class="w-3.5 h-3.5" />
        Run All
      </button>
      <button v-else
              @click="emit('stop')"
              class="flex items-center gap-1.5 px-3 py-1.5 bg-red-600 hover:bg-red-500 rounded text-sm font-medium transition-colors">
        <Square class="w-3.5 h-3.5" />
        Stop
      </button>
    </div>
  </header>
</template>
