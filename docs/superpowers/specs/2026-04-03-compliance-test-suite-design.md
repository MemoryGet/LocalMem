# LocalMem Compliance Test Suite Design

**Date**: 2026-04-03  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. Context

LocalMem 已新增两份上层设计：

- [Unified AI Tool Integration Protocol v1](./2026-04-03-unified-ai-tool-integration-protocol-design.md)
- [Adapter Runtime Design](./2026-04-03-adapter-runtime-design.md)

前两份文档定义了：

- 如何统一接入 Codex / Claude Code / Cursor / Cline
- 如何通过 runtime 执行 session lifecycle、capture、finalize、repair

当前还缺一份“如何判断实现是否正确”的文档。  
本设计定义 **Compliance Test Suite**，用于验证：

1. 协议是否被正确实现
2. 不同 adapter profile 是否达到目标等级
3. 幂等、隔离、修复是否成立
4. 新接入不会破坏既有行为

---

## 2. Goals

- 为 LocalMem adapter/runtime 定义统一合规测试集
- 定义 L1-L4 compliance level 的验收标准
- 将协议要求映射成可执行测试用例
- 为 Codex / Claude Code / Cursor / Cline 定义最小通过线
- 为后续官方支持列表提供可量化依据

## 3. Non-Goals

- 不要求首版自动化覆盖所有真实宿主 UI 行为
- 不模拟每个第三方工具的完整产品逻辑
- 不替代业务检索质量评测
- 不定义性能基准的最终 SLA

---

## 4. Test Suite Scope

Compliance Test Suite 只验证“接入协议是否成立”，不验证所有记忆质量问题。

首版覆盖 6 个维度：

1. bootstrap
2. retrieval
3. capture
4. finalize
5. repair
6. isolation

---

## 5. Compliance Levels

### 5.1 L1: MCP Reachable

要求：

- 宿主或 adapter 能成功连接 LocalMem
- 核心工具可被调用

通过标准：

- `iclude_scan` 成功
- `iclude_fetch` 成功
- `iclude_retain` 成功

### 5.2 L2: Guided Memory

要求：

- 会话开始时能高概率触发 scan
- 有规则或指令层约束记忆使用

通过标准：

- bootstrap 期间发生至少一次 scan
- 注入内容为摘要层
- 重要用户事实可被 retain

### 5.3 L3: Lifecycle-Aware

要求：

- 具备 session create/start/capture 基础闭环

通过标准：

- `iclude_create_session` 成功
- capture 至少有一种自动化来源
- metadata 中有正确 `session_id` / `tool_name`

### 5.4 L4: Fully Managed Memory

要求：

- 具备 start/capture/stop/repair 全链路

通过标准：

- `finalize_session` 或等价 finalize 逻辑成功
- stop 丢失时 repair 可补齐
- finalize 与 ingest 幂等

---

## 6. Profile Target Matrix

### 6.0 Reading Guide

本章中的矩阵描述的是：

- **推荐 profile**
- **目标合规等级**
- **最终应达到的测试覆盖**

它不是“当前仓库已经具备的测试覆盖现状”。

因此阅读方式应区分：

1. **target level**
   表示长期目标
2. **current test priority**
   表示当前两周内应优先实现的测试范围

当前两周内的实际优先级，以本文件后文 `Recommended Two-Week Testing Plan` 为准。

| Tool | Recommended Profile | Minimum Level | Target Level |
|------|---------------------|---------------|--------------|
| Codex | B | L2 | L3 |
| Claude Code | A | L3 | L4 |
| Cursor | B/D | L2 | L3/L4 |
| Cline | C | L3 | L4 |

解释：

- `Codex` 首版可先验证 guided memory + partial lifecycle
- `Claude Code` 应作为完整标杆
- `Cursor` 取决于可用扩展能力
- `Cline` 通过 wrapper 目标应是完整闭环

---

## 7. Test Architecture

```text
compliance test runner
   |
   |- mock host profile
   |- adapter/runtime harness
   |- LocalMem test backend
   |- transcript fixtures
   |- assertions
```

测试分三层：

1. **Protocol unit tests**
   验证请求/响应、字段、状态机、幂等键
2. **Runtime integration tests**
   验证 launcher、registry、retry、repair、finalize
3. **Profile conformance tests**
   验证 Codex / Claude / Cursor / Cline profile 是否达标

---

## 8. Canonical Test Fixtures

