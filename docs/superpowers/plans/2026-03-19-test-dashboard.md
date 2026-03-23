# IClude Test Dashboard Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a real-time test visualization dashboard that shows each test case's input/steps/output as an animated flow graph via WebSocket.

**Architecture:** Go WebSocket server (`cmd/test-dashboard/`) runs `go test -v -json` and pushes events to a Vue 3 + Vite frontend (`tools/test-dashboard-ui/`). Tests use `pkg/testreport` to emit structured step data via `##TESTREPORT##` prefixed stdout lines.

**Tech Stack:** Go (nhooyr.io/websocket), Vue 3, Vite, Pinia, Tailwind CSS, Custom SVG flow graph

**Spec:** `docs/superpowers/specs/2026-03-19-test-dashboard-design.md`

---

## Task 1: Add JSON stdout mode to pkg/testreport

**Files:**
- Modify: `pkg/testreport/reporter.go`

- [ ] **Step 1: Add `emitJSON` helper function**

Add at bottom of `reporter.go`:

```go
// emitJSON 向 stdout 发送 ##TESTREPORT## JSON 行 / Emit JSON line to stdout for dashboard
func emitJSON(data map[string]any) {
	if os.Getenv("TESTREPORT_JSON") != "1" {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stdout, "##TESTREPORT##%s\n", b)
}
```

- [ ] **Step 2: Inject emitJSON calls into Input, Step, Output, Done methods**

In `Case.Input()` — add after appending to `c.Inputs`:
```go
emitJSON(map[string]any{
    "type": "input", "name": c.Name, "label": label, "value": value,
})
```

In `Case.Step()` — add after appending to `c.Steps`:
```go
emitJSON(map[string]any{
    "type": "step", "name": c.Name, "seq": c.step, "action": action, "detail": d, "status": "ok",
})
```

In `Case.StepInfo()` — add after appending:
```go
emitJSON(map[string]any{
    "type": "step", "name": c.Name, "seq": c.step, "action": action, "detail": d, "status": "info",
})
```

In `Case.StepFail()` — add after appending:
```go
emitJSON(map[string]any{
    "type": "step", "name": c.Name, "seq": c.step, "action": action, "detail": d, "status": "fail",
})
```

In `Case.Output()` — add after appending:
```go
emitJSON(map[string]any{
    "type": "output", "name": c.Name, "label": label, "value": value,
})
```

In `Case.OutputCode()` — add after appending:
```go
emitJSON(map[string]any{
    "type": "output", "name": c.Name, "label": label, "value": value,
})
```

In `Case.InputCode()` — add after appending:
```go
emitJSON(map[string]any{
    "type": "input", "name": c.Name, "label": label, "value": value,
})
```

In `Case.InputSQL()` — add after appending:
```go
emitJSON(map[string]any{
    "type": "input", "name": c.Name, "label": label, "value": value,
})
```

In `Case.Done()` — add at end of function (note: type is "case_end" to match spec):
```go
emitJSON(map[string]any{
    "type": "case_end", "name": c.Name, "status": c.Status, "duration": c.Duration,
})
```

- [ ] **Step 3: Verify existing tests still pass**

Run: `go test ./testing/... 2>&1 | grep -E "(ok|FAIL)" | head -10`
Expected: All packages pass (emitJSON is no-op without env var)

- [ ] **Step 4: Verify JSON mode works**

Run: `TESTREPORT_JSON=1 go test -v ./testing/report/... 2>&1 | grep "##TESTREPORT##" | head -5`
Expected: Lines starting with `##TESTREPORT##` followed by valid JSON

- [ ] **Step 5: Commit**

```bash
git add pkg/testreport/reporter.go
git commit -m "feat(testreport): add JSON stdout mode for dashboard integration"
```

---

## Task 2: Create Go WebSocket test runner server

**Files:**
- Create: `cmd/test-dashboard/main.go`

- [ ] **Step 1: Install nhooyr.io/websocket**

