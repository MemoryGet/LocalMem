<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  x1: number
  y1: number
  x2: number
  y2: number
  active: boolean
}>()

const pathId = computed(() => `edge-${props.x1}-${props.y1}-${props.x2}-${props.y2}`)
const pathD = computed(() => `M${props.x1},${props.y1} L${props.x2},${props.y2}`)
</script>

<template>
  <g>
    <path :id="pathId" :d="pathD" fill="none"
          :stroke="active ? '#3b82f6' : '#4b5563'" stroke-width="2"
          marker-end="url(#arrowhead)" />
    <!-- Animated particle when active -->
    <circle v-if="active" r="3" fill="#60a5fa">
      <animateMotion dur="0.8s" repeatCount="indefinite">
        <mpath :href="`#${pathId}`" />
      </animateMotion>
    </circle>
  </g>
</template>
