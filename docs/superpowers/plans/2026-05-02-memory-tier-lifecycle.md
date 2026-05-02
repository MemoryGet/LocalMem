# Memory Tier Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让记忆层级系统真正运转：写入时按 MemoryClass 自动分配 RetentionTier，访问强化触发晋升，ephemeral 到期自动清理。

**Architecture:** 三处改动串联成完整生命周期：① `lifecycle.go` 新增写入时 tier 推断函数；② `manager.go` 写入路径调用该函数；③ `promoter.go` class 晋升时同步 tier；④ heartbeat 新增 `tier_promotion.go` 和 `expiry_cleanup.go` 两个后台任务并注册到 `engine.go`。

**Tech Stack:** Go 1.25, SQLite, testify/assert, 现有 `store.MemoryStore` 接口（无需新增接口方法）

---

### Task 1: 新增 `ResolveTierFromClass` 和 `minTierForClass`

**Files:**
- Modify: `internal/memory/lifecycle.go`
- Test: `testing/memory/tier_lifecycle_test.go` (新建)

- [ ] **Step 1: 写失败测试**

新建 `testing/memory/tier_lifecycle_test.go`：

```go
package memory_test

import (
	"testing"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestResolveTierFromClass(t *testing.T) {
	tests := []struct {
		name      string
		class     string
		kind      string
		explicitTier string
		wantTier  string
	}{
		{"explicit tier not overridden", "episodic", "", "long_term", "long_term"},
		{"conversation kind → ephemeral", "episodic", "conversation", "", "ephemeral"},
		{"episodic class → short_term", "episodic", "", "", "short_term"},
		{"semantic class → standard", "semantic", "", "", "standard"},
		{"procedural class → long_term", "procedural", "", "", "long_term"},
		{"core class → permanent", "core", "", "", "permanent"},
		{"empty class → standard", "", "", "", "standard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{
				MemoryClass:   tt.class,
				Kind:          tt.kind,
				RetentionTier: tt.explicitTier,
			}
			memory.ResolveTierFromClass(mem)
			assert.Equal(t, tt.wantTier, mem.RetentionTier)
		})
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /root/LocalMem && go test ./testing/memory/ -run TestResolveTierFromClass -v
```

期望：`FAIL — memory.ResolveTierFromClass undefined`

- [ ] **Step 3: 在 `lifecycle.go` 末尾添加两个函数**

打开 `internal/memory/lifecycle.go`，在文件末尾（`PromoteTier` 函数之后）追加：

```go
// minTierForClass returns the minimum RetentionTier for a given MemoryClass.
// Used by ResolveTierFromClass and Promoter tier sync.
func minTierForClass(class string) string {
	switch class {
	case "episodic":
		return model.TierShortTerm
	case "semantic":
		return model.TierStandard
	case "procedural":
		return model.TierLongTerm
	case "core":
		return model.TierPermanent
	default:
		return model.TierStandard
	}
}

// tierIndex returns the rank of a tier (higher index = more permanent).
// Used to compare tier levels without string comparison.
func tierIndex(tier string) int {
	for i, t := range tierOrder {
		if t == tier {
			return i
		}
	}
	return 2 // default: standard rank
}

// ResolveTierFromClass auto-assigns RetentionTier from MemoryClass and Kind.
// Must be called before ResolveTierDefaults in the write path.
// If RetentionTier is already set, it is respected and not overridden.
func ResolveTierFromClass(mem *model.Memory) {
	if mem.RetentionTier != "" {
		return
	}
	if mem.Kind == "conversation" {
		mem.RetentionTier = model.TierEphemeral
		return
	}
	mem.RetentionTier = minTierForClass(mem.MemoryClass)
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /root/LocalMem && go test ./testing/memory/ -run TestResolveTierFromClass -v
```

期望：`PASS — 7/7`

- [ ] **Step 5: 提交**

```bash
git add internal/memory/lifecycle.go testing/memory/tier_lifecycle_test.go
git commit -m "feat(memory): add ResolveTierFromClass and minTierForClass to lifecycle"
```

