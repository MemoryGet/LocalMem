<script setup lang="ts">
import { ref } from 'vue'
import { useTestSocket } from './composables/useTestSocket'
import TopBar from './components/TopBar.vue'
import ProgressBar from './components/ProgressBar.vue'
import TestSidebar from './components/TestSidebar.vue'
import TestDetail from './components/TestDetail.vue'
import TestFlowGraph from './components/TestFlowGraph.vue'
import PlaygroundView from './components/PlaygroundView.vue'

const { connected, runTests, stopTests } = useTestSocket()
const activeTab = ref<'tests' | 'playground'>('tests')
</script>

<template>
  <div class="h-screen flex flex-col bg-gray-950 text-gray-300">
    <!-- Tab 栏 -->
    <div class="flex items-center border-b border-gray-800">
      <button @click="activeTab = 'tests'"
        class="px-6 py-2 text-sm font-medium transition-colors"
        :class="activeTab === 'tests' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-500 hover:text-gray-300'">
        Go Tests
      </button>
      <button @click="activeTab = 'playground'"
        class="px-6 py-2 text-sm font-medium transition-colors"
        :class="activeTab === 'playground' ? 'text-emerald-400 border-b-2 border-emerald-400' : 'text-gray-500 hover:text-gray-300'">
        Playground
      </button>
      <div class="flex-1" />
    </div>

    <!-- Go Tests 视图 (现有) -->
    <template v-if="activeTab === 'tests'">
      <TopBar :connected="connected" @run="runTests()" @stop="stopTests()" />
      <ProgressBar />
      <div class="flex flex-1 overflow-hidden">
        <TestSidebar />
        <main class="flex-1 overflow-auto flex items-center justify-center p-6">
          <TestFlowGraph />
        </main>
        <TestDetail />
      </div>
    </template>

    <!-- Playground 视图 (新增) -->
    <template v-if="activeTab === 'playground'">
      <PlaygroundView />
    </template>
  </div>
</template>
