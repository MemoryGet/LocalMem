<script setup lang="ts">
import { computed } from 'vue'
import { useTestStore } from '../stores/testStore'
import { Play, SkipBack, SkipForward, ChevronsRight } from 'lucide-vue-next'

const store = useTestStore()
const tc = computed(() => store.selectedCaseData)

interface NodeDef {
  id: string
  label: string
  sublabel?: string
  kind: 'input' | 'step' | 'output' | 'status'
  status: 'pending' | 'running' | 'ok' | 'pass' | 'fail' | 'info'
  globalIdx: number // 在所有流程节点中的序号
}

const NODE_W = 280
const NODE_H = 52
const GAP_Y = 24
const PAD_X = 20
const PAD_Y = 20

const allNodes = computed<NodeDef[]>(() => {
  void store.version
  if (!tc.value) return []
  const result: NodeDef[] = []
  const hasFlowData = tc.value.inputs.length > 0 || tc.value.steps.length > 0 || tc.value.outputs.length > 0
  let idx = 0

  if (!hasFlowData) {
    const s = tc.value.status
    const sm: Record<string, NodeDef['status']> = { pass: 'pass', fail: 'fail', running: 'running', skip: 'pending', pending: 'pending' }
    result.push({ id: 'test', label: tc.value.displayName || tc.value.name, kind: 'status', status: sm[s] || 'pending', globalIdx: idx++ })
    if (tc.value.durationMs > 0) {
      result.push({ id: 'result', label: `${s.toUpperCase()} — ${tc.value.durationMs}ms`, kind: 'status', status: sm[s] || 'pending', globalIdx: idx++ })
    }
    return result
  }

  for (let i = 0; i < tc.value.inputs.length; i++) {
    const inp = tc.value.inputs[i]
    result.push({ id: `in-${i}`, label: inp.label, sublabel: inp.value, kind: 'input', status: 'ok', globalIdx: idx++ })
  }
  for (const step of tc.value.steps) {
    result.push({ id: `step-${step.seq}`, label: step.action, sublabel: step.detail || undefined, kind: 'step', status: step.status as NodeDef['status'], globalIdx: idx++ })
  }
  if (tc.value.status === 'running' && tc.value.outputs.length === 0) {
    result.push({ id: 'running', label: 'Running...', kind: 'step', status: 'running', globalIdx: idx++ })
  }
  for (let i = 0; i < tc.value.outputs.length; i++) {
    const out = tc.value.outputs[i]
    result.push({ id: `out-${i}`, label: out.label, sublabel: out.value, kind: 'output', status: tc.value.status === 'fail' ? 'fail' : 'pass', globalIdx: idx++ })
  }
  return result
})

// 回放模式下可见的节点
const visibleNodes = computed(() => {
  if (!store.replayMode) return allNodes.value
  return allNodes.value.filter(n => n.globalIdx < store.visibleUpTo)
})

// 当前高亮的节点
const highlightIdx = computed(() => {
  if (!store.replayMode) return -1
  return store.replayIndex
})

const svgW = computed(() => PAD_X * 2 + NODE_W)
const svgH = computed(() => {
  const n = visibleNodes.value.length || 1
  return PAD_Y * 2 + n * NODE_H + (n - 1) * GAP_Y
})

function nodeY(i: number) { return PAD_Y + i * (NODE_H + GAP_Y) }

function fillColor(status: string, highlighted: boolean) {
  if (highlighted) {
    switch (status) {
      case 'ok': case 'pass': return '#166534'
      case 'fail': return '#991b1b'
      default: return '#1e3a5f'
    }
  }
  switch (status) {
    case 'running': return '#1e3a5f'
    case 'ok': case 'pass': return '#14352a'
    case 'fail': return '#3b1111'
    case 'info': return '#1e2d4a'
    default: return '#1f2937'
  }
}

function strokeColor(status: string, highlighted: boolean) {
  if (highlighted) return '#f59e0b'
  switch (status) {
    case 'running': return '#3b82f6'
    case 'ok': case 'pass': return '#22c55e'
    case 'fail': return '#ef4444'
    case 'info': return '#60a5fa'
    default: return '#4b5563'
  }
}

function kindLabel(kind: string) {
  switch (kind) {
    case 'input': return 'IN'
    case 'output': return 'OUT'
    case 'step': return 'STEP'
    default: return ''
  }
}

function trunc(s: string, max: number) {
  if (!s) return ''
  return s.length > max ? s.slice(0, max) + '…' : s
}

function onNodeClick(node: NodeDef) {
  if (store.replayMode) {
    store.replayGoTo(node.globalIdx)
  }
}
</script>

