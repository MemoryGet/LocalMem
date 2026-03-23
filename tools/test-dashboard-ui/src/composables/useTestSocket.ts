import { ref, onUnmounted } from 'vue'
import { useTestStore } from '../stores/testStore'
import type { TestEvent } from '../types/events'

export function useTestSocket() {
  const store = useTestStore()
  const connected = ref(false)
  let ws: WebSocket | null = null
  let reconnectTimer: number | null = null

  function connect() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${location.host}/ws`
    ws = new WebSocket(url)

    ws.onopen = () => {
      connected.value = true
      if (store.running) {
        ws?.send(JSON.stringify({ action: 'sync' }))
      }
    }

    ws.onmessage = (e) => {
      try {
        const evt: TestEvent = JSON.parse(e.data)
        store.handleEvent(evt)
      } catch { /* ignore parse errors */ }
    }

    ws.onclose = () => {
      connected.value = false
      reconnectTimer = window.setTimeout(connect, 2000)
    }
  }

  function runTests(suite?: string) {
    store.reset()
    store.running = true
    const msg: Record<string, string> = { action: 'run' }
    if (suite) msg.suite = suite
    ws?.send(JSON.stringify(msg))
  }

  function stopTests() {
    ws?.send(JSON.stringify({ action: 'stop' }))
  }

  connect()

  onUnmounted(() => {
    if (reconnectTimer) clearTimeout(reconnectTimer)
    ws?.close()
  })

  return { connected, runTests, stopTests }
}
