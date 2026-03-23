<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { CheckCircle, XCircle, Loader, Clock, MinusCircle, ChevronRight, ChevronDown } from 'lucide-vue-next'
import { useTestStore } from '../stores/testStore'
import type { TestCase } from '../stores/testStore'

const store = useTestStore()
const expandedSuites = ref<Set<string>>(new Set())
const currentRef = ref<HTMLElement | null>(null)

function toggleSuite(name: string) {
  if (expandedSuites.value.has(name)) {
    expandedSuites.value.delete(name)
  } else {
    expandedSuites.value.add(name)
  }
}

function selectCase(name: string) {
  store.selectedCase = name
}

function statusIcon(status: TestCase['status']) {
  switch (status) {
    case 'pass': return CheckCircle
    case 'fail': return XCircle
    case 'running': return Loader
    case 'skip': return MinusCircle
    default: return Clock
  }
}

function statusColor(status: TestCase['status']) {
  switch (status) {
    case 'pass': return 'text-green-400'
    case 'fail': return 'text-red-400'
    case 'running': return 'text-blue-400 animate-spin'
    case 'skip': return 'text-gray-500'
    default: return 'text-gray-600'
  }
}

// Auto-expand suite and scroll to current running test
watch(() => store.currentCase, async (name) => {
  if (!name) return
  for (const suiteName of store.suiteOrder) {
    const suite = store.suites[suiteName]
    if (suite && suite.cases[name]) {
      expandedSuites.value.add(suiteName)
      break
    }
  }
  await nextTick()
  currentRef.value?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
})
</script>

<template>
  <aside class="w-72 bg-gray-900 border-r border-gray-700 overflow-y-auto flex-shrink-0">
    <div class="p-3 text-xs text-gray-500 uppercase tracking-wider font-semibold border-b border-gray-700">
      Test Suites
    </div>

    <div v-if="store.suiteOrder.length === 0" class="p-4 text-sm text-gray-500 text-center">
      Click "Run All" to start tests
    </div>

    <div v-for="suiteName of store.suiteOrder" :key="suiteName">
      <button @click="toggleSuite(suiteName)"
              class="w-full flex items-center gap-2 px-3 py-2 text-sm text-gray-300 hover:bg-gray-800 transition-colors">
        <component :is="expandedSuites.has(suiteName) ? ChevronDown : ChevronRight" class="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />
        <span class="font-medium truncate">{{ suiteName }}</span>
        <span class="ml-auto text-xs text-gray-500">
          <span v-if="store.suites[suiteName]?.passed" class="text-green-500">{{ store.suites[suiteName].passed }}</span>
          <span v-if="store.suites[suiteName]?.passed && store.suites[suiteName]?.failed"> / </span>
          <span v-if="store.suites[suiteName]?.failed" class="text-red-500">{{ store.suites[suiteName].failed }}</span>
        </span>
      </button>

      <div v-if="expandedSuites.has(suiteName) && store.suites[suiteName]">
        <button v-for="caseName of store.suites[suiteName].caseOrder" :key="caseName"
                :ref="el => { if (caseName === store.currentCase) currentRef = el as HTMLElement }"
                @click="selectCase(caseName)"
                class="w-full flex items-center gap-2 pl-8 pr-3 py-1.5 text-xs hover:bg-gray-800 transition-colors"
                :class="store.selectedCase === caseName ? 'bg-gray-800 text-white' : 'text-gray-400'">
          <component :is="statusIcon(store.suites[suiteName].cases[caseName]?.status || 'pending')"
                     class="w-3.5 h-3.5 flex-shrink-0"
                     :class="statusColor(store.suites[suiteName].cases[caseName]?.status || 'pending')" />
          <span class="truncate">{{ store.suites[suiteName].cases[caseName]?.displayName || caseName }}</span>
          <span v-if="store.suites[suiteName].cases[caseName]?.durationMs > 0"
                class="ml-auto text-gray-600 tabular-nums whitespace-nowrap">
            {{ store.suites[suiteName].cases[caseName].durationMs }}ms
          </span>
        </button>
      </div>
    </div>
  </aside>
</template>