---

### Task 2: 将 `ResolveTierFromClass` 接入写入路径

**Files:**
- Modify: `internal/memory/manager.go` (line 157 附近)

- [ ] **Step 1: 写集成测试**

在 `testing/memory/tier_lifecycle_test.go` 末尾追加：

```go
func TestResolveTierFromClass_DecayRateSync(t *testing.T) {
	// 验证写入后 decay_rate 与 tier 同步
	tests := []struct {
		name          string
		class         string
		wantTier      string
		wantDecayRate float64
	}{
		{"episodic gets short_term decay", "episodic", "short_term", 0.05},
		{"semantic gets standard decay", "semantic", "standard", 0.01},
		{"procedural gets long_term decay", "procedural", "long_term", 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{MemoryClass: tt.class}
			memory.ResolveTierFromClass(mem)
			memory.ResolveTierDefaults(mem)
			assert.Equal(t, tt.wantTier, mem.RetentionTier)
			assert.Equal(t, tt.wantDecayRate, mem.DecayRate)
		})
	}
}
```

- [ ] **Step 2: 运行测试，确认通过**

```bash
cd /root/LocalMem && go test ./testing/memory/ -run TestResolveTierFromClass_DecayRateSync -v
```

期望：`PASS`（已有函数，测试直接通过）

- [ ] **Step 3: 在写入路径中调用 `ResolveTierFromClass`**

打开 `internal/memory/manager.go`，找到第 156-157 行：

```go
	// 应用等级默认值
	ResolveTierDefaults(mem)
```

改为：

```go
	// 先从 class 推断 tier，再填充衰减参数
	ResolveTierFromClass(mem)
	ResolveTierDefaults(mem)
```

- [ ] **Step 4: 编译验证**

```bash
cd /root/LocalMem && go build ./...
```

期望：无错误

- [ ] **Step 5: 运行全量记忆测试**

```bash
cd /root/LocalMem && go test ./testing/memory/... -v -count=1 2>&1 | tail -20
```

期望：所有已有测试仍通过

- [ ] **Step 6: 提交**

```bash
git add internal/memory/manager.go testing/memory/tier_lifecycle_test.go
git commit -m "feat(memory): wire ResolveTierFromClass into Create write path"
```

---

### Task 3: Promoter class 晋升时同步 Tier

**Files:**
- Modify: `internal/memory/promoter.go`
- Test: `testing/memory/tier_lifecycle_test.go`

- [ ] **Step 1: 写失败测试**

在 `testing/memory/tier_lifecycle_test.go` 末尾追加：

```go
func TestTierIndex_Order(t *testing.T) {
	// 验证 tier 排序关系
	assert.Less(t, memory.TierIndex("ephemeral"), memory.TierIndex("short_term"))
	assert.Less(t, memory.TierIndex("short_term"), memory.TierIndex("standard"))
	assert.Less(t, memory.TierIndex("standard"), memory.TierIndex("long_term"))
	assert.Less(t, memory.TierIndex("long_term"), memory.TierIndex("permanent"))
	assert.Equal(t, 2, memory.TierIndex("unknown")) // 未知 tier → standard rank
}
```

- [ ] **Step 2: 导出 `TierIndex`（测试需要访问）**

在 `internal/memory/lifecycle.go` 中，将 `tierIndex` 改名为 `TierIndex`（大写导出）：

```go
// TierIndex returns the rank of a tier (higher index = more permanent).
func TierIndex(tier string) int {
	for i, t := range tierOrder {
		if t == tier {
			return i
		}
	}
	return 2
}
```

同时将 `promoter.go` 中的调用（Task 3 Step 4 会用到）直接用 `TierIndex`。

- [ ] **Step 3: 运行测试，确认失败**

```bash
cd /root/LocalMem && go test ./testing/memory/ -run TestTierIndex_Order -v
```

期望：`FAIL — memory.TierIndex undefined`

