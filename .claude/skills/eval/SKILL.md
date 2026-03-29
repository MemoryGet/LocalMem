---
name: eval
description: 手动触发 IClude 项目四维专家评估，并将报告按时间戳存入 docs/evaluations/
user-invocable: true
---

# IClude 项目专家评估

## 执行步骤

### 1. 确认目录
确保 `docs/evaluations/` 目录存在（若不存在则创建）。

### 2. 生成报告文件名
时间戳格式：`YYYY-MM-DD_HH-MM`，文件名：`docs/evaluations/eval_YYYY-MM-DD_HH-MM.md`

用 Bash 获取当前时间：
```bash
date +"%Y-%m-%d_%H-%M"
```

### 3. 并行 dispatch 四个专家 Agent

同时启动以下 4 个 subagent（**必须并行**，单次 message 多 tool call）：

| Agent | subagent_type | 评估范围 |
|-------|---------------|----------|
| Database Optimizer | `everything-claude-code:database-reviewer` | SQLite schema、索引、迁移、FTS5 查询效率 |
| AI Engineer | `AI Engineer` | 向量检索、RRF 融合、Reflect 引擎、Extractor 质量 |
| Security Engineer | `everything-claude-code:security-reviewer` | OWASP Top10、认证鉴权、输入验证、敏感数据 |
| Software Architect | `everything-claude-code:architect` | 整体架构、依赖流向、模块耦合、可扩展性 |

每个 agent 的 prompt 模板：
```
对 /root/LocalMem 的 IClude 项目进行 [领域] 专项评估。
重点关注：[领域具体关注点]
输出格式（Markdown）：
## [领域]评估报告
### 总体评分（1-10）
### 关键发现
#### CRITICAL
#### HIGH
#### MEDIUM
#### LOW
### 改进建议（按优先级排序）
### 亮点
```

### 4. 汇总写入报告文件

等所有 agent 完成后，将四份报告合并写入报告文件：

```markdown
# IClude 项目专家评估报告
> 生成时间：{timestamp}

## 目录
- [数据库评估](#数据库评估)
- [AI/检索评估](#ai检索评估)
- [安全评估](#安全评估)
- [架构评估](#架构评估)
- [综合总结](#综合总结)

{各 agent 报告内容}

## 综合总结
> 综合4个维度，给出3条最高优先级行动项
```

用 Write 工具写入 `docs/evaluations/eval_{timestamp}.md`。

### 5. 向用户展示

- 告知报告已保存路径
- 展示综合总结（最高优先级行动项）
- 列出各维度评分汇总表