<template>
  <div class="flex flex-col items-center gap-3 w-full h-full overflow-auto p-4">
    <!-- Description -->
    <div v-if="tc?.description" class="text-xs text-gray-400 bg-gray-800/50 rounded px-3 py-2 max-w-sm text-center leading-relaxed">
      {{ tc.description }}
    </div>

    <!-- Empty state -->
    <div v-if="!tc" class="text-gray-600 text-sm select-none flex-1 flex items-center">
      Select a test case to view its flow graph
    </div>

    <!-- Replay controls -->
    <div v-if="tc && allNodes.length > 1" class="flex items-center gap-2">
      <template v-if="!store.replayMode">
        <button @click="store.enterReplay()"
                class="flex items-center gap-1 px-2.5 py-1 rounded bg-gray-800 hover:bg-gray-700 text-xs text-gray-300 transition-colors">
          <Play class="w-3 h-3" /> Step Mode
        </button>
      </template>
      <template v-else>
        <button @click="store.replayPrev()" :disabled="store.replayIndex <= 0"
                class="p-1 rounded bg-gray-800 hover:bg-gray-700 disabled:opacity-30 text-gray-300 transition-colors">
          <SkipBack class="w-3.5 h-3.5" />
        </button>
        <span class="text-xs text-gray-400 tabular-nums min-w-[48px] text-center">
          {{ store.replayIndex + 1 }} / {{ store.totalFlowNodes }}
        </span>
        <button @click="store.replayNext()" :disabled="store.replayIndex >= store.totalFlowNodes - 1"
                class="p-1 rounded bg-gray-800 hover:bg-gray-700 disabled:opacity-30 text-gray-300 transition-colors">
          <SkipForward class="w-3.5 h-3.5" />
        </button>
        <button @click="store.exitReplay()"
                class="flex items-center gap-1 px-2 py-1 rounded bg-gray-800 hover:bg-gray-700 text-xs text-gray-300 transition-colors">
          <ChevronsRight class="w-3 h-3" /> Show All
        </button>
      </template>
    </div>

    <!-- Flow Graph -->
    <svg v-if="tc && visibleNodes.length > 0"
         :viewBox="`0 0 ${svgW} ${svgH}`"
         :width="Math.min(svgW, 360)" :style="{ maxHeight: svgH + 'px' }"
         class="flex-shrink-0">
      <defs>
        <marker id="arrow" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto">
          <polygon points="0 0, 8 3, 0 6" fill="#4b5563" />
        </marker>
        <marker id="arrow-hl" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto">
          <polygon points="0 0, 8 3, 0 6" fill="#f59e0b" />
        </marker>
      </defs>

      <!-- Edges -->
      <template v-for="i in visibleNodes.length - 1" :key="'e'+i">
        <line
          :x1="PAD_X + NODE_W / 2" :y1="nodeY(i - 1) + NODE_H"
          :x2="PAD_X + NODE_W / 2" :y2="nodeY(i)"
          :stroke="visibleNodes[i]?.globalIdx === highlightIdx ? '#f59e0b' : '#4b5563'"
          stroke-width="2"
          :marker-end="visibleNodes[i]?.globalIdx === highlightIdx ? 'url(#arrow-hl)' : 'url(#arrow)'"
        />
      </template>

      <!-- Nodes -->
      <g v-for="(node, i) in visibleNodes" :key="node.id"
         :transform="`translate(${PAD_X}, ${nodeY(i)})`"
         class="cursor-pointer" @click="onNodeClick(node)">
        <rect :width="NODE_W" :height="NODE_H"
              :rx="node.kind === 'input' || node.kind === 'output' ? 22 : 6"
              :fill="fillColor(node.status, node.globalIdx === highlightIdx)"
              :stroke="strokeColor(node.status, node.globalIdx === highlightIdx)"
              :stroke-width="node.globalIdx === highlightIdx ? 3 : 2"
              :class="{ 'animate-pulse-glow': node.status === 'running' }"
              class="transition-all duration-200" />

        <!-- Kind badge -->
        <template v-if="node.kind !== 'status'">
          <rect x="8" :y="NODE_H/2 - 9" width="36" height="18" rx="4"
                :fill="strokeColor(node.status, node.globalIdx === highlightIdx)" opacity="0.3" />
          <text x="26" :y="NODE_H/2" text-anchor="middle" dominant-baseline="central"
                :fill="strokeColor(node.status, node.globalIdx === highlightIdx)" font-size="9" font-weight="700"
                class="select-none">
            {{ kindLabel(node.kind) }}
          </text>
        </template>

        <!-- Label -->
        <text :x="node.kind !== 'status' ? 52 : NODE_W/2" :y="node.sublabel ? NODE_H/2 - 8 : NODE_H/2"
              :text-anchor="node.kind !== 'status' ? 'start' : 'middle'" dominant-baseline="central"
              fill="white" font-size="12" font-weight="600" class="select-none">
          {{ trunc(node.label, 28) }}
        </text>
        <!-- Sublabel -->
        <text v-if="node.sublabel"
              :x="node.kind !== 'status' ? 52 : NODE_W/2" :y="NODE_H/2 + 10"
              :text-anchor="node.kind !== 'status' ? 'start' : 'middle'" dominant-baseline="central"
              fill="#9ca3af" font-size="10" class="select-none">
          {{ trunc(node.sublabel, 32) }}
        </text>
      </g>
    </svg>
  </div>
</template>

<style>
@keyframes pulse-glow {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
.animate-pulse-glow {
  animation: pulse-glow 1.2s ease-in-out infinite;
}
</style>