Run: `go get nhooyr.io/websocket@latest`

- [ ] **Step 2: Create `cmd/test-dashboard/main.go`**

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
	running bool
	cancel  context.CancelFunc
	history []json.RawMessage
}

func newHub() *Hub {
	return &Hub{clients: make(map[*websocket.Conn]bool)}
}

func (h *Hub) addClient(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

func (h *Hub) removeClient(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *Hub) broadcast(msg json.RawMessage) {
	h.mu.Lock()
	h.history = append(h.history, msg)
	clients := make([]*websocket.Conn, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		c.Write(context.Background(), websocket.MessageText, msg)
	}
}

func (h *Hub) sendSnapshot(c *websocket.Conn) {
	h.mu.Lock()
	history := make([]json.RawMessage, len(h.history))
	copy(history, h.history)
	running := h.running
	h.mu.Unlock()

	// 发送历史事件让客户端重建状态
	for _, msg := range history {
		c.Write(context.Background(), websocket.MessageText, msg)
	}
	// 发送 snapshot 标记告知重放完成
	snap := map[string]any{"type": "snapshot", "running": running, "replayed": len(history)}
	wsjson.Write(context.Background(), c, snap)
}

// GoTestEvent go test -json 信封结构
type GoTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package,omitempty"`
	Test    string  `json:"Test,omitempty"`
	Output  string  `json:"Output,omitempty"`
	Elapsed float64 `json:"Elapsed,omitempty"`
}

func (h *Hub) runTests(ctx context.Context, suite string) {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		msg, _ := json.Marshal(map[string]any{"type": "error", "msg": "test already running"})
		h.broadcast(msg)
		return
	}
	h.running = true
	h.history = nil
	h.mu.Unlock()

	startTime := time.Now()

	defer func() {
		h.mu.Lock()
		h.running = false
		h.cancel = nil
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.cancel = cancel
	h.mu.Unlock()
	defer cancel()

	// 构造 go test 命令
	pkg := "./testing/..."
	if suite != "" {
		pkg = fmt.Sprintf("./testing/%s/...", suite)
	}

	cmd := exec.CommandContext(ctx, "go", "test", "-v", "-json", pkg)
	cmd.Env = append(os.Environ(), "TESTREPORT_JSON=1")
	cmd.Dir = findProjectRoot()
	cmd.Stderr = os.Stderr // 编译错误/panic 输出到 server 日志

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		msg, _ := json.Marshal(map[string]any{"type": "error", "msg": err.Error()})
		h.broadcast(msg)
		return
	}

	if err := cmd.Start(); err != nil {
		msg, _ := json.Marshal(map[string]any{"type": "error", "msg": err.Error()})
		h.broadcast(msg)
		return
	}

	scanner := bufio.NewScanner(stdout)
	suiteStats := make(map[string]*suiteState)

	for scanner.Scan() {
		line := scanner.Text()
		var evt GoTestEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		suiteName := extractSuite(evt.Package)

		switch evt.Action {
		case "run":
			if evt.Test != "" {
				// 初始化 suite
				if _, ok := suiteStats[suiteName]; !ok {
					suiteStats[suiteName] = &suiteState{}
					msg, _ := json.Marshal(map[string]any{
						"type": "suite_start", "suite": suiteName, "ts": time.Now().UTC().Format(time.RFC3339),
					})
					h.broadcast(msg)
				}
				msg, _ := json.Marshal(map[string]any{
					"type": "case_start", "suite": suiteName, "name": evt.Test,
				})
				h.broadcast(msg)
			}

		case "output":
			output := strings.TrimRight(evt.Output, "\n")
			// 提取 ##TESTREPORT## 增强数据
			if strings.HasPrefix(output, "##TESTREPORT##") {
				payload := output[len("##TESTREPORT##"):]
				var data map[string]any
				if json.Unmarshal([]byte(payload), &data) == nil {
					data["suite"] = suiteName
					msg, _ := json.Marshal(data)
					h.broadcast(msg)
				}
			} else if evt.Test != "" && len(output) > 0 {
				// 普通日志
				msg, _ := json.Marshal(map[string]any{
					"type": "log", "suite": suiteName, "name": evt.Test, "text": output,
				})
				h.broadcast(msg)
			}

		case "pass", "fail", "skip":
			if evt.Test != "" {
				ss := suiteStats[suiteName]
				if ss != nil {
					if evt.Action == "pass" {
						ss.passed++
					} else if evt.Action == "fail" {
						ss.failed++
					}
				}
				msg, _ := json.Marshal(map[string]any{
					"type":        "case_end",
					"suite":       suiteName,
					"name":        evt.Test,
					"status":      evt.Action,
					"duration_ms": int(evt.Elapsed * 1000),
				})
				h.broadcast(msg)
			} else if evt.Package != "" {
				// package-level pass/fail = suite_end
				ss := suiteStats[suiteName]
				passed, failed := 0, 0
				if ss != nil {
					passed, failed = ss.passed, ss.failed
				}
				msg, _ := json.Marshal(map[string]any{
					"type":        "suite_end",
					"suite":       suiteName,
					"passed":      passed,
					"failed":      failed,
					"duration_ms": int(evt.Elapsed * 1000),
				})
				h.broadcast(msg)
			}
		}
	}

	totalPassed, totalFailed := 0, 0
	for _, ss := range suiteStats {
		totalPassed += ss.passed
		totalFailed += ss.failed
	}

	err = cmd.Wait()
	durationMs := int(time.Since(startTime).Milliseconds())

	// 区分正常完成 vs 被中断
	if ctx.Err() != nil {
		msg, _ := json.Marshal(map[string]any{
			"type":      "stopped",
			"completed": totalPassed + totalFailed,
			"total":     totalPassed + totalFailed, // 已知完成数
		})
		h.broadcast(msg)
	} else {
		msg, _ := json.Marshal(map[string]any{
			"type":         "done",
			"total_passed": totalPassed,
			"total_failed": totalFailed,
			"duration_ms":  durationMs,
		})
		h.broadcast(msg)
	}
}