### 8.1 Identity Fixture

固定测试身份：

```json
{
  "user_id": "u_test_001",
  "tool_name": "claude-code",
  "project_id": "p_test_repo",
  "session_id": "s_test_001"
}
```

### 8.2 Transcript Fixture

提供三类 transcript fixture：

- `short_session.jsonl`
- `tool_heavy_session.jsonl`
- `crash_before_finalize.jsonl`

### 8.3 Memory Fixture

预置记忆：

- `user/* preferences`
- `project/* current_status`
- `session/* recent_turns`

用于验证 scope priority 和串味问题。

---

## 9. Required Test Categories

### 9.1 Bootstrap Tests

验证内容：

- 是否创建 session
- 是否执行 scan
- 是否生成 memory prelude
- create 失败后是否可降级

必测用例：

1. `bootstrap_success_creates_context`
2. `bootstrap_scan_returns_summary_only`
3. `bootstrap_timeout_degrades_without_blocking_host`
4. `bootstrap_records_registry_state`

### 9.2 Retrieval Tests

验证内容：

- scan -> fetch 工作流
- scope priority
- 不默认走 recall 全量

必测用例：

1. `scan_then_fetch_selected_memory`
2. `session_scope_has_priority_over_user_scope`
3. `project_scope_does_not_leak_to_other_project`
4. `summary_injection_is_bounded`

### 9.3 Capture Tests

验证内容：

- hook / bridge / transcript 至少一种采集通路成立
- retain metadata 正确
- 噪音过滤生效

必测用例：

1. `capture_tool_event_retains_observation`
2. `capture_user_fact_retains_preference`
3. `capture_skips_low_value_noise`
4. `capture_sets_required_metadata_fields`

### 9.4 Finalize Tests

验证内容：

- finalize 能成功关闭会话
- ingest 和 finalize 的关系正确
- 重复 finalize 无副作用

必测用例：

1. `finalize_ingests_conversation_and_marks_closed`
2. `finalize_is_idempotent`
3. `finalize_does_not_duplicate_summary`
4. `finalize_after_partial_ingest_completes_marker_only`

### 9.5 Repair Tests

验证内容：

- stop 丢失后能否补 finalize
- retry 与 repair 分工是否正确

必测用例：

1. `repair_recovers_pending_finalize_session`
2. `repair_replays_transcript_from_cursor`
3. `repair_marks_abandoned_after_threshold`
4. `retryable_errors_do_not_skip_finalize`

### 9.6 Isolation Tests

验证内容：

- 用户共享与项目隔离是否符合协议
- agent/session 私有数据不串味

必测用例：

1. `user_scope_is_shared_across_tools`
2. `project_scope_is_isolated`
3. `session_scope_is_not_visible_to_other_session`
4. `agent_scope_is_private`

---

## 10. Mandatory Assertions

所有合规测试至少要断言以下字段：

| Assertion | Description |
|----------|-------------|
| `session_id` | 会话归属正确 |
| `tool_name` | 宿主归属正确 |
| `project_id` | 项目归属正确 |
| `context_id` | 已建立 LocalMem context 绑定 |
| `scope` | scope 正确 |
| `capture_mode` | 采集模式正确 |
| `idempotency_key` | 幂等键存在 |
| `state_transition` | 状态迁移合法 |

---

## 11. Idempotency Test Rules

### 11.1 Retain Idempotency

测试方式：

- 同一 observation 连续提交两次
- 第二次不得产生重复高价值记忆

必测：

1. `retain_same_event_twice_is_deduped`
2. `retain_different_event_hash_creates_new_record`

### 11.2 Ingest Idempotency

测试方式：

- 相同 transcript 用同一 `idempotency_key` 提交两次
- 只允许一次有效导入

必测：

1. `ingest_same_key_is_idempotent`
2. `ingest_new_version_after_transcript_growth_is_allowed`

### 11.3 Finalize Idempotency

测试方式：

- finalize 连续调用多次
- 不得重复生成 summary / final marker

必测：

1. `finalize_same_key_is_idempotent`
2. `finalize_after_already_closed_returns_success`

---

## 12. Failure Injection Matrix

首版测试必须支持注入以下故障：

