import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export interface DatasetInfo {
  name: string
  file_name: string
  description: string
  stats: { memories: number; entities: number; relations: number; cases: number }
}

export interface PreprocessResult {
  original_query: string
  semantic_query: string
  keywords: string[]
  entities: string[]
  intent: string
  weights: { fts: number; qdrant: number; graph: number }
}

export interface ChannelResult {
  available: boolean
  count: number
  results: MemoryResult[]
}

export interface MemoryResult {
  memory_id: string
  content: string
  score: number
  source: string
  scope?: string
  kind?: string
}

export interface QueryResult {
  preprocess: PreprocessResult | null
  channels: Record<string, ChannelResult>
  merged: MemoryResult[]
  duration_ms: number
}

export interface CaseResult {
  name: string
  query: string
  description: string
  expected_intent: string
  actual_intent: string
  passed: boolean
  result_count: number
  duration_ms: number
  preprocess: PreprocessResult | null
  top_results: MemoryResult[]
}

export interface BatchResult {
  dataset: string
  results: CaseResult[]
  summary: { total: number; passed: number; failed: number }
  duration_ms: number
}

export const usePlaygroundStore = defineStore('playground', () => {
  const datasets = ref<DatasetInfo[]>([])
  const loadedDataset = ref<{ name: string; stats: DatasetInfo['stats'] } | null>(null)
  const loading = ref(false)

  const queryResult = ref<QueryResult | null>(null)
  const querying = ref(false)

  const batchResult = ref<BatchResult | null>(null)
  const batchRunning = ref(false)

  const selectedCase = ref<CaseResult | null>(null)

  const isLoaded = computed(() => loadedDataset.value !== null)

  async function fetchDatasets() {
    const res = await fetch('/api/datasets')
    if (res.ok) {
      datasets.value = await res.json()
    }
  }

  async function loadDataset(fileName: string) {
    loading.value = true
    queryResult.value = null
    batchResult.value = null
    selectedCase.value = null
    try {
      const res = await fetch('/api/datasets/load', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: fileName }),
      })
      if (res.ok) {
        loadedDataset.value = await res.json()
      }
    } finally {
      loading.value = false
    }
  }

  async function executeQuery(query: string, limit = 10) {
    querying.value = true
    try {
      const res = await fetch('/api/query', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query, limit }),
      })
      if (res.ok) {
        queryResult.value = await res.json()
      }
    } finally {
      querying.value = false
    }
  }

  async function runBatchCases() {
    batchRunning.value = true
    selectedCase.value = null
    try {
      const res = await fetch('/api/cases/run', { method: 'POST' })
      if (res.ok) {
        batchResult.value = await res.json()
      }
    } finally {
      batchRunning.value = false
    }
  }

  return {
    datasets, loadedDataset, loading, isLoaded,
    queryResult, querying,
    batchResult, batchRunning,
    selectedCase,
    fetchDatasets, loadDataset, executeQuery, runBatchCases,
  }
})
