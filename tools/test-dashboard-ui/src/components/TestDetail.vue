<script setup lang="ts">
import { computed } from 'vue'
import { CheckCircle, XCircle, Info, Clock, Loader } from 'lucide-vue-next'
import { useTestStore } from '../stores/testStore'

const store = useTestStore()
const tc = computed(() => store.selectedCaseData)

// 回放模式下当前高亮的节点信息
const highlightedNode = computed(() => {
  void store.version
  if (!store.replayMode || !tc.value) return null
  const idx = store.replayIndex
  const inputs = tc.value.inputs
  const steps = tc.value.steps
  const outputs = tc.value.outputs
  let cur = 0
  for (let i = 0; i < inputs.length; i++) {
    if (cur === idx) return { kind: 'input' as const, data: inputs[i], idx: i }
    cur++
  }
  for (let i = 0; i < steps.length; i++) {
    if (cur === idx) return { kind: 'step' as const, data: steps[i], idx: i }
    cur++
  }
  for (let i = 0; i < outputs.length; i++) {
    if (cur === idx) return { kind: 'output' as const, data: outputs[i], idx: i }
    cur++
  }
  return null
})

function statusIcon(status: string) {
  switch (status) {
    case 'pass': case 'ok': return CheckCircle
    case 'fail': return XCircle
    case 'info': return Info
    case 'running': return Loader
    default: return Clock
  }
}

function statusColor(status: string) {
  switch (status) {
    case 'pass': case 'ok': return 'text-green-400'
    case 'fail': return 'text-red-400'
    case 'info': return 'text-blue-400'
    case 'running': return 'text-blue-400 animate-spin'
    default: return 'text-gray-500'
  }
}

function statusBadgeClass(status: string) {
  switch (status) {
    case 'pass': return 'bg-green-900/60 text-green-300 border-green-700'
    case 'fail': return 'bg-red-900/60 text-red-300 border-red-700'
    case 'running': return 'bg-blue-900/60 text-blue-300 border-blue-700'
    default: return 'bg-gray-800/60 text-gray-400 border-gray-700'
  }
}

function formatValue(val: string): string {
  try {
    const parsed = JSON.parse(val)
    return JSON.stringify(parsed, null, 2)
  } catch {
    return val
  }
}
</script>