| Failure | Expected Result |
|--------|-----------------|
| MCP connect timeout | bootstrap 降级但宿主继续 |
| `create_session` 失败 | 可重试，session 不丢 |
| `scan` 失败 | 不阻塞 active |
| `retain` 429 | 进入 retry queue |
| `ingest` 5xx | 进入 retry/repair |
| `finalize` 中断 | 标记 pending_repair |
| process crash | registry + transcript 支持恢复 |

---

## 13. Profile Conformance Cases

### 13.1 Codex

最低要求：

- 通过 L2 全套
- 通过 L3 中的 `create_session` 和至少一种 capture

关键用例：

1. `codex_bootstrap_uses_instruction_guided_scan`
2. `codex_retain_fact_via_mcp`
3. `codex_wrapper_can_finalize_session`

current priority note：

- 当前仓库还没有完整 codex wrapper
- 因此第 1 批应优先实现前两类用例
- 第 3 条属于后续扩展目标

### 13.2 Claude Code

最低要求：

- 通过 L4

关键用例：

1. `claude_session_start_hook_bootstraps_memory`
2. `claude_post_tool_use_auto_retains`
3. `claude_stop_hook_finalizes_session`
4. `claude_missing_stop_hook_is_repaired`

current priority note：

- `Claude Code` 是当前最适合作为首批 conformance 标杆的宿主
- 本节 4 条用例都应进入前两周测试规划

### 13.3 Cursor

最低要求：

- 通过 L2
- 若 bridge 存在则验证 L3/L4

关键用例：

1. `cursor_mcp_scan_is_available`
2. `cursor_instruction_injection_guides_memory_usage`
3. `cursor_bridge_finalizes_if_api_available`

current priority note：

- 当前仓库尚无稳定 Cursor bridge 实现
- 因此前两周只建议保留前两类轻量用例占位
- 第 3 条属于后续 bridge 落地后的目标测试

### 13.4 Cline

最低要求：

- 通过 L3

关键用例：

1. `cline_launcher_bootstraps_session`
2. `cline_transcript_capture_generates_turns`
3. `cline_wrapper_finalize_is_idempotent`
4. `cline_crash_repair_replays_pending_session`

current priority note：

- 当前仓库尚无完整 launcher / wrapper
- 因此前两周不建议实现本节完整 conformance
- 本节应视为后续 profile C 落地后的目标测试

---

## 14. Suggested Test Directory Layout

建议新增目录：

```text
testing/compliance/
  protocol/
  runtime/
  profiles/
  fixtures/
```

说明：

- `protocol/`：字段、契约、幂等、状态机测试
- `runtime/`：launcher、registry、retry、repair、finalize
- `profiles/`：Codex / Claude / Cursor / Cline 合规测试
- `fixtures/`：transcript、host event、mock config

---

## 15. Test Harness Requirements

Harness 需要能模拟：

1. mock MCP backend
2. fake host events
3. transcript append
4. process exit / crash
5. delayed responses
6. duplicate delivery

建议能力：

- 可冻结时间
- 可注入随机 session id
- 可统计 API 调用次数

---

## 16. Pass/Fail Policy

### 16.1 Per-Level Policy

- L1: 所有核心工具连通测试必须通过
- L2: bootstrap + bounded injection + retain 核心用例必须通过
- L3: create/capture/state binding 必须通过
- L4: finalize + repair + idempotency 必须通过

### 16.2 Official Support Policy

工具被标记为 “Officially Supported” 的最低标准：

1. 至少连续两轮测试通过
2. 无 blocker 级失败
3. profile 目标等级达标
4. 幂等与隔离测试全部通过

### 16.3 Experimental Support Policy

若只达到：

- L1 或 L2

则应标记为：

- `experimental`

不得宣传为完整记忆集成。

---

## 17. Metrics and Reporting

每次 compliance run 建议产出：

| Metric | Meaning |
|-------|---------|
| `total_cases` | 总用例数 |
| `passed_cases` | 通过数 |
| `failed_cases` | 失败数 |
| `level_achieved` | 达到的合规等级 |
| `idempotency_pass_rate` | 幂等通过率 |
| `repair_pass_rate` | 修复通过率 |
| `isolation_pass_rate` | 隔离通过率 |

建议生成报告：

```text
docs/evaluations/compliance_<tool>_<date>.md
```

---

## 18. Initial Implementation Plan

### Phase 1

- 建立 `testing/compliance/fixtures`
- 建立 protocol unit tests
- 建立 idempotency tests

### Phase 2

- 建立 runtime harness
- 建立 finalize / repair tests

