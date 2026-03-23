# IClude Test Dashboard — Design Spec

## Context

IClude 项目有 96 个测试函数（17 个文件），用户需要一个实时可视化测试仪表盘：
- 展示每个测试用例的**输入、执行步骤、输出**
- 用**流程图动画**表示执行进度（节点逐步亮起，pass 绿 / fail 红）
- **实时监控**（WebSocket 推送，非回放）
- 独立文件夹开发，技术栈 Node.js + Vite

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  浏览器 (Vite + Vue3 + Custom SVG Flow + Tailwind)  │
│  ┌──────────┐  ┌──────────────┐  ┌───────────────┐  │
│  │ 测试列表  │  │  流程图动画   │  │ 输入/输出面板  │  │
│  │ (树形)    │  │  (SVG/CSS)   │  │ (详情)        │  │
│  └────┬─────┘  └──────┬───────┘  └──────┬────────┘  │
│       └───────────────┴─────────────────┘            │
│                     ↑ WebSocket :3001/ws              │
└─────────────────────┼───────────────────────────────┘
                      │
┌─────────────────────┼───────────────────────────────┐
│  Go Test Runner Server (:3001)                       │
│  1. 接收前端 "run" 指令                               │
│  2. 执行 go test -v -json ./testing/...              │
│  3. 两层解析 go test -json 信封 → Output 字段         │
│  4. 从 Output 中提取 ##TESTREPORT## 增强数据          │
│  5. WebSocket 实时推送事件                             │
└──────────────────────────────────────────────────────┘
```

### Stdout Buffering Mitigation

`go test -json` 会缓冲输出直到测试完成。使用 `-v` 标志强制逐行刷新（Go 1.21+ 支持）。
Server 解析采用两层模式：先解析 `go test -json` 信封 `{"Action":"output","Test":"...","Output":"..."}`,
再从 `Output` 字段中检查 `##TESTREPORT##` 前缀提取增强数据。

### Concurrency Model

- 同一时刻只允许一个测试运行（`run` 时如已在运行则返回 `{"type":"error","msg":"test already running"}`）
- 多个浏览器客户端可同时观察同一次运行（广播模式）

---

## WebSocket Event Protocol

### Client → Server

```json
{"action": "run"}              // 开始执行全部测试
{"action": "run", "suite": "store"}  // 执行指定套件
{"action": "stop"}             // 中断测试（server 发送 SIGTERM 给 go test 进程组）
{"action": "sync"}             // 重连后请求当前状态快照
```

### Server → Client

```json
// 错误事件（进程启动失败、崩溃等）
{"type": "error", "msg": "failed to start go test: exec not found"}

// 测试被中断
{"type": "stopped", "completed": 45, "total": 96}

// 重连同步快照（响应 sync 请求）
{"type": "snapshot", "suites": [...], "cases": [...], "running": true}

// 测试日志/失败详情（来自 t.Log / testify 断言失败）
{"type": "log", "suite": "store", "name": "TestDelete_NotFound", "text": "Expected nil, got: memory not found"}

// 测试套件开始
{"type": "suite_start", "suite": "store", "total": 53, "ts": "2026-03-19T10:00:00Z"}

// 单个用例开始
{"type": "case_start", "suite": "store", "name": "TestCreate_WithRetentionTier", "ts": "..."}

// 测试步骤（来自 testreport 增强）
{"type": "step", "suite": "store", "name": "TestCreate_WithRetentionTier",
 "input": {"content": "永久记忆内容", "retention_tier": "permanent"},
 "step_text": "创建 SQLite store 并写入记忆",
 "seq": 1}

// 单个用例结束
{"type": "case_end", "suite": "store", "name": "TestCreate_WithRetentionTier",
 "status": "pass", "duration_ms": 45,
 "output": {"retention_tier": "permanent", "strength": 1.0}}

// 测试套件结束
{"type": "suite_end", "suite": "store", "passed": 52, "failed": 1, "duration_ms": 3200}

// 全部完成
{"type": "done", "total_passed": 94, "total_failed": 2, "duration_ms": 8500}
```

---

## Frontend UI Design

### Three-Panel Layout

| 区域 | 宽度 | 内容 |
|------|------|------|
| 左侧 Sidebar | 250px | 测试套件树（可折叠），每个用例显示状态图标 |
| 中间 Main | flex-1 | 选中用例的流程图动画 + 底部全局进度条 |
| 右侧 Detail | 300px | 选中用例的输入/步骤列表/输出 JSON |

### Flow Graph Animation

每个测试用例渲染为水平流程图：

```
[输入] ──▶ [步骤1] ──▶ [步骤2] ──▶ ... ──▶ [输出]
```

**节点状态与颜色**：
- 灰色（#6b7280）= 未执行
- 蓝色脉冲（#3b82f6 + pulse animation）= 正在执行
- 绿色（#22c55e）= pass
- 红色（#ef4444）= fail

