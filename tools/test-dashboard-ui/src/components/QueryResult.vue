<script setup lang="ts">
import { ref } from 'vue'
import { usePlaygroundStore } from '../stores/playgroundStore'
import { Search, Loader, Zap, Hash, GitBranch } from 'lucide-vue-next'

const store = usePlaygroundStore()
const query = ref('')

function run() {
  if (query.value.trim()) store.executeQuery(query.value.trim())
}

const intentColors: Record<string, string> = {
  keyword: 'text-green-400 bg-green-900/30',
  semantic: 'text-purple-400 bg-purple-900/30',
  temporal: 'text-yellow-400 bg-yellow-900/30',
  relational: 'text-blue-400 bg-blue-900/30',
  general: 'text-gray-400 bg-gray-700/30',
}

function formatScore(s: number) { return s > 0 ? s.toFixed(4) : '-' }
</script>

<template>
  <div class="flex flex-col h-full overflow-auto">
    <!-- 输入栏 -->
    <div class="flex gap-2 p-4 border-b border-gray-700">
      <input v-model="query" @keyup.enter="run" placeholder="输入查询..."
        class="flex-1 bg-gray-800 border border-gray-600 rounded px-3 py-2 text-sm text-gray-200 placeholder-gray-500" />
      <button @click="run" :disabled="!store.isLoaded || store.querying || !query.trim()"
        class="px-4 py-2 bg-emerald-600 hover:bg-emerald-500 disabled:bg-gray-700 disabled:text-gray-500 rounded text-sm font-medium transition-colors flex items-center gap-2">
        <Loader v-if="store.querying" class="w-4 h-4 animate-spin" />
        <Search v-else class="w-4 h-4" />
        执行
      </button>
    </div>

    <div v-if="!store.isLoaded" class="flex-1 flex items-center justify-center text-gray-500 text-sm">
      请先加载数据集
    </div>

    <div v-else-if="store.queryResult" class="flex-1 overflow-auto p-4 space-y-4">
      <!-- 预处理结果 -->
      <div v-if="store.queryResult.preprocess" class="bg-gray-900 rounded-lg p-4 border border-gray-700">
        <div class="flex items-center gap-2 mb-3">
          <Zap class="w-4 h-4 text-amber-400" />
          <span class="text-sm font-medium text-amber-400">Preprocess</span>
          <span class="text-xs text-gray-500">{{ store.queryResult.duration_ms }}ms</span>
        </div>
        <div class="grid grid-cols-2 gap-3 text-sm">
          <div>
            <span class="text-gray-500">Intent:</span>
            <span :class="['ml-2 px-2 py-0.5 rounded text-xs font-medium', intentColors[store.queryResult.preprocess.intent] || 'text-gray-400']">
              {{ store.queryResult.preprocess.intent }}
            </span>
          </div>
          <div>
            <span class="text-gray-500">Keywords:</span>
            <span class="ml-2 text-gray-300">{{ store.queryResult.preprocess.keywords?.join(', ') || '-' }}</span>
          </div>
          <div class="col-span-2">
            <span class="text-gray-500">SemanticQuery:</span>
            <span class="ml-2 text-gray-300">{{ store.queryResult.preprocess.semantic_query }}</span>
          </div>
          <div class="col-span-2">
            <span class="text-gray-500">Weights:</span>
            <span class="ml-2 text-gray-300">
              FTS={{ store.queryResult.preprocess.weights.fts.toFixed(2) }}
              Qdrant={{ store.queryResult.preprocess.weights.qdrant.toFixed(2) }}
              Graph={{ store.queryResult.preprocess.weights.graph.toFixed(2) }}
            </span>
          </div>
          <div v-if="store.queryResult.preprocess.entities?.length" class="col-span-2">
            <span class="text-gray-500">Matched Entities:</span>
            <span class="ml-2 text-gray-300">{{ store.queryResult.preprocess.entities.length }}个</span>
          </div>
        </div>
      </div>

      <!-- 通道结果 -->
      <div class="bg-gray-900 rounded-lg p-4 border border-gray-700">
        <div class="flex items-center gap-2 mb-3">
          <Hash class="w-4 h-4 text-blue-400" />
          <span class="text-sm font-medium text-blue-400">Channels</span>
        </div>
        <div class="flex gap-4 text-sm">
          <div v-for="(ch, name) in store.queryResult.channels" :key="name"
            class="px-3 py-1.5 rounded border"
            :class="ch.available ? 'border-gray-600 text-gray-300' : 'border-gray-800 text-gray-600'">
            <span class="font-medium">{{ name }}</span>
            <span class="ml-2">{{ ch.available ? ch.count + '条' : 'N/A' }}</span>
          </div>
        </div>
      </div>

      <!-- 融合结果 -->
      <div class="bg-gray-900 rounded-lg p-4 border border-gray-700">
        <div class="flex items-center gap-2 mb-3">
          <GitBranch class="w-4 h-4 text-emerald-400" />
          <span class="text-sm font-medium text-emerald-400">Merged (RRF)</span>
          <span class="text-xs text-gray-500">{{ store.queryResult.merged?.length || 0 }}条</span>
        </div>
        <div v-if="store.queryResult.merged?.length" class="space-y-2">
          <div v-for="(item, idx) in store.queryResult.merged" :key="item.memory_id"
            class="flex items-start gap-3 p-2 rounded bg-gray-800/50 text-sm">
            <span class="text-gray-500 font-mono w-6 text-right shrink-0">#{{ idx + 1 }}</span>
            <div class="flex-1 min-w-0">
              <div class="text-gray-200 break-words">{{ item.content }}</div>
              <div class="flex gap-3 mt-1 text-xs text-gray-500">
                <span>score: {{ formatScore(item.score) }}</span>
                <span>source: {{ item.source }}</span>
                <span v-if="item.scope">scope: {{ item.scope }}</span>
                <span v-if="item.kind">kind: {{ item.kind }}</span>
              </div>
            </div>
          </div>
        </div>
        <div v-else class="text-sm text-gray-500">无匹配结果</div>
      </div>
    </div>

    <div v-else class="flex-1 flex items-center justify-center text-gray-500 text-sm">
      输入查询并点击执行
    </div>
  </div>
</template>