### Phase 3

- 建立 Claude Code profile conformance
- 建立 Codex profile conformance

### Phase 4

- 建立 Cursor / Cline profile conformance
- 输出 official support matrix

---

## 19. Recommended Two-Week Testing Plan

本节根据当前仓库状态，将 compliance 测试工作压缩成适合紧随 runtime 实现推进的两周计划。

当前前提：

- 数据库 migration 与 store 层已补齐 `sessions / session_finalize_state / transcript_cursors / idempotency_keys`
- 但 runtime 主链路仍在接线中
- 因此 compliance 测试不能一开始就全面铺开，而应跟着 runtime 落地节奏增量补

### Week 1: 先不追求全套 compliance，聚焦测试地基

Week 1 的主目标不是完成 profile conformance，而是先为 runtime 接线准备测试基础设施。

#### 应做

1. 建立 `testing/compliance/fixtures`
2. 建立最小 transcript fixture
3. 建立最小 identity fixture
4. 建立 mock host / mock MCP backend harness 雏形
5. 确定 `finalize` / `idempotency` / `repair` 的断言格式

#### 暂不做

1. 不做完整 `Cursor` profile conformance
2. 不做完整 `Cline` profile conformance
3. 不做大规模兼容矩阵
4. 不做官方支持列表输出

#### Week 1 测试侧验收标准

- 可以开始为 `FinalizeService` 和 `session-stop` 路径写测试
- fixture 结构已经固定，不再临时发明格式

### Week 2: 补第一批最小高价值 compliance cases

Week 2 的目标是让 compliance 测试真正开始约束 runtime 主链路，而不是继续停留在设计文档。

#### 第一批必须补的测试域

1. `bootstrap`
2. `finalize`
3. `idempotency`
4. `repair`

#### 第一批建议补的具体用例

- `bootstrap_success_creates_context`
- `capture_sets_required_metadata_fields`
- `finalize_is_idempotent`
- `finalize_does_not_duplicate_summary`
- `repair_recovers_pending_finalize_session`
- `ingest_same_key_is_idempotent`

#### 第一批建议覆盖的 profile

优先顺序：

1. `Claude Code`
2. `Codex`

原因：

- `Claude Code` 是完整 lifecycle 标杆
- `Codex` 是最有代表性的 guided-memory 场景

#### 暂缓的 profile

Week 2 暂缓：

- `Cursor`
- `Cline`

原因：

- 二者更依赖后续 adapter / wrapper / extension bridge 的落地
- 过早写全套 conformance 容易变成空壳测试

### 两周后的预期结果

完成这两周后，compliance 层应达到：

1. 不再只是设计文档
2. 已能验证 runtime 关键链路
3. 已能对 `Claude Code` 和 `Codex` 给出初步合规判断
4. 为后续 `Cursor / Cline` 扩展保留统一测试框架

### 两周内明确不做

为控制范围，这两周内不建议做：

1. 全 profile L1-L4 覆盖
2. 完整 official support matrix 发布
3. 大量 UI/宿主特定行为模拟
4. 与检索质量评测混在一起

### 执行原则

compliance 测试必须跟着 runtime 主链路推进：

- runtime 未接通的链路，不要先写大而空的 conformance
- runtime 一旦接通，优先补关键用例
- 先保证 finalize / repair / idempotency 这些 blocker 级行为可测

---

## 20. Key Decisions

本设计锁定以下决策：

1. 合规测试围绕协议正确性，而不是检索质量本身
2. `bootstrap / capture / finalize / repair / isolation` 是必测五大核心
3. 幂等是 blocker 级要求，不是增强项
4. `Claude Code` 作为 L4 标杆
5. `Codex` / `Cursor` 允许阶段性只达 L2/L3
6. 未通过 repair 和 isolation 的接入，不得宣称完整支持

---

## 21. Open Questions

1. 是否要为 `finalize_session` 单独定义 golden response fixture
2. transcript fixture 是采用 JSONL 还是宿主原始格式 + parser fixture
3. 是否要引入 chaos mode 随机打断 finalize/retry
4. 是否要将 compliance report 接入现有 `docs/evaluations/`

---

## 22. Recommended Next Spec

下一份建议补：

**LocalMem Security Model for Multi-Tool Integrations**

重点定义：

- user_id 绑定
- scope 授权
- tool capability 授权
- transcript redaction
- host trust boundary
