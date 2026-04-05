// Package main 测试仪表盘 WebSocket 服务 / Test Dashboard WebSocket server
// 接收 WS 连接，执行 go test -v -json 并将结构化事件推送至所有客户端
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
	"regexp"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// validSuite suite 名称白名单正则 / Whitelist regex for suite name
var validSuite = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// Hub 管理 WebSocket 客户端和测试运行状态 / Manages WebSocket clients and test run state
type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
	running bool
	cancel  context.CancelFunc
	history []json.RawMessage
}

// newHub 创建新的 Hub 实例 / Create a new Hub instance
func newHub() *Hub {
	return &Hub{clients: make(map[*websocket.Conn]bool)}
}

// addClient 注册 WebSocket 客户端 / Register a WebSocket client
func (h *Hub) addClient(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

// removeClient 移除 WebSocket 客户端 / Remove a WebSocket client
func (h *Hub) removeClient(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// broadcast 向所有客户端广播消息并记录到历史 / Broadcast message to all clients and record in history
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

// sendSnapshot 向重连客户端发送历史事件快照 / Send history snapshot to reconnecting client
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

// GoTestEvent go test -json 信封结构 / Envelope structure from go test -json output
type GoTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package,omitempty"`
	Test    string  `json:"Test,omitempty"`
	Output  string  `json:"Output,omitempty"`
	Elapsed float64 `json:"Elapsed,omitempty"`
}

// suiteState 跟踪每个测试套件的通过/失败计数 / Track pass/fail counts per test suite
type suiteState struct {
	passed int
	failed int
}

// runTests 执行 go test 并解析输出广播事件 / Execute go test and parse output to broadcast events
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

	// 校验 suite 名称，防止命令注入 / Validate suite name to prevent command injection
	if suite != "" && !validSuite.MatchString(suite) {
		msg, _ := json.Marshal(map[string]any{"type": "error", "msg": "invalid suite name: must be alphanumeric, underscore or hyphen"})
		h.broadcast(msg)
		return
	}

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
					// 用 Go 测试函数名覆盖 testreport 的 case name，确保与 case_start 匹配
					if evt.Test != "" {
						data["display_name"] = data["name"]
						data["name"] = evt.Test
					}
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

	_ = cmd.Wait()
	durationMs := int(time.Since(startTime).Milliseconds())

	// 区分正常完成 vs 被中断
	if ctx.Err() != nil {
		msg, _ := json.Marshal(map[string]any{
			"type":      "stopped",
			"completed": totalPassed + totalFailed,
			"total":     totalPassed + totalFailed,
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

// extractSuite 从包路径提取 suite 名 / Extract suite name from package path
// iclude/testing/store → store
func extractSuite(pkg string) string {
	const prefix = "iclude/testing/"
	if strings.HasPrefix(pkg, prefix) {
		s := pkg[len(prefix):]
		// 去掉子包后缀
		if i := strings.Index(s, "/"); i >= 0 {
			s = s[:i]
		}
		return s
	}
	return pkg
}

// findProjectRoot 向上查找 go.mod 所在目录 / Walk up to find directory containing go.mod
func findProjectRoot() string {
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

	// Playground REST API
	fixtureDir := filepath.Join(findProjectRoot(), "testing", "fixtures")
	env := NewTestEnv(fixtureDir)
	http.HandleFunc("/api/datasets", env.HandleListDatasets)
	http.HandleFunc("/api/datasets/load", env.HandleLoadDataset)
	http.HandleFunc("/api/datasets/status", env.HandleDatasetStatus)
	http.HandleFunc("/api/query", env.HandleQuery)
	http.HandleFunc("/api/cases/run", env.HandleRunCases)

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

	log.Printf("Test Dashboard server running on 127.0.0.1:%s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}
