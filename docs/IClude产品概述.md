# IClude 开源记忆体系统开发文档

> 读者分流：
> - 面向用户/合作方优先看 [对外产品路线图](/root/LocalMem/docs/对外产品路线图.md)
> - 面向研发推进优先看 [内部研发路线图](/root/LocalMem/docs/内部研发路线图.md)

## 目录

0. [项目宗旨](#0-项目宗旨)
1. [项目概述](#1-项目概述)
2. [核心理念](#2-核心理念)
3. [开发规范](#3-开发规范)
4. [技术架构](#4-技术架构)
5. [数据模型](#5-数据模型)
6. [项目结构](#6-项目结构)
7. [阶段规划](#7-阶段规划)
8. [部署方式](#8-部署方式)
9. [运维与安全](#9-运维与安全)
10. [商业模式](#10-商业模式)
11. [核心成功关键](#11-核心成功关键)
12. [风险防控](#12-风险防控)

---

## 0. 项目宗旨

IClude 项目的宗旨是成为一套万物皆可数据化的记忆体系统。

## 1. 项目概述

IClude 是一个开源、本地优先、可混合存储的记忆体系统，旨在为 AI 应用提供长期、结构化、可演化的记忆能力。它首先服务日常用户和开发者场景，让对话、文档、决策等碎片化知识沉淀为可检索记忆；企业级能力作为后续独立路线逐步补齐。通过本地记忆数据获取问题关键数据，解决 AI 上下文过大问题。

本项目采用 **SQLite + Qdrant** 的混合存储架构，并支持灵活的后端开关——你可以单独使用 SQLite（仅结构化/全文检索）、单独使用 Qdrant（仅向量检索），或两者并用实现混合检索。系统设计遵循"一人公司"原则，分阶段演进，从简单 MVP 逐步扩展至企业级能力。

### 核心特性

- **混合存储**：结合关系型数据库的精确查询与向量数据库的语义搜索
- **配置开关**：支持灵活的后端切换，单后端或双后端模式
- **轻量化设计**：单一二进制文件，降低运维成本
- **多语言 SDK**：Python/Go/Node.js 支持

---

## 2. 核心理念

- **开源优先**：核心代码完全开源，建立开发者生态
- **本地优先**：数据默认存储在用户本地，尊重数据主权
- **混合存储**：结合关系型数据库的精确查询与向量数据库的语义搜索，提供更精准的记忆召回
- **渐进式复杂**：从单机单文件开始，按需演进至分布式高可用架构
- **一人可维护**：所有设计均考虑单人开发与运维的成本

---

## 3. 开发规范

### 3.1 项目基础规范

- **核心模式**：个人决策 + AI 辅助研发 + 全流程自动化执行
- **核心原则**：SDK 规格内容完整保留、历史基础规范不删减、一人公司落地策略无缝融合
- **代码风格**：遵循各语言标准规范（Go/Python/Node.js）
- **文档规范**：
  - 提供快速接入文档（README.md）
  - 提供 API 参考文档
  - 提供问题排查文档

### 3.2 SDK 开发规范

- **设计原则**：极简接入、功能完整、兼容适配、安全可靠、易于维护
- **目录结构**：统一遵循「初始化-核心操作-结果返回」结构
- **依赖管理**：
  - 无重量级第三方依赖，仅依赖各语言原生标准库 + HTTP 请求库
  - 依赖包体积 ≤ 500KB
  - 无版本冲突风险
- **测试要求**：
  - 单元测试覆盖率 ≥ 95%
  - 集成测试覆盖所有场景
  - 性能测试（批量存储 QPS ≥ 100，语义检索 QPS ≥ 200，响应延迟 ≤ 200ms）
- **安全规范**：
  - 所有请求采用 HTTPS 加密传输
  - 所有请求自动生成唯一签名
  - 内置统一异常处理机制

### 3.3 混合存储测试要求

- **单元测试**：覆盖每个存储后端的独立操作（SQLite 增删改查、Qdrant 向量增删改查）
- **集成测试**：测试混合检索时多后端结果融合、开关配置生效、数据一致性
- **性能测试**：单后端 QPS ≥ 100，混合后端 QPS ≥ 50（初期目标），响应延迟 ≤ 300ms

### 3.4 代码管理规范

- **版本控制**：遵循语义化版本规范（MAJOR.MINOR.PATCH）
- **代码风格**：统一代码风格，使用语言标准格式化工具
- **目录结构**：采用语义化命名，提升可读性和可维护性
- **文档同步**：代码变更同步更新文档

---

## 4. 技术架构

### 4.1 技术栈选型

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| **开发语言** | Go | 1.21+ | 核心服务 |
| | Python | 3.8+ | SDK 开发 |
| | Node.js | 16+ | SDK 开发 |
| **主存储** | SQLite | 3.x | 结构化数据、全文索引 |
| **向量存储** | Qdrant | 1.7+ | 向量嵌入、相似性检索 |
| **部署方式** | Docker Compose | - | 轻量化部署 |
| **向量嵌入** | 第三方成熟接口 | - | 文心一言/讯飞星火/BGE |

### 4.2 系统架构图

```text
┌─────────────────────────────────────────────┐
│              Client SDK                       │
│         (Python/Go/Node.js)                    │
└───────────────────┬───────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────┐
│           Memory Core Service                  │
│           (单一二进制文件)                       │
├─────────────────────────────────────────────┤
│ ┌─────────────────────────────────────────┐ │
│ │          存储抽象层 (Storage Interface)   │ │
│ │  ┌──────────┐    ┌──────────┐          │ │
│ │  │ SQLite   │    │ Qdrant   │          │ │
│ │  │ 实现     │    │ 实现     │          │ │
│ │  └──────────┘    └──────────┘          │ │
│ └─────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────┐ │
│ │          业务逻辑层                        │ │
│ │  - 记忆 CRUD                              │ │
│ │  - 检索路由与融合                          │ │
│ │  - 配置开关处理                            │ │
│ └─────────────────────────────────────────┘ │
└─────────────────────────────────────────────┘
       │                     │
       ▼                     ▼
┌──────────────┐    ┌────────────────────┐
│  SQLite 文件  │    │  Qdrant (本地容器)  │
│ (memory.db)   │    │ 或 嵌入式 (可选)   │
└──────────────┘    └────────────────────┘
```

### 4.3 数据流说明

- **写入**：根据配置，数据同时写入 SQLite 和/或 Qdrant
- **查询**：根据配置，从启用的后端检索结果，若双后端启用则执行混合检索 → 融合排序 → 返回

### 4.4 配置开关设计

```yaml
# config.yaml
storage:
  sqlite:
    enabled: true
    path: "./data/memory.db"
  qdrant:
    enabled: true
    url: "http://localhost:6333"
    collection: "memories"
    embedding:
      model: "BAAI/bge-small-en-v1.5"
      dimension: 384
retrieval:
  fusion: "rrf"        # rrf 或 weighted
  weights:
    sqlite: 0.3
    qdrant: 0.7
```

---

## 5. 数据模型

### 5.1 SQLite 核心表

```sql
-- 记忆主表
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    metadata JSON,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    access_count INTEGER DEFAULT 0,
    team_id TEXT,
);

-- 实体表（可选，后续图谱用）
CREATE TABLE entities (
    id TEXT PRIMARY KEY,
    name TEXT,
    type TEXT,
    properties JSON
);

-- 关系表
CREATE TABLE relations (
    id TEXT PRIMARY KEY,
    from_entity TEXT,
    to_entity TEXT,
    relation_type TEXT,
    properties JSON
);
```

### 5.2 Qdrant 存储结构

- **Collection**：memories
- **Payload**：包含 memory_id、team_id、content（可选）、created_at 等
- **向量**：由嵌入模型生成的固定维度向量（如 384/768/1536 维）

### 5.3 核心实体

- **Memory**：记忆单元
- **Entity**：实体（图谱用）
- **Relation**：关系（图谱用）
- **Team**：团队/分区（用于多租户隔离）

---

## 6. 项目结构

```
iclude/
├── cmd/                       # 服务入口
│   └── api/                  # HTTP 服务入口
├── internal/                  # 内部包
│   ├── config/               # 配置管理
│   ├── store/                # 存储抽象层
│   │   ├── interface.go     # 存储接口定义
│   │   ├── sqlite/          # SQLite 实现
│   │   └── qdrant/          # Qdrant 实现
│   ├── service/              # 业务逻辑层
│   └── handler/             # HTTP 处理层
├── pkg/                      # 公共包
│   ├── errors/              # 错误定义
│   └── utils/               # 工具函数
├── sdks/                     # SDK
│   ├── python/              # Python SDK
│   ├── go/                  # Go SDK
│   └── nodejs/              # Node.js SDK
├── deploy/                   # 部署配置
│   └── docker-compose/      # Docker Compose 配置
├── documentation/            # 文档
├── tests/                    # 测试
│   ├── unit/                # 单元测试
│   ├── integration/         # 集成测试
│   └── performance/         # 性能测试
├── go.mod
└── README.md
```

---

## 7. 阶段规划

> **竞品参照**：[vectorize-io/hindsight](https://github.com/vectorize-io/hindsight)（仿生记忆系统，LongMemEval SOTA 91.4%）
>
> 当前已完成 Phase 2 的核心能力建设；后续围绕 Hindsight 对比识别出的 7 项借鉴能力（B1~B7），拆分为 Benchmark Track 与 Enterprise Track 两条独立路线。

### 7.1 总体阶段规划

| 阶段 | 时间 | 核心目标 | 关键成果 |
|------|------|----------|----------|
| **Phase 1** | 4-6 周 | MVP 核心验证 | 混合存储 + 分层架构 + 生命周期 + 全功能 API ✅ 已完成 |
| **Phase 2** | 已完成 | 日常用户可交付 | Reflect 反思 + 自动实体抽取 + 图谱检索 + Token 裁剪 |
| **Benchmark Track** | 6-8 周 | 对标 Hindsight 91.4% | LongMemEval 评测闭环 + 精排 + Reflect 利用率优化 |
| **Enterprise Track** | 持续演进 | 企业就绪 | 认证权限 + 管理后台 + 监控 + 多租户 + 商业化 |

### 7.2 Phase 1：MVP 核心验证 ✅ 已完成

**目标**：实现混合存储 + 分层架构 + 全功能 API，跑通核心流程

**已完成清单**：

1. **存储抽象层** — 8 个接口、64 个方法（MemoryStore/VectorStore/ContextStore/TagStore/GraphStore/DocumentStore/Embedder）
2. **SQLite 后端** — 31 列 memories 表 + FTS5 三列加权搜索 + 13 个索引 + V0→V3 迁移框架
3. **Qdrant 后端** — VectorStore + SearchFiltered + payload index
4. **业务逻辑层** — Manager（CRUD + 双写 + 标签）+ ContextManager（树形结构）+ GraphManager（知识图谱）+ Lifecycle（衰减模型）
5. **检索层** — Retriever（三模式：SQLite-only / Qdrant-only / Hybrid RRF）+ 强度加权 + Timeline
6. **文档处理** — 上传 → 段落分块(1000字) → 批量创建记忆
7. **HTTP API** — 27+ 端点，覆盖记忆 CRUD、检索、Context、Tag、Graph、Document、对话摄取、运维
8. **Python SDK** — 基础封装
9. **优化** — FTS5 中文分词器（pkg/tokenizer）+ SQL Builder（pkg/sqlbuilder）+ 连接池调优
10. **测试** — 58+ 测试函数 + HTML 可视化测试报告（pkg/testreport）

**Phase 1 交付物**：

- ✅ Go 核心服务（含 SQLite + Qdrant + 6 个子存储）
- ✅ Python SDK（基础版）
- ✅ Docker Compose 一键启动
- ✅ 完整测试覆盖（store/memory/search/api 四层）
- ✅ 配置文件 + 部署脚本

### 7.3 Phase 2：智能记忆版 ✅ 已完成

**目标**：从"被动存取"升级为"主动学习"——让记忆系统能自动抽取知识、反思已有记忆、利用图谱关联检索

**核心：借鉴 Hindsight 的 Retain/Recall/Reflect 三大能力**

**开发任务**：

1. **🔴 Reflect 反思机制 [B1]（3 周）**
   - 新增 `internal/memory/reflect.go` — Reflect 引擎
   - 流程：召回相关记忆 → 调用外部 LLM 多步推理 → 生成新记忆（kind=mental_model, source_type=reflect）
   - 返回推理结果 + 溯源链（哪些记忆参与了推理）
   - 与"多轮思考型检索 (Iterative Retriever)"合并设计
   - 新增端点：`POST /v1/reflect`（输入问题 + scope，输出推理结果 + trace）
   - 支持配置最大推理轮数、token 预算、使用的 LLM 模型

2. **🔴 Retain 自动实体抽取 [B2]（2 周）**
   - Create 记忆时可选 `auto_extract: true`
   - 调用外部 LLM 从文本中抽取：实体（人/组织/概念/工具）+ 关系（uses/knows/belongs_to）+ 时间
   - 实体规范化：将不同说法的同一实体合并（如"阿里""阿里巴巴""Alibaba"→ 统一节点）
   - 自动写入 GraphStore（Entity + Relation + MemoryEntity）
   - 新增 `internal/memory/extractor.go` — NER 抽取器接口 + LLM 实现

3. **🟡 图谱参与检索 — 三路 RRF [B3]（2 周）**
   - Retriever 增加第三路检索通道：
     1. FTS5 文本检索 → 结果集 A
     2. Qdrant 向量检索 → 结果集 B
     3. **Graph 实体关联检索 → 结果集 C**（从查询提取实体 → 查关系 → 找关联记忆）
   - 三路 RRF 融合，k=60
   - Graph 检索可配置开关（`retrieval.graph_enabled: true`）

4. **🟡 Token 感知裁剪 [B5]（1 周）**
   - Retrieve 请求增加 `max_tokens` 参数（默认 4096）
   - 检索结果按 RRF 分数排序后，从高到低累加 token 数，超出预算则截断
   - 新增简单 tokenizer（按字/词估算 token 数，中文 ≈ 1 字 1 token）

5. **Go/Node.js SDK（2 周）**
   - 移植 Python SDK 到 Go 和 Node.js，保持接口一致
   - 编写示例代码和文档
   - 覆盖 Reflect/Retain/Recall 三大操作

6. **对话摄取事务批量写入（1 周）**
   - IngestConversation 包装在单事务中（db.BeginTx），批量插入提升 10-50x 性能
   - 支持 auto_extract 对话消息的实体

7. **异步 Embedding 处理（1 周）**
   - 将向量生成和写入 Qdrant 改为异步（Go channel 解耦）
   - 批量 Embedding 支持

8. **基础设施优化（1 周）**
   - Qdrant payload index 优化（scope/context_id/kind 创建索引）
   - detail_level 字段裁剪（abstract_only / summary / full）
   - 监控指标（Prometheus — 请求数、延迟、错误率）

9. **🟡 LLM 对话模型增强 [Phase 1 测试反馈]（1 周）**
   - **Context 路径防冲突**：IngestConversation 自动生成 context 时，在路径中追加时间戳或随机后缀（当前 `/conversation-{provider}` 同 provider 多次调用会冲突）
   - **新增 `thinking` 消息角色**：`message_role` 扩展为 user/assistant/system/tool/**thinking**，原生支持 Claude extended thinking、OpenAI o1 reasoning 等思考过程数据（当前需通过 metadata 的 `is_thinking` 字段 workaround）
   - **区分 tool_call 与 tool_result**：`message_role` 拆分为 `tool_call`（assistant 发起的工具调用请求）和 `tool_result`（工具返回结果），替代当前统一使用 `tool` 角色 + metadata 区分的方案
   - **消息父子关联**：Memory 新增 `parent_message_id` 字段，建立 assistant 消息与其触发的 tool_call/tool_result 之间的显式关联链，支持对话树状结构还原

**Phase 2 交付物**：

- ✅ Reflect 反思能力（POST /v1/reflect）
- ✅ 自动实体抽取（auto_extract）
- ✅ 三路 RRF 检索（FTS5 + Qdrant + Graph）
- ✅ Token 感知裁剪
- ✅ 异步 Embedding + 事务批量写入
- ✅ LLM 对话模型增强（thinking 角色、tool_call/tool_result 拆分、消息父子关联、Context 防冲突）
- 🟡 Python SDK 已完善；Go/Node.js SDK 移至 Enterprise Track
- 🟡 监控能力保留在 Enterprise Track，不作为日常用户交付阻塞项

### 7.4 Benchmark Track：对标 Hindsight 91.4%

**目标**：在不改变“Phase 2 已可交付给日常用户”这一前提下，持续提升 LongMemEval 表现，把 IClude 做成真正有竞争力的开源记忆系统。

**开发任务**：

1. **LongMemEval 评测闭环**
   - 新增 `testing/eval/`，支持官方数据集跑批
   - 统一输出 accuracy / recall@k / MRR / NDCG / category breakdown
   - 固化 full-context、hybrid retrieval、reflect 三条基线

2. **Cross-encoder 重排 [B4]**
   - 新增 `Reranker` 接口
   - 管道升级为：三路检索 → RRF → Cross-encoder → 强度加权 → Token 裁剪
   - 支持本地轻量模型或远程 rerank API

3. **Reflect 利用率增强**
   - 多轮 Reflect 累积前几轮 reasoning + 证据摘要
   - Top-K 按 intent / token budget 动态调整
   - 引入精确 token 计算，减少误裁剪

4. **时间与演化增强**
   - Temporal query 动态时间窗口
   - Consolidation 候选改为时间轮转 + 随机采样
   - 补齐三层记忆演化：fact → observation → mental_model

**Benchmark Track 成功标准**：

- 第一阶段：LongMemEval ≥ 80%
- 第二阶段：LongMemEval ≥ 85%
- 长期技术标杆：逼近 Hindsight 91.4%

### 7.5 Enterprise Track：企业版路线

**目标**：服务企业部署、权限治理、安全合规和可视化运维；该路线独立排期，不阻塞 Benchmark Track。

**开发任务**：

- **认证与权限**：JWT / API Key 双模式、团队角色、Context 树访问控制
- **管理后台**：记忆浏览、搜索编辑、图谱可视化、演化链路追踪、导入导出
- **监控与运维**：Prometheus 指标、压测、pprof、告警基础设施
- **多租户与隔离**：按租户拆分 SQLite 文件与向量集合
- **企业交付能力**：SSO、SLA、私有化部署、定制支持
- **分布式后端**：PostgreSQL + pgvector、Qdrant 集群、Milvus 选配

---

## 8. 部署方式

### 8.1 本地运行

下载二进制文件，配置后直接运行。

### 8.2 Docker 单容器

将核心服务和 Qdrant 打包成 Compose。

```yaml
version: '3.8'
services:
  iclude:
    image: iclude/iclude:latest
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      - ENABLE_SQLITE=true
      - ENABLE_QDRANT=true

  qdrant:
    image: qdrant/qdrant:latest
    ports:
      - "6333:6333"
    volumes:
      - ./qdrant:/qdrant/storage
```

### 8.3 Kubernetes

提供 Helm Chart（Phase 3+）

---

## 9. 运维与安全

### 9.1 运维要求

- 保障核心服务 7×24 小时稳定运行，服务可用率 ≥ 99.9%
- 保障 SDK 接口调用稳定性，接口可用率 ≥ 99.9%，响应延迟 ≤ 300ms
- 实现全维度监控，故障秒级告警
- 完成数据每日全量备份，保障数据安全性
- 支持轻量化扩容

### 9.2 安全与合规

- **数据安全**：
  - 传输加密（HTTPS/TLS 1.3）
  - 存储加密（AES-256）
  - 租户数据完全隔离
  - 历史版本全量留存，满足合规追溯
- **访问控制**：
  - API 级别鉴权
  - 租户级别隔离
  - 应用级别权限
- **合规**：
  - 数据本地化（私有部署）
  - 数据导出权
  - 数据更新/删除操作日志全量记录

---

## 10. 商业模式

### 10.1 定价策略

| 套餐名称 | 月费 | 核心限制 | 定位 |
|----------|------|----------|------|
| Starter | ¥99 | 3个应用/1GB存储 | 引流款 |
| Professional | ¥499 | 10个应用/10GB存储 | 核心盈利款 |
| Enterprise | ¥1999 | 无限应用/100GB存储 | 高利润款 |
| 私有部署 | 面议 | 按需定制 | 高附加值款 |

### 10.2 优惠策略

- 年付享 8 折优惠
- 老客户升级套餐享 9 折优惠
- SDK 永久免费接入

### 10.3 数据承诺

- 数据归属：永远是企业的
- 不用于 AI 训练：绝对不用客户数据
- 可导出：企业随时导出全部数据
- 可删除：企业可要求彻底删除

---

## 11. 核心成功关键

1. **技术稳定性**：保障核心服务稳定运行，SDK 接口调用稳定
2. **性能优化**：确保语义检索响应延迟满足企业级需求
3. **安全合规**：实现租户间数据完全隔离，满足企业级数据安全要求
4. **极简接入**：SDK 实现一行代码接入、三步完成开发
5. **行业聚焦**：以咨询行业为突破口，快速验证产品市场 fit
6. **AI 自动化**：充分利用 AI 能力，实现全流程自动化执行
7. **商业闭环**：快速完成种子客户转化，实现正向盈利
8. **持续迭代**：基于客户反馈持续优化产品

---

## 12. 风险防控

1. **技术风险**
   - 应对措施：实现多重备份，配置多通道告警

2. **性能风险**
   - 应对措施：优化数据库索引，实现热点数据缓存，进行充分的性能测试

3. **安全风险**
   - 应对措施：实现传输加密、存储加密、请求签名

4. **市场风险**
   - 应对措施：聚焦行业，快速验证产品市场 fit，建立行业标杆客户

5. **运营风险**
   - 应对措施：实现 AI 全自动化运营，降低人工成本

---

## 附录

### A. 环境变量配置

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| ENABLE_SQLITE | 启用 SQLite 后端 | true |
| ENABLE_QDRANT | 启用 Qdrant 后端 | true |
| SQLITE_PATH | SQLite 数据库路径 | ./data/memory.db |
| QDRANT_URL | Qdrant 服务地址 | http://localhost:6333 |
| LOG_LEVEL | 日志级别 | info |

### B. API 接口列表

| 接口 | 方法 | 说明 |
|------|------|------|
| /api/v1/memories | POST | 创建记忆 |
| /api/v1/memories/:id | GET | 获取记忆 |
| /api/v1/memories/:id | PUT | 更新记忆 |
| /api/v1/memories/:id | DELETE | 删除记忆 |
| /api/v1/search | POST | 检索记忆 |

---

**文档版本**：v2.1

**最后更新**：2026-04-01

**变更记录**：
- v2.1 (2026-04-01)：明确 Phase 2 为日常用户可交付版本；路线拆分为 Benchmark Track（对标 Hindsight 91.4%）与 Enterprise Track（企业能力建设）；修正文档中的交付口径
- v2.0 (2026-03-19)：Phase 1 标记已完成，Phase 2~3 融合 Hindsight 竞品对比的 7 项借鉴能力（Reflect/自动实体抽取/图谱检索/Cross-encoder/Token裁剪/记忆演化/行为配置），新增 MCP Server 规划
- v1.0 (2026-03-08)：初始版本
