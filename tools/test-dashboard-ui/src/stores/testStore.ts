import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { TestEvent } from '../types/events'

export interface TestCase {
  name: string
  displayName: string
  description: string
  suite: string
  status: 'pending' | 'running' | 'pass' | 'fail' | 'skip'
  durationMs: number
  inputs: { label: string; value: string }[]
  steps: { seq: number; action: string; detail: string; status: string }[]
  outputs: { label: string; value: string }[]
  logs: string[]
}

export interface TestSuite {
  name: string
  cases: Record<string, TestCase>
  caseOrder: string[]
  passed: number
  failed: number
  durationMs: number
}

export const useTestStore = defineStore('test', () => {
  const suites = ref<Record<string, TestSuite>>({})
  const suiteOrder = ref<string[]>([])
  const running = ref(false)
  const selectedCase = ref<string | null>(null)
  const currentCase = ref<string | null>(null)
  const totalPassed = ref(0)
  const totalFailed = ref(0)
  const version = ref(0)

  // 单步回放状态
  const replayIndex = ref(-1) // -1 = 全部显示, 0..N = 当前步骤
  const replayMode = ref(false)

  const allCases = computed(() => {
    void version.value
    const cases: TestCase[] = []
    for (const sn of suiteOrder.value) {
      const s = suites.value[sn]
      if (s) {
        for (const cn of s.caseOrder) {
          const c = s.cases[cn]
          if (c) cases.push(c)
        }
      }
    }
    return cases
  })

  const selectedCaseData = computed(() => {
    void version.value
    if (!selectedCase.value) return null
    for (const sn of suiteOrder.value) {
      const s = suites.value[sn]
      if (s && s.cases[selectedCase.value]) {
        return s.cases[selectedCase.value]
      }
    }
    return null
  })

  // 当前回放可见的流程节点总数
  const totalFlowNodes = computed(() => {
    const tc = selectedCaseData.value
    if (!tc) return 0
    return tc.inputs.length + tc.steps.length + tc.outputs.length
  })

  // 回放时可见的节点截止索引
  const visibleUpTo = computed(() => {
    if (!replayMode.value || replayIndex.value < 0) return totalFlowNodes.value
    return Math.min(replayIndex.value + 1, totalFlowNodes.value)
  })

  function bump() { version.value++ }

  function reset() {
    suites.value = {}
    suiteOrder.value = []
    totalPassed.value = 0
    totalFailed.value = 0
    selectedCase.value = null
    currentCase.value = null
    version.value = 0
    replayIndex.value = -1
    replayMode.value = false
  }

  function enterReplay() {
    replayMode.value = true
    replayIndex.value = 0
  }

  function exitReplay() {
    replayMode.value = false
    replayIndex.value = -1
  }

  function replayNext() {
    if (replayIndex.value < totalFlowNodes.value - 1) {
      replayIndex.value++
    }
  }

  function replayPrev() {
    if (replayIndex.value > 0) {
      replayIndex.value--
    }
  }

  function replayGoTo(idx: number) {
    replayIndex.value = Math.max(0, Math.min(idx, totalFlowNodes.value - 1))
  }

  function getOrCreateSuite(name: string): TestSuite {
    if (!suites.value[name]) {
      suites.value[name] = {
        name, cases: {}, caseOrder: [], passed: 0, failed: 0, durationMs: 0,
      }
      suiteOrder.value.push(name)
    }
    return suites.value[name]
  }

  function getOrCreateCase(suite: string, name: string): TestCase {
    const s = getOrCreateSuite(suite)
    if (!s.cases[name]) {
      s.cases[name] = {
        name, displayName: name, description: '', suite, status: 'pending', durationMs: 0,
        inputs: [], steps: [], outputs: [], logs: [],
      }
      s.caseOrder.push(name)
    }
    return s.cases[name]
  }

  function handleEvent(evt: TestEvent) {
    const dn = (evt as any).display_name
    switch (evt.type) {
      case 'suite_start':
        getOrCreateSuite(evt.suite)
        break
      case 'case_start': {
        const tc = getOrCreateCase(evt.suite, evt.name)
        tc.status = 'running'
        currentCase.value = evt.name
        selectedCase.value = evt.name
        replayMode.value = false
        replayIndex.value = -1
        break
      }
      case 'description': {
        const dc = getOrCreateCase(evt.suite, evt.name)
        if (dn) dc.displayName = dn
        dc.description = (evt as any).desc || ''
        break
      }
      case 'input': {
        const ic = getOrCreateCase(evt.suite, evt.name)
        if (dn) ic.displayName = dn
        ic.inputs.push({ label: evt.label!, value: evt.value! })
        break
      }
      case 'step': {
        const sc = getOrCreateCase(evt.suite, evt.name)
        if (dn) sc.displayName = dn
        sc.steps.push({
          seq: evt.seq!, action: evt.action!, detail: evt.detail || '', status: evt.status || 'ok',
        })
        break
      }
      case 'output': {
        const oc = getOrCreateCase(evt.suite, evt.name)
        if (dn) oc.displayName = dn
        oc.outputs.push({ label: evt.label!, value: evt.value! })
        break
      }
      case 'case_end': {
        const ec = getOrCreateCase(evt.suite, evt.name)
        ec.status = evt.status
        ec.durationMs = evt.duration_ms
        break
      }
      case 'suite_end': {
        const se = getOrCreateSuite(evt.suite)
        se.passed = evt.passed
        se.failed = evt.failed
        se.durationMs = evt.duration_ms
        break
      }
      case 'done':
        running.value = false
        totalPassed.value = evt.total_passed
        totalFailed.value = evt.total_failed
        currentCase.value = null
        break
      case 'log': {
        const lc = getOrCreateCase(evt.suite, evt.name)
        lc.logs.push(evt.text)
        break
      }
      case 'stopped':
        running.value = false
        currentCase.value = null
        break
      case 'snapshot':
        running.value = evt.running
        break
      case 'error':
        running.value = false
        console.error('Test error:', evt.msg)
        break
    }
    bump()
  }

  return {
    suites, suiteOrder, running, selectedCase, currentCase, totalPassed, totalFailed,
    version, allCases, selectedCaseData, totalFlowNodes, visibleUpTo,
    replayMode, replayIndex,
    reset, handleEvent, enterReplay, exitReplay, replayNext, replayPrev, replayGoTo,
  }
})
