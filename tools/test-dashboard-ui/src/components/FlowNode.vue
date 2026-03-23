<script setup lang="ts">
defineProps<{
  x: number
  y: number
  width: number
  height: number
  label: string
  sublabel?: string
  status: 'pending' | 'running' | 'ok' | 'pass' | 'fail' | 'info'
  nodeType: 'input' | 'step' | 'output'
}>()

function fillColor(status: string) {
  switch (status) {
    case 'running': return '#1e40af'
    case 'ok': case 'pass': return '#166534'
    case 'fail': return '#991b1b'
    case 'info': return '#1e3a5f'
    default: return '#1f2937'
  }
}

function strokeColor(status: string) {
  switch (status) {
    case 'running': return '#3b82f6'
    case 'ok': case 'pass': return '#22c55e'
    case 'fail': return '#ef4444'
    case 'info': return '#3b82f6'
    default: return '#4b5563'
  }
}
</script>

<template>
  <g :transform="`translate(${x}, ${y})`">
    <rect
      :width="width"
      :height="height"
      :rx="nodeType === 'input' || nodeType === 'output' ? 20 : 6"
      :fill="fillColor(status)"
      :stroke="strokeColor(status)"
      stroke-width="2"
      :class="{ 'animate-pulse-glow': status === 'running' }"
    />
    <text
      :x="width / 2"
      :y="sublabel ? height / 2 - 6 : height / 2"
      text-anchor="middle"
      dominant-baseline="central"
      fill="white"
      font-size="12"
      font-weight="600"
      class="select-none pointer-events-none"
    >{{ label }}</text>
    <text
      v-if="sublabel"
      :x="width / 2"
      :y="height / 2 + 10"
      text-anchor="middle"
      dominant-baseline="central"
      fill="#9ca3af"
      font-size="10"
      class="select-none pointer-events-none"
    >{{ sublabel }}</text>
  </g>
</template>