- [ ] **Step 4: 实现，运行测试确认通过**

`TierIndex` 已在 Step 2 中添加。运行：

```bash
cd /root/LocalMem && go test ./testing/memory/ -run TestTierIndex_Order -v
```

期望：`PASS`

- [ ] **Step 5: 在 `Promoter.Run()` 中添加 tier 联动**

打开 `internal/memory/promoter.go`，找到 `Run()` 方法中设置 class 的两行：

```go
		mem.MemoryClass = targetClass
		mem.CandidateFor = ""
```

在这两行之后、`memStore.Update` 之前，添加：

```go
		// 同步 tier：若当前 tier 低于新 class 的最低要求，升级 tier
		if minT := minTierForClass(targetClass); TierIndex(mem.RetentionTier) < TierIndex(minT) {
			mem.RetentionTier = minT
			dr, _ := model.DefaultDecayParams(minT)
			mem.DecayRate = dr
		}
```

- [ ] **Step 6: 在 `Promoter.PromoteByID()` 中添加相同的 tier 联动**

找到 `PromoteByID()` 方法中设置 class 的两行：

```go
	mem.MemoryClass = targetClass
	mem.CandidateFor = ""
```

在这两行之后、`memStore.Update` 之前，添加相同的联动代码：

```go
	// 同步 tier：若当前 tier 低于新 class 的最低要求，升级 tier
	if minT := minTierForClass(targetClass); TierIndex(mem.RetentionTier) < TierIndex(minT) {
		mem.RetentionTier = minT
		dr, _ := model.DefaultDecayParams(minT)
		mem.DecayRate = dr
	}
```

- [ ] **Step 7: 编译并运行全量测试**

```bash
cd /root/LocalMem && go build ./... && go test ./testing/memory/... -count=1 2>&1 | tail -10
```

期望：编译通过，所有测试通过

- [ ] **Step 8: 提交**

```bash
git add internal/memory/lifecycle.go internal/memory/promoter.go testing/memory/tier_lifecycle_test.go
git commit -m "feat(memory): sync RetentionTier when Promoter elevates MemoryClass"
```

---

### Task 4: Heartbeat - TierPromotionAuditor

**Files:**
- Create: `internal/heartbeat/tier_promotion.go`

- [ ] **Step 1: 新建 `tier_promotion.go`**

新建 `internal/heartbeat/tier_promotion.go`，内容：

```go
package heartbeat

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// runTierPromotion 按强化次数+存活时间晋升 short_term→standard 和 standard→long_term
func (e *Engine) runTierPromotion(ctx context.Context) error {
	const batchLimit = 100

	candidates, err := e.memStore.List(ctx, nil, 0, batchLimit)
	if err != nil {
		return fmt.Errorf("list tier promotion candidates: %w", err)
	}

	crystalCfg := config.GetConfig().Crystallization
	promoted := 0
	now := time.Now()

	for _, mem := range candidates {
		if ctx.Err() != nil {
			break
		}

		oldTier := mem.RetentionTier
		var newTier string

		switch mem.RetentionTier {
		case model.TierShortTerm:
			if mem.Kind == "conversation" {
				continue
			}
			if mem.ReinforcedCount >= 2 && now.Sub(mem.CreatedAt) >= 24*time.Hour {
				newTier = model.TierStandard
			}
		case model.TierStandard:
			if memory.ShouldCrystallize(mem, crystalCfg) {
				newTier = model.TierLongTerm
			}
		}

		if newTier == "" {
			continue
		}

		mem.RetentionTier = newTier
		dr, _ := model.DefaultDecayParams(newTier)
		mem.DecayRate = dr

		if err := e.memStore.Update(ctx, mem); err != nil {
			logger.Warn("heartbeat: tier promotion update failed",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			continue
		}

		promoted++
		logger.Info("heartbeat: tier promoted",
			zap.String("memory_id", mem.ID),
			zap.String("from", oldTier),
			zap.String("to", newTier),
		)
	}

	if promoted > 0 {
		logger.Info("heartbeat: tier promotion round completed", zap.Int("promoted", promoted))
	}
	return nil
}
```