**动画行为**：
1. 收到 `case_start` → 渲染流程图，所有节点灰色
2. 收到 `step` → 对应节点变蓝色脉冲，连接线显示粒子流动
3. 收到下一个 `step` → 前一个节点变绿，当前节点变蓝
4. 收到 `case_end(pass)` → 所有节点变绿
5. 收到 `case_end(fail)` → 失败节点变红，后续节点保持灰色

**自动跟随**：测试运行时，左侧列表自动滚动到当前用例，流程图自动切换。

### Top Bar

- 项目名：IClude Test Dashboard
- 运行按钮：[▶ Run All] [▶ Run Suite ▼]
- 统计：94 passed / 2 failed / 96 total
- 耗时：8.5s

---

## Go Side Changes

### New: `cmd/test-dashboard/main.go` (~200 lines)

- HTTP server on `:3001`
- WebSocket endpoint `/ws` (nhooyr.io/websocket — 活跃维护，支持 context)
- 执行 `go test -v -json ./testing/...` 子进程
- 两层解析：先解析 `go test -json` 信封（`Action`/`Test`/`Output` 字段），再从 `Output` 提取 `##TESTREPORT##` 前缀
- Suite 名映射：`iclude/testing/store` → `store`（去掉 `iclude/testing/` 前缀）
- 广播模式：多客户端观察同一次运行
- 同一时刻仅允许一个测试运行
- `stop` 动作发送 SIGTERM 给进程组，推送 `stopped` 事件
- 支持 CORS（允许 Vite dev server :5173）
- 使用项目根 go.mod（nhooyr.io/websocket 加到现有依赖）

### Modified: `pkg/testreport/reporter.go` (~30 lines)

新增 JSON stdout 模式：

- 检测环境变量 `TESTREPORT_JSON=1`
- `tc.Input()` / `tc.Step()` / `tc.Output()` 向 stdout 打印：
  ```
  ##TESTREPORT##{"type":"step","name":"TestXxx","input":{...},"seq":1}
  ```
- 不影响现有 HTML 报告功能

---

## Directory Structure

```
cmd/test-dashboard/
└── main.go                      # Go WebSocket + test runner（项目根 go.mod）

tools/test-dashboard-ui/         # 独立前端项目
├── package.json
├── vite.config.ts
├── tsconfig.json
├── tailwind.config.js
├── index.html
├── src/
│   ├── main.ts
│   ├── App.vue
│   ├── composables/
│   │   └── useTestSocket.ts     # WebSocket 连接管理 + 自动重连 + sync
│   ├── components/
│   │   ├── TopBar.vue           # 顶栏：标题 + 运行按钮 + 统计
│   │   ├── TestSidebar.vue      # 左侧：测试套件树
│   │   ├── TestFlowGraph.vue    # 中间：自定义 SVG 流程图
│   │   ├── FlowNode.vue         # SVG 节点（带 CSS 状态动画）
│   │   ├── FlowEdge.vue         # SVG 连线（粒子动画）
│   │   ├── TestDetail.vue       # 右侧：输入/步骤/输出面板
│   │   └── ProgressBar.vue      # 底部全局进度条
│   ├── stores/
│   │   └── testStore.ts         # Pinia：测试状态管理
│   └── types/
│       └── events.ts            # WebSocket 事件 TypeScript 类型
└── public/
    └── favicon.svg
```

**Go server 放在 `cmd/test-dashboard/`**：与现有 `cmd/server/` 一致，可通过 `go run ./cmd/test-dashboard/` 启动。
**前端放在 `tools/test-dashboard-ui/`**：Node.js 代码不适合 Go 目录规范，独立放置。

---

## Tech Stack

| Component | Choice | Reason |
|-----------|--------|--------|
| Frontend framework | Vue 3 + Composition API | 轻量、响应式 |
| Build tool | Vite | 快速 HMR |
| Flow graph | Custom SVG + CSS animations | 线性流程图不需要 Vue Flow（~150KB），自定义 SVG 更轻量可控 |
| Styling | Tailwind CSS | 快速原型 |
| State management | Pinia | Vue 3 标准 |
| Icons | Lucide Vue | 轻量一致 |
| WebSocket (Go) | nhooyr.io/websocket | 活跃维护，支持 context，gorilla 已停维 |
| JSON parsing | go test -json | Go 原生支持 |

---

## Startup Commands

```bash
# Terminal 1: Go test runner server（从项目根目录）
go run ./cmd/test-dashboard/
# → Server running on :3001

# Terminal 2: Vite dev server
cd tools/test-dashboard-ui
npm install
npm run dev
# → http://localhost:5173

# 打开浏览器 → http://localhost:5173 → 点 [▶ Run Tests]
```

---

## Success Criteria

1. 点击 Run Tests 后，浏览器实时显示测试执行进度
2. 每个测试用例有流程图，节点随执行逐步变色
3. 能看到每个用例的输入数据、执行步骤、输出结果
4. Pass 绿色 / Fail 红色，一目了然
5. 左侧树自动跟随当前执行的用例
6. 全部完成后显示总统计（passed/failed/duration）
