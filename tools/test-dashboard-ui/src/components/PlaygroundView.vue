<script setup lang="ts">
import DatasetSelector from './DatasetSelector.vue'
import BatchCases from './BatchCases.vue'
import QueryResult from './QueryResult.vue'
</script>

<template>
  <div class="flex flex-col flex-1 overflow-hidden">
    <DatasetSelector />
    <div class="flex flex-1 overflow-hidden">
      <BatchCases />
      <div class="flex-1 overflow-hidden">
        <QueryResult />
      </div>
    </div>
  </div>
</template>