- [ ] **Step 2: 编译验证**

```bash
cd /root/LocalMem && go build ./internal/heartbeat/...
```

期望：无错误

- [ ] **Step 3: 提交**

```bash
git add internal/heartbeat/tier_promotion.go
git commit -m "feat(heartbeat): add runTierPromotion — short_term→standard, standard→long_term"
```

---

### Task 5: Heartbeat - ExpiryCleanup

**Files:**
- Create: `internal/heartbeat/expiry_cleanup.go`

- [ ] **Step 1: 新建 `expiry_cleanup.go`**

新建 `internal/heartbeat/expiry_cleanup.go`，内容：

```go
package heartbeat

import (
	"context"
	"fmt"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// runExpiryCleanup 软删除 expires_at 已到期的记忆（主要是 ephemeral tier）
func (e *Engine) runExpiryCleanup(ctx context.Context) error {
	deleted, err := e.memStore.CleanupExpired(ctx)
	if err != nil {
		return fmt.Errorf("cleanup expired memories: %w", err)
	}
	if deleted > 0 {
		logger.Info("heartbeat: expired memories soft-deleted", zap.Int("count", deleted))
	}
	return nil
}
```

- [ ] **Step 2: 编译验证**

```bash
cd /root/LocalMem && go build ./internal/heartbeat/...
```

期望：无错误

- [ ] **Step 3: 提交**

```bash
git add internal/heartbeat/expiry_cleanup.go
git commit -m "feat(heartbeat): add runExpiryCleanup — soft-delete expired ephemeral memories"
```

---

### Task 6: 将两个新任务注册到 Heartbeat Engine

**Files:**
- Modify: `internal/heartbeat/engine.go`

- [ ] **Step 1: 在 `Run()` 中注册新任务**

打开 `internal/heartbeat/engine.go`，找到步骤 7（`runCandidatePromotion`）之后、`logger.Info("heartbeat: inspection round completed")` 之前，添加：

```go
	// 8. Tier 晋升 / Tier promotion: short_term→standard, standard→long_term
	if err := e.runTierPromotion(ctx); err != nil {
		logger.Warn("heartbeat: tier promotion failed", zap.Error(err))
	}

	// 9. 过期清理 / Expiry cleanup: soft-delete ephemeral past expires_at
	if err := e.runExpiryCleanup(ctx); err != nil {
		logger.Warn("heartbeat: expiry cleanup failed", zap.Error(err))
	}
```

- [ ] **Step 2: 编译验证**

```bash
cd /root/LocalMem && go build ./...
```

期望：无错误

- [ ] **Step 3: 运行全量测试**

```bash
cd /root/LocalMem && go test ./testing/... -count=1 2>&1 | tail -20
```

期望：所有测试通过

- [ ] **Step 4: 提交**

```bash
git add internal/heartbeat/engine.go
git commit -m "feat(heartbeat): register runTierPromotion and runExpiryCleanup in Engine.Run"
```

---

## 自检清单

**Spec 覆盖：**
- [x] 写入时自动分配 tier（Task 1+2）
- [x] MemoryClass Promoter 联动 tier（Task 3）
- [x] short_term → standard 晋升（Task 4）
- [x] standard → long_term 晶化（Task 4）
- [x] ephemeral 过期软删除（Task 5）
- [x] heartbeat 注册（Task 6）

**类型一致性：**
- `ResolveTierFromClass` 在 Task 1 定义，Task 2 调用 ✓
- `TierIndex` 在 Task 3 导出，Task 3 Step 5/6 调用 ✓
- `minTierForClass` 在 Task 1 定义，Task 3 调用 ✓（同包，可用）
- `runTierPromotion` 在 Task 4 定义，Task 6 调用 ✓
- `runExpiryCleanup` 在 Task 5 定义，Task 6 调用 ✓
