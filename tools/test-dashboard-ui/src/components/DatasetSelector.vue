<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { usePlaygroundStore } from '../stores/playgroundStore'
import { Database, Loader, CheckCircle } from 'lucide-vue-next'

const store = usePlaygroundStore()
const selectedFile = ref('')

onMounted(() => { store.fetchDatasets() })
</script>

<template>
  <div class="flex items-center gap-4 p-4 bg-gray-900 border-b border-gray-700">
    <Database class="w-5 h-5 text-blue-400" />
    <select v-model="selectedFile"
      class="bg-gray-800 border border-gray-600 rounded px-3 py-1.5 text-sm text-gray-200 min-w-48">
      <option value="" disabled>选择数据集...</option>
      <option v-for="ds in store.datasets" :key="ds.file_name" :value="ds.file_name">
        {{ ds.name }} ({{ ds.stats.memories }}条记忆)
      </option>
    </select>
    <button @click="store.loadDataset(selectedFile)" :disabled="!selectedFile || store.loading"
      class="px-4 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:bg-gray-700 disabled:text-gray-500 rounded text-sm font-medium transition-colors">
      <Loader v-if="store.loading" class="w-4 h-4 animate-spin" />
      <span v-else>Load</span>
    </button>
    <div v-if="store.isLoaded" class="flex items-center gap-2 text-sm text-green-400">
      <CheckCircle class="w-4 h-4" />
      <span>{{ store.loadedDataset?.name }}</span>
      <span class="text-gray-500">
        {{ store.loadedDataset?.stats.memories }}条记忆 /
        {{ store.loadedDataset?.stats.entities }}个实体 /
        {{ store.loadedDataset?.stats.relations }}条关系
      </span>
    </div>
  </div>
</template>