<template>
  <aside class="w-96 bg-gray-900 border-l border-gray-700 overflow-y-auto flex-shrink-0">
    <div class="p-3 text-xs text-gray-500 uppercase tracking-wider font-semibold border-b border-gray-700">
      Detail
    </div>

    <div v-if="!tc" class="p-4 text-sm text-gray-500 text-center">
      Select a test case to view details
    </div>

    <div v-else class="p-3 space-y-4">
      <!-- Header: Name + Status -->
      <div>
        <div class="flex items-start gap-2 mb-1">
          <span class="text-sm font-semibold text-white leading-snug flex-1">{{ tc.displayName || tc.name }}</span>
          <span class="px-2 py-0.5 rounded border text-xs font-medium whitespace-nowrap"
                :class="statusBadgeClass(tc.status)">
            {{ tc.status.toUpperCase() }}
          </span>
        </div>
        <div v-if="tc.displayName && tc.displayName !== tc.name" class="text-xs text-gray-600 font-mono">
          {{ tc.name }}
        </div>
        <div v-if="tc.durationMs > 0" class="text-xs text-gray-500 mt-1">
          Duration: {{ tc.durationMs }}ms
        </div>
      </div>

      <!-- Description / 测试意义 -->
      <div v-if="tc.description" class="bg-blue-950/40 border border-blue-900/50 rounded-lg p-3">
        <h3 class="text-xs text-blue-400 uppercase tracking-wider mb-1.5 font-semibold">Purpose</h3>
        <p class="text-xs text-gray-300 leading-relaxed whitespace-pre-wrap">{{ tc.description }}</p>
      </div>

      <!-- Highlighted node in replay mode -->
      <div v-if="store.replayMode && highlightedNode"
           class="bg-amber-950/30 border border-amber-800/50 rounded-lg p-3">
        <h3 class="text-xs text-amber-400 uppercase tracking-wider mb-1.5 font-semibold">
          Current: {{ highlightedNode.kind.toUpperCase() }} #{{ highlightedNode.idx + 1 }}
        </h3>
        <template v-if="highlightedNode.kind === 'input'">
          <div class="text-xs text-gray-400 mb-0.5">{{ highlightedNode.data.label }}</div>
          <pre class="text-xs bg-gray-800 rounded p-2 text-green-300 overflow-x-auto whitespace-pre-wrap break-all">{{ formatValue(highlightedNode.data.value) }}</pre>
        </template>
        <template v-else-if="highlightedNode.kind === 'step'">
          <div class="flex items-center gap-1.5 mb-1">
            <component :is="statusIcon(highlightedNode.data.status)" class="w-3.5 h-3.5" :class="statusColor(highlightedNode.data.status)" />
            <span class="text-xs text-gray-300 font-medium">{{ highlightedNode.data.action }}</span>
          </div>
          <div v-if="highlightedNode.data.detail" class="text-xs text-gray-500">{{ highlightedNode.data.detail }}</div>
        </template>
        <template v-else-if="highlightedNode.kind === 'output'">
          <div class="text-xs text-gray-400 mb-0.5">{{ highlightedNode.data.label }}</div>
          <pre class="text-xs bg-gray-800 rounded p-2 text-blue-300 overflow-x-auto whitespace-pre-wrap break-all">{{ formatValue(highlightedNode.data.value) }}</pre>
        </template>
      </div>

      <!-- Full Inputs -->
      <div v-if="tc.inputs.length > 0">
        <h3 class="text-xs text-gray-500 uppercase tracking-wider mb-2 font-semibold flex items-center gap-1">
          <span class="w-1.5 h-1.5 rounded-full bg-green-500 inline-block"></span>
          Inputs ({{ tc.inputs.length }})
        </h3>
        <div v-for="(inp, i) of tc.inputs" :key="i" class="mb-3"
             :class="{ 'opacity-40': store.replayMode && i >= store.visibleUpTo }">
          <div class="text-xs text-gray-400 mb-0.5 font-medium">{{ inp.label }}</div>
          <pre class="text-xs bg-gray-800 rounded p-2 text-green-300 overflow-x-auto whitespace-pre-wrap break-all max-h-32 overflow-y-auto">{{ formatValue(inp.value) }}</pre>
        </div>
      </div>

      <!-- Full Steps -->
      <div v-if="tc.steps.length > 0">
        <h3 class="text-xs text-gray-500 uppercase tracking-wider mb-2 font-semibold flex items-center gap-1">
          <span class="w-1.5 h-1.5 rounded-full bg-blue-500 inline-block"></span>
          Steps ({{ tc.steps.length }})
        </h3>
        <div v-for="(step, si) of tc.steps" :key="step.seq" class="flex items-start gap-2 mb-2.5"
             :class="{ 'opacity-40': store.replayMode && (tc.inputs.length + si) >= store.visibleUpTo }">
          <div class="flex items-center gap-1 flex-shrink-0 mt-0.5">
            <span class="text-xs text-gray-600 tabular-nums w-4 text-right">{{ step.seq }}</span>
            <component :is="statusIcon(step.status)" class="w-3.5 h-3.5" :class="statusColor(step.status)" />
          </div>
          <div class="min-w-0 flex-1">
            <div class="text-xs text-gray-300 font-medium">{{ step.action }}</div>
            <div v-if="step.detail" class="text-xs text-gray-500 mt-0.5 whitespace-pre-wrap break-all">{{ step.detail }}</div>
          </div>
        </div>
      </div>

      <!-- Full Outputs -->
      <div v-if="tc.outputs.length > 0">
        <h3 class="text-xs text-gray-500 uppercase tracking-wider mb-2 font-semibold flex items-center gap-1">
          <span class="w-1.5 h-1.5 rounded-full bg-purple-500 inline-block"></span>
          Outputs ({{ tc.outputs.length }})
        </h3>
        <div v-for="(out, i) of tc.outputs" :key="i" class="mb-3"
             :class="{ 'opacity-40': store.replayMode && (tc.inputs.length + tc.steps.length + i) >= store.visibleUpTo }">
          <div class="text-xs text-gray-400 mb-0.5 font-medium">{{ out.label }}</div>
          <pre class="text-xs bg-gray-800 rounded p-2 text-blue-300 overflow-x-auto whitespace-pre-wrap break-all max-h-40 overflow-y-auto">{{ formatValue(out.value) }}</pre>
        </div>
      </div>

      <!-- Logs -->
      <div v-if="tc.logs.length > 0">
        <h3 class="text-xs text-gray-500 uppercase tracking-wider mb-2 font-semibold flex items-center gap-1">
          <span class="w-1.5 h-1.5 rounded-full bg-gray-500 inline-block"></span>
          Logs ({{ tc.logs.length }})
        </h3>
        <pre class="text-xs bg-gray-800 rounded p-2 text-gray-400 overflow-x-auto whitespace-pre-wrap break-all max-h-60 overflow-y-auto">{{ tc.logs.join('\n') }}</pre>
      </div>
    </div>
  </aside>
</template>
