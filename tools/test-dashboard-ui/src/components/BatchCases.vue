<script setup lang="ts">
import { usePlaygroundStore } from '../stores/playgroundStore'
import { Play, Loader, CheckCircle, XCircle, ChevronRight } from 'lucide-vue-next'

const store = usePlaygroundStore()

const intentColors: Record<string, string> = {
  keyword: 'text-green-400',
  semantic: 'text-purple-400',
  temporal: 'text-yellow-400',
  relational: 'text-blue-400',
  general: 'text-gray-400',
}
</script>

<template>
  <div class="flex flex-col h-full border-r border-gray-700 w-72 shrink-0 bg-gray-900">
    <div class="flex items-center justify-between p-3 border-b border-gray-700">
      <span class="text-sm font-medium text-gray-300">Batch Cases</span>
      <button @click="store.runBatchCases()" :disabled="!store.isLoaded || store.batchRunning"
        class="px-3 py-1 bg-blue-600 hover:bg-blue-500 disabled:bg-gray-700 disabled:text-gray-500 rounded text-xs font-medium transition-colors flex items-center gap-1">
        <Loader v-if="store.batchRunning" class="w-3 h-3 animate-spin" />
        <Play v-else class="w-3 h-3" />
        Run All
      </button>
    </div>

    <!-- 摘要 -->
    <div v-if="store.batchResult" class="flex items-center gap-3 px-3 py-2 border-b border-gray-700 text-xs">
      <span class="text-green-400">{{ store.batchResult.summary.passed }} passed</span>
      <span class="text-red-400">{{ store.batchResult.summary.failed }} failed</span>
      <span class="text-gray-500">{{ store.batchResult.duration_ms }}ms</span>
    </div>

    <!-- Case 列表 -->
    <div class="flex-1 overflow-auto">
      <div v-if="!store.isLoaded" class="p-4 text-sm text-gray-500 text-center">
        请先加载数据集
      </div>
      <div v-else-if="!store.batchResult" class="p-4 text-sm text-gray-500 text-center">
        点击 Run All 执行测试
      </div>
      <div v-else>
        <div v-for="cr in store.batchResult.results" :key="cr.name"
          @click="store.selectedCase = cr"
          class="flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-gray-800 border-b border-gray-800 transition-colors"
          :class="store.selectedCase?.name === cr.name ? 'bg-gray-800' : ''">
          <CheckCircle v-if="cr.passed" class="w-4 h-4 text-green-400 shrink-0" />
          <XCircle v-else class="w-4 h-4 text-red-400 shrink-0" />
          <div class="flex-1 min-w-0">
            <div class="text-sm text-gray-200 truncate">{{ cr.name }}</div>
            <div class="flex items-center gap-2 text-xs text-gray-500">
              <span :class="intentColors[cr.actual_intent]">{{ cr.actual_intent }}</span>
              <span>{{ cr.result_count }}条</span>
              <span>{{ cr.duration_ms }}ms</span>
            </div>
          </div>
          <ChevronRight class="w-3 h-3 text-gray-600 shrink-0" />
        </div>
      </div>
    </div>
  </div>
</template>