type suiteState struct {
	passed int
	failed int
}

func extractSuite(pkg string) string {
	// iclude/testing/store → store
	const prefix = "iclude/testing/"
	if strings.HasPrefix(pkg, prefix) {
		s := pkg[len(prefix):]
		// 去掉子包后缀 e.g. "store" from "store"
		if i := strings.Index(s, "/"); i >= 0 {
			s = s[:i]
		}
		return s
	}
	return pkg
}

func findProjectRoot() string {
	// 向上找 go.mod（使用 filepath.Dir 兼容 Windows）
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func main() {
	hub := newHub()
	port := "3001"

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			log.Printf("websocket accept error: %v", err)
			return
		}
		defer c.CloseNow()
		hub.addClient(c)
		defer hub.removeClient(c)

		for {
			var msg map[string]string
			err := wsjson.Read(r.Context(), c, &msg)
			if err != nil {
				break
			}
			switch msg["action"] {
			case "run":
				go hub.runTests(r.Context(), msg["suite"])
			case "stop":
				hub.mu.Lock()
				if hub.cancel != nil {
					hub.cancel()
				}
				hub.mu.Unlock()
			case "sync":
				hub.sendSnapshot(c)
			}
		}
	})

	log.Printf("Test Dashboard server running on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./cmd/test-dashboard/`
Expected: No errors

- [ ] **Step 4: Verify it starts**

Run: `go run ./cmd/test-dashboard/ &` then `curl -s http://localhost:3001/ || echo "server running (no root handler, expected)"`
Expected: Server starts without crash

- [ ] **Step 5: Commit**

```bash
git add cmd/test-dashboard/main.go go.mod go.sum
git commit -m "feat: add test dashboard WebSocket server"
```

---

## Task 3: Scaffold Vite + Vue 3 frontend project

**Files:**
- Create: `tools/test-dashboard-ui/` (entire directory)

- [x] **Step 1: Initialize Vite project**

```bash
mkdir -p tools/test-dashboard-ui
cd tools/test-dashboard-ui
npm create vite@latest . -- --template vue-ts
npm install
npm install -D tailwindcss @tailwindcss/vite pinia lucide-vue-next
```

- [x] **Step 1b: Create .gitignore**

Create `tools/test-dashboard-ui/.gitignore`:
```
node_modules/
dist/
```

- [x] **Step 2: Configure Tailwind**

Create `tools/test-dashboard-ui/src/style.css`:
```css
@import "tailwindcss";
```

Update `tools/test-dashboard-ui/vite.config.ts`:
```ts
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/ws': {
        target: 'ws://localhost:3001',
        ws: true,
      },
    },
  },
})
```

- [x] **Step 3: Create TypeScript event types**

Create `tools/test-dashboard-ui/src/types/events.ts`:
```ts
export interface SuiteStartEvent {
  type: 'suite_start'
  suite: string
  total?: number
  ts?: string
}

export interface CaseStartEvent {
  type: 'case_start'
  suite: string
  name: string
  ts?: string
}

export interface StepEvent {
  type: 'step' | 'input' | 'output'
  suite: string
  name: string
  seq?: number
  action?: string
  detail?: string
  status?: string
  label?: string
  value?: string
}

export interface CaseEndEvent {
  type: 'case_end'
  suite: string
  name: string
  status: 'pass' | 'fail' | 'skip'
  duration_ms: number
}

export interface SuiteEndEvent {
  type: 'suite_end'
  suite: string
  passed: number
  failed: number
  duration_ms: number
}

export interface DoneEvent {
  type: 'done'
  total_passed: number
  total_failed: number
}

export interface LogEvent {
  type: 'log'
  suite: string
  name: string
  text: string
}

export interface ErrorEvent {
  type: 'error'
  msg: string
}

export interface StoppedEvent {
  type: 'stopped'
  completed: number
  total: number
}

export interface SnapshotEvent {
  type: 'snapshot'
  running: boolean
  replayed: number
}

export type TestEvent =
  | SuiteStartEvent | CaseStartEvent | StepEvent
  | CaseEndEvent | SuiteEndEvent | DoneEvent
  | LogEvent | ErrorEvent | StoppedEvent | SnapshotEvent
```

- [x] **Step 4: Verify dev server starts**

Run: `cd tools/test-dashboard-ui && npm run dev`
Expected: Vite dev server on http://localhost:5173

- [x] **Step 5: Commit**

```bash
git add tools/test-dashboard-ui/
git commit -m "feat: scaffold test dashboard Vue 3 + Vite frontend"
```

---

## Task 4: Implement Pinia store + WebSocket composable

**Files:**
- Create: `tools/test-dashboard-ui/src/stores/testStore.ts`
- Create: `tools/test-dashboard-ui/src/composables/useTestSocket.ts`

- [x] **Step 1: Create Pinia store**

Create `tools/test-dashboard-ui/src/stores/testStore.ts`:
```ts
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { TestEvent, StepEvent } from '../types/events'

export interface TestCase {
  name: string
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
  cases: Map<string, TestCase>
  passed: number
  failed: number
  durationMs: number
}

export const useTestStore = defineStore('test', () => {
  const suites = ref<Map<string, TestSuite>>(new Map())
  const running = ref(false)
  const selectedCase = ref<string | null>(null)
  const currentCase = ref<string | null>(null)
  const totalPassed = ref(0)
  const totalFailed = ref(0)

  const allCases = computed(() => {
    const cases: TestCase[] = []
    suites.value.forEach(s => s.cases.forEach(c => cases.push(c)))
    return cases
  })

  const selectedCaseData = computed(() => {
    if (!selectedCase.value) return null
    for (const s of suites.value.values()) {
      const c = s.cases.get(selectedCase.value)
      if (c) return c
    }
    return null
  })

  function reset() {
    suites.value = new Map()
    totalPassed.value = 0
    totalFailed.value = 0
    selectedCase.value = null
    currentCase.value = null
  }

  function getOrCreateSuite(name: string): TestSuite {
    if (!suites.value.has(name)) {
      suites.value.set(name, {
        name, cases: new Map(), passed: 0, failed: 0, durationMs: 0,
      })
    }
    return suites.value.get(name)!
  }

  function getOrCreateCase(suite: string, name: string): TestCase {
    const s = getOrCreateSuite(suite)
    if (!s.cases.has(name)) {
      s.cases.set(name, {
        name, suite, status: 'pending', durationMs: 0,
        inputs: [], steps: [], outputs: [], logs: [],
      })
    }
    return s.cases.get(name)!
  }

  function handleEvent(evt: TestEvent) {
    switch (evt.type) {
      case 'suite_start':
        getOrCreateSuite(evt.suite)
        break
      case 'case_start':
        const tc = getOrCreateCase(evt.suite, evt.name)
        tc.status = 'running'
        currentCase.value = evt.name
        selectedCase.value = evt.name
        break
      case 'input':
        const ic = getOrCreateCase(evt.suite, evt.name)
        ic.inputs.push({ label: evt.label!, value: evt.value! })
        break
      case 'step':
        const sc = getOrCreateCase(evt.suite, evt.name)
        sc.steps.push({
          seq: evt.seq!, action: evt.action!, detail: evt.detail || '', status: evt.status || 'ok',
        })
        break
      case 'output':
        const oc = getOrCreateCase(evt.suite, evt.name)
        oc.outputs.push({ label: evt.label!, value: evt.value! })
        break
      case 'case_end':
        const ec = getOrCreateCase(evt.suite, evt.name)
        ec.status = evt.status
        ec.durationMs = evt.duration_ms
        break
      case 'suite_end':
        const se = getOrCreateSuite(evt.suite)
        se.passed = evt.passed
        se.failed = evt.failed
        se.durationMs = evt.duration_ms
        break
      case 'done':
        running.value = false
        totalPassed.value = evt.total_passed
        totalFailed.value = evt.total_failed
        currentCase.value = null
        break
      case 'log':
        const lc = getOrCreateCase(evt.suite, evt.name)
        lc.logs.push(evt.text)
        break
      case 'stopped':
        running.value = false
        currentCase.value = null
        break
      case 'snapshot':
        // 历史事件已在 snapshot 之前逐条回放处理完毕
        running.value = evt.running
        break
      case 'error':
        running.value = false
        console.error('Test error:', evt.msg)
        break
    }
  }

  return {
    suites, running, selectedCase, currentCase, totalPassed, totalFailed,
    allCases, selectedCaseData, reset, handleEvent,
  }
})
```

- [x] **Step 2: Create WebSocket composable**

Create `tools/test-dashboard-ui/src/composables/useTestSocket.ts`:
```ts
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
      } catch {}
    }

    ws.onclose = () => {
      connected.value = false
      reconnectTimer = window.setTimeout(connect, 2000)
    }
  }

  function runTests(suite?: string) {
    store.reset()
    store.running = true
    const msg: any = { action: 'run' }
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
```

- [x] **Step 3: Commit**

```bash
git add tools/test-dashboard-ui/src/stores/ tools/test-dashboard-ui/src/composables/
git commit -m "feat: add Pinia test store and WebSocket composable"
```

---

## Task 5: Build UI components — TopBar + Sidebar + Detail

**Files:**
- Create: `tools/test-dashboard-ui/src/components/TopBar.vue`
- Create: `tools/test-dashboard-ui/src/components/TestSidebar.vue`
- Create: `tools/test-dashboard-ui/src/components/TestDetail.vue`
- Create: `tools/test-dashboard-ui/src/components/ProgressBar.vue`
- Modify: `tools/test-dashboard-ui/src/App.vue`
- Modify: `tools/test-dashboard-ui/src/main.ts`

- [x] **Step 1: Create TopBar component**
- [x] **Step 2: Create TestSidebar component** (tree view with status icons, auto-scroll to current)
- [x] **Step 3: Create TestDetail panel** (inputs/steps/outputs JSON display)
- [x] **Step 4: Create ProgressBar component**
- [x] **Step 5: Wire up App.vue with three-panel layout + Pinia + socket**
- [x] **Step 6: Update main.ts to register Pinia**
- [x] **Step 7: Verify layout renders in browser**

Run: `cd tools/test-dashboard-ui && npm run dev` → open http://localhost:5173
Expected: Three-panel layout visible, Run button clickable

- [x] **Step 8: Commit**

```bash
git add tools/test-dashboard-ui/src/
git commit -m "feat: add dashboard UI components — TopBar, Sidebar, Detail, ProgressBar"
```

---

## Task 6: Build animated SVG flow graph

**Files:**
- Create: `tools/test-dashboard-ui/src/components/TestFlowGraph.vue`
- Create: `tools/test-dashboard-ui/src/components/FlowNode.vue`
- Create: `tools/test-dashboard-ui/src/components/FlowEdge.vue`

- [x] **Step 1: Create FlowNode.vue** — SVG rect with text, CSS class for state (pending/running/pass/fail), pulse animation on running
- [x] **Step 2: Create FlowEdge.vue** — SVG line with arrow marker, particle animation when active
- [x] **Step 3: Create TestFlowGraph.vue** — Renders `[Input] → [Step1] → [Step2] → ... → [Output]` from selectedCaseData, reactively updates node states
- [x] **Step 4: Add CSS animations** — `@keyframes pulse` for running state, particle flow on edges
- [x] **Step 5: Integrate into App.vue center panel**
- [x] **Step 6: Verify animation behavior**

Run: Start both servers, click Run Tests, watch flow graph animate
Expected: Nodes transition gray→blue(pulse)→green/red as test executes

- [x] **Step 7: Commit**

```bash
git add tools/test-dashboard-ui/src/components/TestFlowGraph.vue
git add tools/test-dashboard-ui/src/components/FlowNode.vue
git add tools/test-dashboard-ui/src/components/FlowEdge.vue
git commit -m "feat: add animated SVG flow graph for test visualization"
```

---

## Task 7: End-to-end integration test

**Files:** None new — verification only

- [ ] **Step 1: Start Go server**

Run: `go run ./cmd/test-dashboard/`
Expected: `Test Dashboard server running on :3001`

- [ ] **Step 2: Start Vite dev server**

Run: `cd tools/test-dashboard-ui && npm run dev`
Expected: `http://localhost:5173`

- [ ] **Step 3: Open browser and run tests**

Open http://localhost:5173, click [▶ Run All]

Verify:
1. Left sidebar populates with test suites (store, memory, search, api, report)
2. Tests execute and status icons update (running → pass/fail)
3. Clicking a test case shows its flow graph in center
4. Flow graph nodes animate through states
5. Right panel shows inputs, steps, outputs
6. Progress bar fills as tests complete
7. Final statistics shown (passed/failed/total)

- [ ] **Step 4: Commit any fixes**

```bash
git add cmd/test-dashboard/ tools/test-dashboard-ui/ pkg/testreport/
git commit -m "fix: integration polish for test dashboard"
```

---

## Verification Checklist

- [x] `go vet ./...` passes
- [x] `go build ./cmd/test-dashboard/` compiles
- [x] `go test ./testing/...` all pass (testreport changes are backwards compatible)
- [x] `cd tools/test-dashboard-ui && npm run build` produces dist/
- [ ] Browser shows real-time test execution with animated flow graphs
- [ ] Pass = green nodes, Fail = red nodes
- [ ] Input/Steps/Output visible in detail panel
