#!/usr/bin/env python3
"""
LocalMem 检索命中率测试 — 500 组查询
生成 HTML 报告到 tools/retrieval_report.html
"""

import argparse
import json
import sys
import time
import requests
from datetime import datetime
from collections import defaultdict

BASE = "http://localhost:8080/v1"
HEADERS = {"Content-Type": "application/json"}
REPORT_HTML = "tools/retrieval_report.html"
REPORT_JSON = "tools/retrieval_results.json"

# ──────────────────────────────────────────────
# 1. 种子数据：80 条多样化记忆
# ──────────────────────────────────────────────

SEED_MEMORIES = [
    # ── 个人偏好 (preference) ──
    {"content": "用户偏好暗色主题，所有编辑器和终端都使用 dark mode", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户喜欢用 Vim 键位绑定，在 VS Code 和 IDEA 中都启用了 Vim 插件", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户习惯用 zsh 搭配 oh-my-zsh，主题是 powerlevel10k", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户偏好 Go 语言开发后端服务，认为 Go 的并发模型最适合微服务", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户不喜欢 Java 的冗长语法，尽量避免使用 Spring Boot", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户喜欢喝美式咖啡，每天早上 9 点一杯", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户偏好使用 SQLite 而不是 PostgreSQL 做本地存储，因为部署简单", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户喜欢极简设计风格，讨厌过多的装饰和动画效果", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户习惯用 tmux 做终端多窗口管理，不用 screen", "kind": "profile", "sub_kind": "preference"},
    {"content": "用户偏好 Markdown 写文档，不喜欢用 Word 或 Google Docs", "kind": "profile", "sub_kind": "preference"},

    # ── 技术事实 (fact) ──
    {"content": "服务器部署在阿里云 ECS 上海区域，2核4G配置，IP 是 47.102.xxx.xxx", "kind": "fact", "sub_kind": "entity"},
    {"content": "项目数据库从 PostgreSQL 迁移到了 SQLite，原因是减少运维成本和简化部署", "kind": "fact", "sub_kind": "event"},
    {"content": "Qdrant 向量数据库运行在 Docker 容器中，端口 6333，集合名 memories", "kind": "fact", "sub_kind": "entity"},
    {"content": "Go 项目的模块名是 iclude，要求 Go 1.25 以上版本", "kind": "fact", "sub_kind": "entity"},
    {"content": "FTS5 全文搜索使用 BM25 算法，权重配置：content 10.0, abstract 5.0, summary 3.0", "kind": "fact", "sub_kind": "entity"},
    {"content": "API 服务监听 8080 端口，MCP 服务监听 8081 端口", "kind": "fact", "sub_kind": "entity"},
    {"content": "项目使用 Gin 框架做 HTTP 路由，Zap 做结构化日志", "kind": "fact", "sub_kind": "entity"},
    {"content": "Embedding 模型使用 OpenAI text-embedding-3-small，维度 1536", "kind": "fact", "sub_kind": "entity"},
    {"content": "SQLite 数据库使用 WAL 模式，mmap_size 256MB，最大连接数 5", "kind": "fact", "sub_kind": "entity"},
    {"content": "前端测试面板用 Vue 3 + Vite 构建，运行在 localhost:5173", "kind": "fact", "sub_kind": "entity"},

    # ── 项目决策 (decision) ──
    {"content": "决定使用 Reciprocal Rank Fusion (RRF) 算法合并多路检索结果，k=60", "kind": "note", "sub_kind": "pattern"},
    {"content": "选择 simple tokenizer 而不是 jieba，因为 jieba 需要额外 HTTP 服务，部署复杂度高", "kind": "note", "sub_kind": "pattern"},
    {"content": "Reflect 反思引擎设计为独立包 internal/reflect，避免 memory↔search 循环依赖", "kind": "note", "sub_kind": "pattern"},
    {"content": "数据库迁移采用版本号递增方案 V0→V11，每次迁移必须幂等", "kind": "note", "sub_kind": "pattern"},
    {"content": "FTS5 查询改用 OR 逻辑代替默认的 AND，提高中文召回率", "kind": "note", "sub_kind": "pattern"},
    {"content": "MCP stdio 传输模式作为主要协议，SSE 作为备选，兼容 Codex CLI", "kind": "note", "sub_kind": "pattern"},
    {"content": "采用 best-effort dual-write 策略：SQLite 为主，Qdrant 失败不回滚", "kind": "note", "sub_kind": "pattern"},
    {"content": "记忆强度采用指数衰减模型，不同 retention_tier 有不同半衰期", "kind": "note", "sub_kind": "pattern"},
    {"content": "文档处理采用 Docling → Tika 三层 fallback 链，保证解析成功率", "kind": "note", "sub_kind": "pattern"},
    {"content": "知识图谱实体抽取使用 LLM 三级 fallback 解析：JSON → 正则 → LLM 重试", "kind": "note", "sub_kind": "pattern"},

    # ── 经验教训 (experience/skill) ──
    {"content": "上次并发写入 SQLite 导致数据库锁死，解决方案是加 busy_timeout=5000 和限制最大连接数为 5", "kind": "skill", "sub_kind": "case"},
    {"content": "Docling 解析 PDF 时 OOM，原因是没有限制 HTTP 响应体大小，修复方案是用 io.LimitReader", "kind": "skill", "sub_kind": "case"},
    {"content": "FTS5 搜索中文时召回率低，根因是 AND 逻辑要求所有词都匹配，改为 OR 后提升 30%", "kind": "skill", "sub_kind": "case"},
    {"content": "goroutine 裸启动导致 panic 崩溃整个进程，必须加 defer recover() 和错误日志", "kind": "skill", "sub_kind": "case"},
    {"content": "GraphStore 缺少 GetRelation 方法导致删除关系时无法做归属验证，接口设计必须支持完整授权链路", "kind": "skill", "sub_kind": "case"},
    {"content": "V10 数据库迁移非幂等，重跑会崩溃，所有 ALTER TABLE ADD COLUMN 必须检查列是否存在", "kind": "skill", "sub_kind": "case"},
    {"content": "scanMemory 函数在三处重复 60 行扫描逻辑，新增字段要改三处，重构为 scanDest 结构体统一处理", "kind": "skill", "sub_kind": "case"},
    {"content": "tag_handler 的 6 个方法都缺少 scope 授权检查，安全评分从 8.5 跌到 7.2", "kind": "skill", "sub_kind": "case"},
    {"content": "中文字符串用 len(s) 得到字节数而非字符数，导致 chunk 是预期 3 倍大，必须用 []rune", "kind": "skill", "sub_kind": "case"},
    {"content": "Create 方法先 SELECT COUNT 再 INSERT 存在 TOCTOU 竞态，应改为 UNIQUE 约束 + 冲突处理", "kind": "skill", "sub_kind": "case"},

    # ── 人物关系 ──
    {"content": "张伟是项目的后端技术负责人，擅长 Go 和分布式系统", "kind": "fact", "sub_kind": "entity"},
    {"content": "李明是前端开发工程师，负责 Vue 测试面板和配置生成器", "kind": "fact", "sub_kind": "entity"},
    {"content": "王芳是产品经理，负责 LocalMem 的需求定义和优先级排序", "kind": "fact", "sub_kind": "entity"},
    {"content": "我最好的朋友叫陈浩，我们从大学就认识，他现在在腾讯做算法工程师", "kind": "profile", "sub_kind": "entity"},
    {"content": "老板要求每周五下午 3 点开技术周会，必须准备 demo", "kind": "fact", "sub_kind": "event"},

    # ── 时间节点/截止日期 ──
    {"content": "4月15日之前必须完成 MCP stdio 模式的稳定性测试", "kind": "note", "sub_kind": "event"},
    {"content": "Q2 目标是将检索召回率从 83% 提升到 95%", "kind": "note", "sub_kind": "event"},
    {"content": "5月1日发布 v1.0 正式版，需要完成文档和 SDK", "kind": "note", "sub_kind": "event"},
    {"content": "3月底完成了数据库迁移从 V8 到 V11", "kind": "fact", "sub_kind": "event"},
    {"content": "上周三修复了 FTS5 的 OR 查询问题，commit 4da099c", "kind": "fact", "sub_kind": "event"},

    # ── 配置/环境 ──
    {"content": "OPENAI_API_KEY 存放在 .env 文件中，不能提交到 git", "kind": "fact", "sub_kind": "entity"},
    {"content": "Docker Compose 配置在 deploy/docker-compose.yml，包含 server + qdrant + docling", "kind": "fact", "sub_kind": "entity"},
    {"content": "日志级别在开发环境用 debug，生产环境用 info", "kind": "fact", "sub_kind": "entity"},
    {"content": "测试数据库使用独立的 SQLite 文件 test.db，不影响主库", "kind": "fact", "sub_kind": "entity"},
    {"content": "CI/CD 使用 GitHub Actions，分支保护要求至少一人 review", "kind": "fact", "sub_kind": "entity"},

    # ── 架构设计 ──
    {"content": "系统分为五层：cmd → api → manager(memory/search/reflect/document) → store → model", "kind": "note", "sub_kind": "pattern"},
    {"content": "Store 层定义 8 个接口：MemoryStore(18方法), VectorStore(6), Embedder(2), ContextStore(10), TagStore(8), GraphStore(12), DocumentStore(8)", "kind": "note", "sub_kind": "pattern"},
    {"content": "配置加载优先级：.env → Viper defaults → 环境变量 → config.yaml", "kind": "fact", "sub_kind": "entity"},
    {"content": "Memory 模型有 31 个字段，支持 5 种 retention_tier：permanent/long_term/standard/short_term/ephemeral", "kind": "fact", "sub_kind": "entity"},
    {"content": "Heartbeat 心跳引擎负责自动衰减审计、孤儿清理和矛盾检测，默认关闭", "kind": "fact", "sub_kind": "entity"},

    # ── 用户角色/背景 ──
    {"content": "用户是一名全栈开发工程师，主力语言是 Go 和 TypeScript", "kind": "profile", "sub_kind": "entity"},
    {"content": "用户在做一个 AI 记忆系统项目叫 LocalMem，目标是让 AI 具有长期记忆能力", "kind": "profile", "sub_kind": "entity"},
    {"content": "用户有 5 年后端开发经验，之前在阿里云工作过", "kind": "profile", "sub_kind": "entity"},
    {"content": "用户正在学习 Rust，计划用 Rust 重写核心检索模块提升性能", "kind": "profile", "sub_kind": "entity"},
    {"content": "用户的 GitHub 用户名是 mem_Tao", "kind": "profile", "sub_kind": "entity"},

    # ── 项目进展 ──
    {"content": "MCP ingest_conversation 工具已完成开发，支持批量对话摄入", "kind": "note", "sub_kind": "event"},
    {"content": "Python SDK 已发布到 sdks/python/iclude 目录，支持 retain/recall/scan", "kind": "note", "sub_kind": "event"},
    {"content": "配置生成器网页工具已完成，支持 basic/standard/premium 三档模板", "kind": "note", "sub_kind": "event"},
    {"content": "知识图谱实体抽取功能已上线，通过 POST /v1/memories/:id/extract 触发", "kind": "note", "sub_kind": "event"},
    {"content": "Codex CLI 集成脚本已完成，放在 integrations/codex/ 目录", "kind": "note", "sub_kind": "event"},

    # ── 杂项/生活 ──
    {"content": "家里的猫叫小橘，是一只橘猫，今年 3 岁了", "kind": "profile", "sub_kind": "entity"},
    {"content": "每周六下午打篮球，在公司附近的体育馆", "kind": "profile", "sub_kind": "preference"},
    {"content": "下个月要去日本东京出差，参加一个 AI 技术会议", "kind": "note", "sub_kind": "event"},
    {"content": "最近在看的书是《Designing Data-Intensive Applications》，DDIA 这本书非常经典", "kind": "profile", "sub_kind": "preference"},
    {"content": "生日是 10 月 15 日", "kind": "profile", "sub_kind": "entity"},

    # ── 更多技术细节 ──
    {"content": "三路检索公式：score = Σ weight × 1/(k + rank + 1)，其中 k=60", "kind": "fact", "sub_kind": "entity"},
    {"content": "MarkdownChunker 使用三层分割策略：标题 → 段落 → 递归字符分割", "kind": "fact", "sub_kind": "entity"},
    {"content": "Token 预算裁剪按分数从高到低截取，直到累计 token 达到 max_tokens", "kind": "fact", "sub_kind": "entity"},
    {"content": "内存模型的 Content 字段权重最高(10.0)，Abstract 次之(5.0)，Summary 最低(3.0)", "kind": "fact", "sub_kind": "entity"},
    {"content": "SQLite 连接池配置：MaxOpen=5, MaxIdle=2, ConnMaxLifetime=5min", "kind": "fact", "sub_kind": "entity"},
    {"content": "Rate limiter 配置：retrieve 10req/s burst 20, write 20req/s burst 40", "kind": "fact", "sub_kind": "entity"},
    {"content": "Consolidation 合并功能通过相似度阈值判断，min_age_days 控制最小合并年龄", "kind": "fact", "sub_kind": "entity"},
    {"content": "GIN 中间件链：CORS → RateLimit → Auth → Identity → Logger → Handler", "kind": "fact", "sub_kind": "entity"},
]

# ──────────────────────────────────────────────
# 2. 500 组测试查询
# 每条格式：(query, expected_keyword_in_content, category, difficulty)
# category: exact/synonym/fuzzy/cross_lang/negation/compound/context/temporal/entity/implicit
# difficulty: easy/medium/hard
# ──────────────────────────────────────────────

TEST_QUERIES = [
    # ════════ 精确匹配 (exact) - 80 条 ════════
    ("暗色主题", "暗色主题", "exact", "easy"),
    ("dark mode", "dark mode", "exact", "easy"),
    ("Vim 键位", "Vim 键位", "exact", "easy"),
    ("oh-my-zsh", "oh-my-zsh", "exact", "easy"),
    ("powerlevel10k", "powerlevel10k", "exact", "easy"),
    ("Go 语言开发后端", "Go 语言", "exact", "easy"),
    ("Spring Boot", "Spring Boot", "exact", "easy"),
    ("美式咖啡", "美式咖啡", "exact", "easy"),
    ("SQLite 本地存储", "SQLite", "exact", "easy"),
    ("极简设计", "极简设计", "exact", "easy"),
    ("tmux 终端", "tmux", "exact", "easy"),
    ("Markdown 文档", "Markdown", "exact", "easy"),
    ("阿里云 ECS", "阿里云 ECS", "exact", "easy"),
    ("PostgreSQL 迁移", "PostgreSQL", "exact", "easy"),
    ("Qdrant 向量数据库", "Qdrant", "exact", "easy"),
    ("iclude 模块名", "iclude", "exact", "easy"),
    ("BM25 算法", "BM25", "exact", "easy"),
    ("8080 端口", "8080", "exact", "easy"),
    ("Gin 框架", "Gin 框架", "exact", "easy"),
    ("Zap 日志", "Zap", "exact", "easy"),
    ("text-embedding-3-small", "text-embedding-3-small", "exact", "easy"),
    ("WAL 模式", "WAL", "exact", "easy"),
    ("Vue 3 Vite", "Vue 3", "exact", "easy"),
    ("RRF 算法", "RRF", "exact", "easy"),
    ("simple tokenizer", "simple tokenizer", "exact", "easy"),
    ("reflect 反思引擎", "reflect", "exact", "easy"),
    ("数据库迁移 V0", "迁移", "exact", "easy"),
    ("FTS5 OR 逻辑", "OR", "exact", "easy"),
    ("MCP stdio", "stdio", "exact", "easy"),
    ("dual-write", "dual-write", "exact", "easy"),
    ("指数衰减", "指数衰减", "exact", "easy"),
    ("Docling Tika", "Docling", "exact", "easy"),
    ("LLM 三级 fallback", "fallback", "exact", "easy"),
    ("busy_timeout", "busy_timeout", "exact", "easy"),
    ("io.LimitReader", "LimitReader", "exact", "easy"),
    ("defer recover", "recover", "exact", "easy"),
    ("GetRelation 方法", "GetRelation", "exact", "easy"),
    ("scanDest 结构体", "scanDest", "exact", "easy"),
    ("scope 授权", "scope", "exact", "easy"),
    ("TOCTOU 竞态", "TOCTOU", "exact", "easy"),
    ("张伟 后端", "张伟", "exact", "easy"),
    ("李明 前端", "李明", "exact", "easy"),
    ("王芳 产品", "王芳", "exact", "easy"),
    ("陈浩 朋友", "陈浩", "exact", "easy"),
    ("技术周会", "技术周会", "exact", "easy"),
    ("4月15日 MCP", "4月15日", "exact", "easy"),
    ("Q2 召回率", "召回率", "exact", "easy"),
    ("v1.0 正式版", "v1.0", "exact", "easy"),
    ("OPENAI_API_KEY", "OPENAI_API_KEY", "exact", "easy"),
    ("docker-compose", "docker-compose", "exact", "easy"),
    ("GitHub Actions", "GitHub Actions", "exact", "easy"),
    ("五层架构", "五层", "exact", "easy"),
    ("MemoryStore 18方法", "MemoryStore", "exact", "easy"),
    ("config.yaml 优先级", "config.yaml", "exact", "easy"),
    ("retention_tier", "retention_tier", "exact", "easy"),
    ("Heartbeat 心跳", "Heartbeat", "exact", "easy"),
    ("全栈开发", "全栈", "exact", "easy"),
    ("5年后端经验", "5年", "exact", "easy"),
    ("Rust 重写", "Rust", "exact", "easy"),
    ("mem_Tao", "mem_Tao", "exact", "easy"),
    ("ingest_conversation", "ingest_conversation", "exact", "easy"),
    ("Python SDK", "Python SDK", "exact", "easy"),
    ("配置生成器", "配置生成器", "exact", "easy"),
    ("实体抽取", "实体抽取", "exact", "easy"),
    ("Codex CLI", "Codex CLI", "exact", "easy"),
    ("小橘 橘猫", "小橘", "exact", "easy"),
    ("打篮球", "打篮球", "exact", "easy"),
    ("日本东京", "东京", "exact", "easy"),
    ("DDIA", "DDIA", "exact", "easy"),
    ("生日 10月", "10月15日", "exact", "easy"),
    ("MarkdownChunker", "MarkdownChunker", "exact", "easy"),
    ("token 预算", "token", "exact", "easy"),
    ("Content 权重", "权重", "exact", "easy"),
    ("MaxOpen=5", "MaxOpen", "exact", "easy"),
    ("Rate limiter", "Rate limiter", "exact", "easy"),
    ("Consolidation", "Consolidation", "exact", "easy"),
    ("中间件链 CORS", "CORS", "exact", "easy"),
    ("[]rune 中文", "rune", "exact", "easy"),
    ("幂等 迁移", "幂等", "exact", "easy"),
    ("OOM Docling", "OOM", "exact", "easy"),

    # ════════ 近义词/语义相关 (synonym) - 80 条 ════════
    ("深色模式 编辑器", "暗色主题", "synonym", "medium"),
    ("夜间模式", "暗色主题", "synonym", "medium"),
    ("代码编辑器键盘映射", "Vim 键位", "synonym", "medium"),
    ("终端工具美化", "powerlevel10k", "synonym", "medium"),
    ("后端开发语言选择", "Go 语言", "synonym", "medium"),
    ("不喜欢啰嗦的编程语言", "Java", "synonym", "medium"),
    ("早晨饮品习惯", "美式咖啡", "synonym", "medium"),
    ("本地数据库选型", "SQLite", "synonym", "medium"),
    ("UI 设计风格", "极简设计", "synonym", "medium"),
    ("终端窗口分屏工具", "tmux", "synonym", "medium"),
    ("写作文档格式", "Markdown", "synonym", "medium"),
    ("云服务器地区", "阿里云", "synonym", "medium"),
    ("数据库技术栈变更", "PostgreSQL", "synonym", "medium"),
    ("向量搜索引擎", "Qdrant", "synonym", "medium"),
    ("全文检索排名算法", "BM25", "synonym", "medium"),
    ("HTTP 服务端口号", "8080", "synonym", "medium"),
    ("Web 框架选型", "Gin", "synonym", "medium"),
    ("结构化日志组件", "Zap", "synonym", "medium"),
    ("文本向量化模型", "embedding", "synonym", "medium"),
    ("数据库日志模式", "WAL", "synonym", "medium"),
    ("测试可视化界面", "Vue 3", "synonym", "medium"),
    ("多路结果融合算法", "RRF", "synonym", "medium"),
    ("分词器选择", "tokenizer", "synonym", "medium"),
    ("思考推理模块", "reflect", "synonym", "medium"),
    ("Schema 升级方案", "迁移", "synonym", "medium"),
    ("搜索词组合策略", "OR", "synonym", "medium"),
    ("AI 工具集成协议", "MCP", "synonym", "medium"),
    ("双写一致性策略", "dual-write", "synonym", "medium"),
    ("记忆衰退机制", "衰减", "synonym", "medium"),
    ("文档解析工具链", "Docling", "synonym", "medium"),
    ("数据库死锁问题", "锁死", "synonym", "medium"),
    ("内存溢出问题", "OOM", "synonym", "medium"),
    ("搜索召回率低", "召回率", "synonym", "medium"),
    ("协程异常处理", "goroutine", "synonym", "medium"),
    ("接口方法缺失", "GetRelation", "synonym", "medium"),
    ("数据库升级回放失败", "幂等", "synonym", "medium"),
    ("重复代码问题", "scanDest", "synonym", "medium"),
    ("权限检查遗漏", "scope", "synonym", "medium"),
    ("并发数据竞争", "TOCTOU", "synonym", "medium"),
    ("后端负责人", "张伟", "synonym", "medium"),
    ("前端开发者", "李明", "synonym", "medium"),
    ("产品负责人", "王芳", "synonym", "medium"),
    ("大学同学", "陈浩", "synonym", "medium"),
    ("每周例会时间", "周五", "synonym", "medium"),
    ("MCP 测试截止日期", "4月15日", "synonym", "medium"),
    ("季度 OKR 目标", "Q2", "synonym", "medium"),
    ("版本发布日期", "5月1日", "synonym", "medium"),
    ("API 密钥存储位置", "OPENAI_API_KEY", "synonym", "medium"),
    ("容器编排配置", "docker-compose", "synonym", "medium"),
    ("持续集成流水线", "GitHub Actions", "synonym", "medium"),
    ("系统分层设计", "五层", "synonym", "medium"),
    ("存储接口数量", "MemoryStore", "synonym", "medium"),
    ("配置文件加载规则", "config.yaml", "synonym", "medium"),
    ("记忆保留级别", "retention_tier", "synonym", "medium"),
    ("定时检查任务", "Heartbeat", "synonym", "medium"),
    ("开发者技术栈", "全栈", "synonym", "medium"),
    ("工作年限", "5年", "synonym", "medium"),
    ("性能优化语言", "Rust", "synonym", "medium"),
    ("git 用户名", "mem_Tao", "synonym", "medium"),
    ("对话批量存储工具", "ingest_conversation", "synonym", "medium"),
    ("客户端开发库", "Python SDK", "synonym", "medium"),
    ("配置可视化工具", "配置生成器", "synonym", "medium"),
    ("NLP 信息提取", "实体抽取", "synonym", "medium"),
    ("CLI 插件集成", "Codex CLI", "synonym", "medium"),
    ("宠物猫", "小橘", "synonym", "medium"),
    ("运动爱好", "打篮球", "synonym", "medium"),
    ("海外出差", "东京", "synonym", "medium"),
    ("在读技术书", "DDIA", "synonym", "medium"),
    ("出生日期", "10月15日", "synonym", "medium"),
    ("文档分块策略", "MarkdownChunker", "synonym", "medium"),
    ("上下文窗口大小限制", "token", "synonym", "medium"),
    ("BM25 字段权重", "权重", "synonym", "medium"),
    ("数据库连接配置", "MaxOpen", "synonym", "medium"),
    ("接口限流配置", "Rate limiter", "synonym", "medium"),
    ("记忆合并功能", "Consolidation", "synonym", "medium"),
    ("请求处理管道", "中间件", "synonym", "medium"),
    ("Unicode 长度计算", "rune", "synonym", "medium"),
    ("响应体大小限制", "LimitReader", "synonym", "medium"),
    ("异步任务安全", "recover", "synonym", "medium"),
    ("并发写入冲突", "UNIQUE 约束", "synonym", "medium"),

    # ════════ 模糊/口语化 (fuzzy) - 60 条 ════════
    ("我平时用什么主题", "暗色主题", "fuzzy", "medium"),
    ("我用的什么编辑器插件", "Vim", "fuzzy", "medium"),
    ("我的终端长什么样", "powerlevel10k", "fuzzy", "medium"),
    ("我为什么选 Go", "Go 语言", "fuzzy", "medium"),
    ("我讨厌什么语言", "Java", "fuzzy", "medium"),
    ("我早上喝什么", "美式咖啡", "fuzzy", "medium"),
    ("为什么不用大数据库", "SQLite", "fuzzy", "medium"),
    ("我喜欢什么样的 UI", "极简设计", "fuzzy", "medium"),
    ("我用什么写文章", "Markdown", "fuzzy", "medium"),
    ("服务器在哪里", "阿里云", "fuzzy", "medium"),
    ("为什么换了数据库", "PostgreSQL", "fuzzy", "medium"),
    ("向量库跑在哪", "Qdrant", "fuzzy", "medium"),
    ("搜索是怎么排序的", "BM25", "fuzzy", "medium"),
    ("服务跑在哪个端口", "8080", "fuzzy", "medium"),
    ("用的什么 web 框架", "Gin", "fuzzy", "medium"),
    ("怎么打日志的", "Zap", "fuzzy", "medium"),
    ("embedding 用的啥", "embedding", "fuzzy", "medium"),
    ("数据库性能怎么调的", "WAL", "fuzzy", "medium"),
    ("前端用什么框架", "Vue 3", "fuzzy", "medium"),
    ("搜索结果怎么合在一起的", "RRF", "fuzzy", "medium"),
    ("为什么不用结巴分词", "jieba", "fuzzy", "medium"),
    ("reflect 为什么单独放", "循环依赖", "fuzzy", "medium"),
    ("数据库怎么升级的", "迁移", "fuzzy", "medium"),
    ("搜索为什么用 OR", "OR", "fuzzy", "medium"),
    ("Qdrant 写失败怎么办", "dual-write", "fuzzy", "medium"),
    ("记忆会不会过期", "衰减", "fuzzy", "medium"),
    ("PDF 怎么解析的", "Docling", "fuzzy", "medium"),
    ("之前数据库卡住过吗", "锁死", "fuzzy", "hard"),
    ("遇到过内存炸了没", "OOM", "fuzzy", "hard"),
    ("搜索不准怎么回事", "召回率", "fuzzy", "hard"),
    ("goroutine 挂了怎么办", "recover", "fuzzy", "hard"),
    ("接口少方法怎么处理的", "GetRelation", "fuzzy", "hard"),
    ("数据库重跑会崩吗", "幂等", "fuzzy", "hard"),
    ("有重复代码怎么改的", "scanDest", "fuzzy", "hard"),
    ("权限漏洞怎么回事", "scope", "fuzzy", "hard"),
    ("并发写有什么问题", "TOCTOU", "fuzzy", "hard"),
    ("项目里谁管后端", "张伟", "fuzzy", "medium"),
    ("谁写前端", "李明", "fuzzy", "medium"),
    ("产品需求谁定的", "王芳", "fuzzy", "medium"),
    ("我最好的朋友是谁", "陈浩", "fuzzy", "hard"),
    ("每周什么时候开会", "周五", "fuzzy", "medium"),
    ("MCP 什么时候要测完", "4月15日", "fuzzy", "medium"),
    ("今年的检索目标是什么", "95%", "fuzzy", "medium"),
    ("正式版什么时候发", "5月1日", "fuzzy", "medium"),
    ("API key 放哪了", "OPENAI_API_KEY", "fuzzy", "medium"),
    ("怎么用 Docker 部署", "docker-compose", "fuzzy", "medium"),
    ("CI 用的什么", "GitHub Actions", "fuzzy", "medium"),
    ("系统架构是怎样的", "五层", "fuzzy", "medium"),
    ("有多少个存储接口", "8", "fuzzy", "medium"),
    ("配置是怎么加载的", "config.yaml", "fuzzy", "medium"),
    ("记忆能保存多久", "retention_tier", "fuzzy", "medium"),
    ("有什么后台任务", "Heartbeat", "fuzzy", "medium"),
    ("我是做什么的", "全栈", "fuzzy", "medium"),
    ("我工作多久了", "5年", "fuzzy", "medium"),
    ("我在学什么新语言", "Rust", "fuzzy", "medium"),
    ("我家有宠物吗", "小橘", "fuzzy", "hard"),
    ("周末都干嘛", "打篮球", "fuzzy", "hard"),
    ("最近要出差吗", "东京", "fuzzy", "medium"),
    ("最近在看什么书", "DDIA", "fuzzy", "medium"),
    ("我什么时候过生日", "10月15日", "fuzzy", "hard"),

    # ════════ 跨语言/中英混合 (cross_lang) - 40 条 ════════
    ("What theme does the user prefer", "暗色主题", "cross_lang", "hard"),
    ("database migration reason", "PostgreSQL", "cross_lang", "hard"),
    ("vector database setup", "Qdrant", "cross_lang", "hard"),
    ("full text search algorithm", "BM25", "cross_lang", "hard"),
    ("HTTP framework choice", "Gin", "cross_lang", "hard"),
    ("logging library", "Zap", "cross_lang", "hard"),
    ("embedding model name", "embedding", "cross_lang", "medium"),
    ("frontend testing dashboard", "Vue 3", "cross_lang", "hard"),
    ("result fusion algorithm", "RRF", "cross_lang", "hard"),
    ("tokenizer selection", "tokenizer", "cross_lang", "medium"),
    ("reflection engine design", "reflect", "cross_lang", "hard"),
    ("memory decay model", "衰减", "cross_lang", "hard"),
    ("document parsing pipeline", "Docling", "cross_lang", "hard"),
    ("concurrent write issue", "锁死", "cross_lang", "hard"),
    ("out of memory problem", "OOM", "cross_lang", "hard"),
    ("search recall improvement", "召回率", "cross_lang", "hard"),
    ("goroutine panic recovery", "recover", "cross_lang", "medium"),
    ("interface missing method", "GetRelation", "cross_lang", "hard"),
    ("idempotent migration", "幂等", "cross_lang", "hard"),
    ("code duplication refactor", "scanDest", "cross_lang", "hard"),
    ("authorization check missing", "scope", "cross_lang", "hard"),
    ("race condition TOCTOU", "TOCTOU", "cross_lang", "medium"),
    ("backend tech lead", "张伟", "cross_lang", "hard"),
    ("product manager name", "王芳", "cross_lang", "hard"),
    ("best friend info", "陈浩", "cross_lang", "hard"),
    ("weekly meeting schedule", "周五", "cross_lang", "hard"),
    ("Q2 OKR target", "Q2", "cross_lang", "medium"),
    ("release date v1.0", "v1.0", "cross_lang", "medium"),
    ("API key storage", "OPENAI_API_KEY", "cross_lang", "medium"),
    ("CI/CD pipeline setup", "GitHub Actions", "cross_lang", "medium"),
    ("system layer architecture", "五层", "cross_lang", "hard"),
    ("config loading priority", "config.yaml", "cross_lang", "medium"),
    ("memory retention levels", "retention_tier", "cross_lang", "medium"),
    ("background health check", "Heartbeat", "cross_lang", "hard"),
    ("user tech stack", "全栈", "cross_lang", "hard"),
    ("user GitHub username", "mem_Tao", "cross_lang", "medium"),
    ("pet cat info", "小橘", "cross_lang", "hard"),
    ("birthday date", "10月15日", "cross_lang", "hard"),
    ("book currently reading", "DDIA", "cross_lang", "medium"),
    ("business trip plan", "东京", "cross_lang", "hard"),

    # ════════ 复合/多条件 (compound) - 60 条 ════════
    ("Go 语言 微服务 并发", "Go 语言", "compound", "easy"),
    ("SQLite WAL 连接池", "WAL", "compound", "easy"),
    ("暗色主题 编辑器 终端", "暗色主题", "compound", "easy"),
    ("BM25 权重 content abstract", "BM25", "compound", "easy"),
    ("Gin Zap 中间件", "Gin", "compound", "easy"),
    ("RRF 多路检索 k=60", "RRF", "compound", "easy"),
    ("Docling Tika fallback", "Docling", "compound", "easy"),
    ("MCP stdio SSE 协议", "MCP", "compound", "easy"),
    ("SQLite Qdrant 双写", "dual-write", "compound", "easy"),
    ("Docker Compose Qdrant Docling", "docker-compose", "compound", "easy"),
    ("张伟 Go 分布式", "张伟", "compound", "easy"),
    ("陈浩 腾讯 算法", "陈浩", "compound", "easy"),
    ("Go TypeScript 全栈", "全栈", "compound", "easy"),
    ("阿里云 上海 ECS", "阿里云", "compound", "easy"),
    ("FTS5 OR 中文 召回率", "OR", "compound", "easy"),
    ("goroutine panic recover", "recover", "compound", "easy"),
    ("TOCTOU SELECT INSERT 并发", "TOCTOU", "compound", "easy"),
    ("Vue Vite 5173", "Vue 3", "compound", "easy"),
    ("Vim VS Code IDEA 键位", "Vim", "compound", "easy"),
    ("zsh oh-my-zsh powerlevel10k", "oh-my-zsh", "compound", "easy"),
    ("日本 东京 AI 会议", "东京", "compound", "easy"),
    ("DDIA 数据密集型应用", "DDIA", "compound", "easy"),
    ("小橘 橘猫 3岁", "小橘", "compound", "easy"),
    ("4月15日 MCP 测试 deadline", "4月15日", "compound", "easy"),
    ("5月1日 v1.0 发布 SDK", "v1.0", "compound", "easy"),
    ("GraphStore GetRelation 归属验证", "GetRelation", "compound", "easy"),
    ("scanDest scanMemory 重构", "scanDest", "compound", "easy"),
    ("tag_handler scope 安全评分", "tag_handler", "compound", "easy"),
    ("io.LimitReader Docling OOM", "LimitReader", "compound", "easy"),
    ("busy_timeout 并发 SQLite 锁", "busy_timeout", "compound", "easy"),
    ("用户偏好 Go 不喜欢 Java", "Go 语言", "compound", "medium"),
    ("tmux screen 终端多窗口", "tmux", "compound", "medium"),
    ("Markdown 不用 Word 文档", "Markdown", "compound", "medium"),
    ("PostgreSQL SQLite 迁移原因", "PostgreSQL", "compound", "medium"),
    ("Embedding OpenAI 1536 维度", "embedding", "compound", "medium"),
    ("reflect 独立包 循环依赖", "reflect", "compound", "medium"),
    ("simple 分词 jieba 部署复杂", "tokenizer", "compound", "medium"),
    ("V0 V11 迁移 幂等", "幂等", "compound", "medium"),
    ("三层 fallback JSON 正则 LLM", "fallback", "compound", "medium"),
    ("中文 rune 字节 chunk", "rune", "compound", "medium"),
    ("Python SDK retain recall scan", "Python SDK", "compound", "medium"),
    ("basic standard premium 模板", "basic", "compound", "medium"),
    ("extract POST memories id", "实体抽取", "compound", "medium"),
    ("Codex CLI integrations 集成", "Codex CLI", "compound", "medium"),
    ("ingest conversation 批量 对话", "ingest_conversation", "compound", "medium"),
    ("周六 篮球 体育馆", "打篮球", "compound", "medium"),
    ("10月15日 生日", "10月15日", "compound", "easy"),
    ("8081 MCP SSE", "8081", "compound", "easy"),
    ("test.db 测试数据库 独立", "test.db", "compound", "easy"),
    ("debug info 日志级别", "日志级别", "compound", "easy"),
    (".env OPENAI_API_KEY git", "OPENAI_API_KEY", "compound", "easy"),
    ("5年 阿里云 后端", "阿里云", "compound", "medium"),
    ("Rust 重写 检索 性能", "Rust", "compound", "medium"),
    ("mem_Tao GitHub 用户名", "mem_Tao", "compound", "easy"),
    ("Heartbeat 衰减审计 孤儿清理", "Heartbeat", "compound", "easy"),
    ("31字段 Memory 模型", "31", "compound", "easy"),
    ("CORS RateLimit Auth Identity", "CORS", "compound", "easy"),
    ("MarkdownChunker 标题 段落 递归", "MarkdownChunker", "compound", "easy"),
    ("Consolidation 相似度 min_age", "Consolidation", "compound", "easy"),
    ("retrieve 10req/s burst 20", "Rate limiter", "compound", "easy"),

    # ════════ 上下文推理 (context) - 50 条 ════════
    ("为什么搜索不准", "召回率", "context", "hard"),
    ("为什么不用外部数据库", "SQLite", "context", "hard"),
    ("系统有几种搜索方式", "三路", "context", "hard"),
    ("记忆会自动消失吗", "衰减", "context", "hard"),
    ("数据可靠性怎么保证", "dual-write", "context", "hard"),
    ("搜索结果怎么排名", "RRF", "context", "hard"),
    ("为什么要做实体抽取", "实体抽取", "context", "hard"),
    ("文档怎么导入系统", "Docling", "context", "hard"),
    ("怎么跟 AI 工具集成", "MCP", "context", "hard"),
    ("系统安全性如何", "scope", "context", "hard"),
    ("部署需要哪些组件", "docker-compose", "context", "hard"),
    ("测试环境怎么搭", "test.db", "context", "hard"),
    ("项目有 SDK 吗", "Python SDK", "context", "hard"),
    ("配置文件怎么管理", "config.yaml", "context", "hard"),
    ("日志怎么查", "Zap", "context", "hard"),
    ("API 有速率限制吗", "Rate limiter", "context", "hard"),
    ("大文件会不会出问题", "LimitReader", "context", "hard"),
    ("并发安全怎么做", "busy_timeout", "context", "hard"),
    ("知识图谱是做什么的", "GraphStore", "context", "hard"),
    ("中文搜索有什么坑", "rune", "context", "hard"),
    ("怎么理解记忆的强度", "strength", "context", "hard"),
    ("项目的 CI 流程", "GitHub Actions", "context", "hard"),
    ("反思引擎有什么用", "reflect", "context", "hard"),
    ("分块策略怎么设计", "MarkdownChunker", "context", "hard"),
    ("怎么避免重复记忆", "Consolidation", "context", "hard"),
    ("过期记忆怎么处理", "Heartbeat", "context", "hard"),
    ("项目的技术栈是什么", "Go", "context", "medium"),
    ("有几种记忆类型", "retention_tier", "context", "hard"),
    ("请求处理流程是怎样的", "中间件", "context", "hard"),
    ("为什么要三级 fallback", "fallback", "context", "hard"),
    ("怎么保证迁移安全", "幂等", "context", "hard"),
    ("代码规模有多大", "Store", "context", "hard"),
    ("谁在用这个项目", "Codex CLI", "context", "hard"),
    ("有没有前端界面", "Vue 3", "context", "hard"),
    ("向量搜索怎么做", "Qdrant", "context", "hard"),
    ("全文搜索引擎是什么", "FTS5", "context", "hard"),
    ("MCP 有哪些工具", "ingest_conversation", "context", "hard"),
    ("怎么配置不同环境", "basic", "context", "hard"),
    ("对话记录怎么保存", "ingest_conversation", "context", "hard"),
    ("实体关系怎么提取", "LLM", "context", "hard"),
    ("数据一致性怎么保证", "事务", "context", "hard"),
    ("接口有文档吗", "API", "context", "hard"),
    ("系统扩展性如何", "Qdrant", "context", "hard"),
    ("性能瓶颈在哪", "SQLite", "context", "hard"),
    ("有没有缓存机制", "mmap", "context", "hard"),
    ("怎么做自动化测试", "test", "context", "hard"),
    ("项目有几个二进制", "cmd", "context", "hard"),
    ("用户的学习计划", "Rust", "context", "hard"),
    ("谁是技术决策者", "张伟", "context", "hard"),
    ("项目最近的变更", "FTS5", "context", "hard"),

    # ════════ 实体查询 (entity) - 50 条 ════════
    ("张伟", "张伟", "entity", "easy"),
    ("李明", "李明", "entity", "easy"),
    ("王芳", "王芳", "entity", "easy"),
    ("陈浩", "陈浩", "entity", "easy"),
    ("小橘", "小橘", "entity", "easy"),
    ("SQLite", "SQLite", "entity", "easy"),
    ("Qdrant", "Qdrant", "entity", "easy"),
    ("Gin", "Gin", "entity", "easy"),
    ("Zap", "Zap", "entity", "easy"),
    ("Vue", "Vue", "entity", "easy"),
    ("Vite", "Vite", "entity", "easy"),
    ("Docker", "Docker", "entity", "easy"),
    ("GitHub", "GitHub", "entity", "easy"),
    ("OpenAI", "OpenAI", "entity", "easy"),
    ("阿里云", "阿里云", "entity", "easy"),
    ("腾讯", "腾讯", "entity", "easy"),
    ("东京", "东京", "entity", "easy"),
    ("Rust", "Rust", "entity", "easy"),
    ("Go", "Go", "entity", "easy"),
    ("Java", "Java", "entity", "easy"),
    ("TypeScript", "TypeScript", "entity", "easy"),
    ("Python", "Python", "entity", "easy"),
    ("Docling", "Docling", "entity", "easy"),
    ("Tika", "Tika", "entity", "easy"),
    ("BM25", "BM25", "entity", "easy"),
    ("RRF", "RRF", "entity", "easy"),
    ("MCP", "MCP", "entity", "easy"),
    ("FTS5", "FTS5", "entity", "easy"),
    ("WAL", "WAL", "entity", "easy"),
    ("DDIA", "DDIA", "entity", "easy"),
    ("Codex", "Codex", "entity", "easy"),
    ("Heartbeat", "Heartbeat", "entity", "easy"),
    ("Vim", "Vim", "entity", "easy"),
    ("tmux", "tmux", "entity", "easy"),
    ("Markdown", "Markdown", "entity", "easy"),
    ("powerlevel10k", "powerlevel10k", "entity", "easy"),
    ("oh-my-zsh", "oh-my-zsh", "entity", "easy"),
    ("MemoryStore", "MemoryStore", "entity", "easy"),
    ("GraphStore", "GraphStore", "entity", "easy"),
    ("MarkdownChunker", "MarkdownChunker", "entity", "easy"),
    ("scanDest", "scanDest", "entity", "easy"),
    ("mem_Tao", "mem_Tao", "entity", "easy"),
    ("iclude", "iclude", "entity", "easy"),
    ("Consolidation", "Consolidation", "entity", "easy"),
    ("LimitReader", "LimitReader", "entity", "easy"),
    ("TOCTOU", "TOCTOU", "entity", "easy"),
    ("GetRelation", "GetRelation", "entity", "easy"),
    ("tag_handler", "tag_handler", "entity", "easy"),
    ("busy_timeout", "busy_timeout", "entity", "easy"),
    ("text-embedding-3-small", "text-embedding-3-small", "entity", "easy"),

    # ════════ 隐含/间接 (implicit) - 40 条 ════════
    ("我有什么宠物", "小橘", "implicit", "hard"),
    ("我周末有什么活动", "打篮球", "implicit", "hard"),
    ("下次旅行去哪", "东京", "implicit", "hard"),
    ("有什么好书推荐", "DDIA", "implicit", "hard"),
    ("我多大了", "10月15日", "implicit", "hard"),
    ("我认识谁在大厂工作", "腾讯", "implicit", "hard"),
    ("团队有几个人", "张伟", "implicit", "hard"),
    ("我会几种语言", "Go", "implicit", "hard"),
    ("项目最大的技术挑战", "召回率", "implicit", "hard"),
    ("系统最脆弱的部分", "SQLite", "implicit", "hard"),
    ("还有什么功能没做完", "v1.0", "implicit", "hard"),
    ("需要注意的安全问题", "scope", "implicit", "hard"),
    ("影响搜索效果的因素", "BM25", "implicit", "hard"),
    ("数据丢失的风险", "dual-write", "implicit", "hard"),
    ("部署时要注意什么", "docker-compose", "implicit", "hard"),
    ("代码质量如何保证", "GitHub Actions", "implicit", "hard"),
    ("用户体验怎么优化", "极简设计", "implicit", "hard"),
    ("存储成本怎么控制", "SQLite", "implicit", "hard"),
    ("开发效率工具有哪些", "tmux", "implicit", "hard"),
    ("文档管理方式", "Markdown", "implicit", "hard"),
    ("监控告警方案", "Heartbeat", "implicit", "hard"),
    ("扩展性瓶颈", "Qdrant", "implicit", "hard"),
    ("接口设计质量", "MemoryStore", "implicit", "hard"),
    ("测试覆盖情况", "test", "implicit", "hard"),
    ("数据备份策略", "SQLite", "implicit", "hard"),
    ("代码风格规范", "Zap", "implicit", "hard"),
    ("上线前要检查什么", "scope", "implicit", "hard"),
    ("历史遗留问题", "V10", "implicit", "hard"),
    ("最近踩过的坑", "OOM", "implicit", "hard"),
    ("性能调优手段", "WAL", "implicit", "hard"),
    ("中文场景的特殊处理", "rune", "implicit", "hard"),
    ("项目依赖了哪些外部服务", "OpenAI", "implicit", "hard"),
    ("有哪些自动化工具", "配置生成器", "implicit", "hard"),
    ("运维复杂度", "docker-compose", "implicit", "hard"),
    ("用户画像", "全栈", "implicit", "hard"),
    ("工作生活平衡", "打篮球", "implicit", "hard"),
    ("技术选型理由", "Go 语言", "implicit", "hard"),
    ("团队协作方式", "周五", "implicit", "hard"),
    ("知识管理方法", "ingest_conversation", "implicit", "hard"),
    ("最近的里程碑", "MCP", "implicit", "hard"),

    # ════════ 否定/排除 (negation) - 20 条 ════════
    ("不用 PostgreSQL 的原因", "PostgreSQL", "negation", "medium"),
    ("不喜欢 Java", "Java", "negation", "easy"),
    ("不用 screen", "tmux", "negation", "medium"),
    ("不用 Word", "Markdown", "negation", "medium"),
    ("不用 jieba 分词", "simple tokenizer", "negation", "medium"),
    ("Qdrant 失败不回滚", "dual-write", "negation", "medium"),
    ("没有 ON DELETE CASCADE", "级联", "negation", "hard"),
    ("缺少 GetRelation", "GetRelation", "negation", "medium"),
    ("不支持交互式输入", "MCP", "negation", "hard"),
    ("不喜欢动画效果", "极简设计", "negation", "medium"),
    ("不喜欢过多装饰", "极简设计", "negation", "medium"),
    ("不用 Google Docs", "Markdown", "negation", "medium"),
    ("Spring Boot 太冗长", "Java", "negation", "medium"),
    ("jieba 部署复杂不用", "simple", "negation", "medium"),
    ("非幂等迁移 V10", "V10", "negation", "medium"),
    ("mock 测试预测不准", "元数据", "negation", "hard"),
    ("白名单不能有兜底", "白名单", "negation", "hard"),
    ("不要绕过 Manager", "Manager", "negation", "hard"),
    ("不能直接调 Store 写", "Manager", "negation", "hard"),
    ("避免 len(s) 处理中文", "rune", "negation", "medium"),

    # ════════ 时间相关 (temporal) - 20 条 ════════
    ("最近的代码改动", "FTS5", "temporal", "hard"),
    ("上周修了什么 bug", "FTS5", "temporal", "hard"),
    ("下个月的计划", "东京", "temporal", "hard"),
    ("这个季度目标", "Q2", "temporal", "medium"),
    ("4月份要做什么", "MCP", "temporal", "medium"),
    ("5月份的截止日期", "v1.0", "temporal", "medium"),
    ("3月底做了什么", "V8", "temporal", "medium"),
    ("每天早上做什么", "美式咖啡", "temporal", "medium"),
    ("每周五要做什么", "周五", "temporal", "medium"),
    ("每周六做什么", "打篮球", "temporal", "medium"),
    ("今年的发布计划", "v1.0", "temporal", "hard"),
    ("最近的 deadline", "4月15日", "temporal", "medium"),
    ("什么时候迁移完的数据库", "3月底", "temporal", "medium"),
    ("猫养了几年", "3岁", "temporal", "medium"),
    ("认识朋友多久了", "大学", "temporal", "hard"),
    ("commit 4da099c 做了什么", "FTS5", "temporal", "medium"),
    ("上一次安全事故", "tag_handler", "temporal", "hard"),
    ("最新完成的功能", "ingest_conversation", "temporal", "hard"),
    ("什么时候开始学 Rust", "Rust", "temporal", "hard"),
    ("过生日是哪天", "10月15日", "temporal", "easy"),
]

assert len(TEST_QUERIES) == 500, f"Expected 500 queries, got {len(TEST_QUERIES)}"

# ──────────────────────────────────────────────
# 3. 执行测试
# ──────────────────────────────────────────────

def create_memory(mem: dict) -> str | None:
    payload = {
        "content": mem["content"],
        "kind": mem.get("kind", "note"),
        "sub_kind": mem.get("sub_kind", ""),
        "scope": "user/test",
    }
    try:
        r = requests.post(f"{BASE}/memories", json=payload, headers=HEADERS, timeout=10)
        if r.status_code in (200, 201):
            data = r.json().get("data", {})
            return data.get("id")
        elif r.status_code == 429:
            time.sleep(1)
            r2 = requests.post(f"{BASE}/memories", json=payload, headers=HEADERS, timeout=10)
            if r2.status_code in (200, 201):
                return r2.json().get("data", {}).get("id")
            return None
        else:
            print(f"  WARN: create failed ({r.status_code}): {r.text[:100]}")
            return None
    except Exception as e:
        print(f"  ERROR: {e}")
        return None


def build_retrieve_payload(query: str, limit: int, rerank_mode: str) -> dict:
    payload = {"query": query, "limit": limit}

    if rerank_mode == "off":
        payload["rerank_enabled"] = False
    elif rerank_mode in ("overlap", "remote"):
        payload["rerank_enabled"] = True
        payload["rerank_provider"] = rerank_mode
    else:
        raise ValueError(f"unsupported rerank mode: {rerank_mode}")

    return payload


def retrieve(query: str, limit: int = 10, rerank_mode: str = "off") -> list:
    payload = build_retrieve_payload(query, limit, rerank_mode)
    for attempt in range(3):
        try:
            r = requests.post(f"{BASE}/retrieve", json=payload, headers=HEADERS, timeout=10)
            if r.status_code == 200:
                return r.json().get("data", {}).get("results", [])
            if r.status_code == 429:
                time.sleep(0.5)
                continue
            return []
        except Exception:
            return []
    return []


def check_hit(results: list, expected_keyword: str) -> dict:
    """Check if expected_keyword appears in any result content."""
    for i, r in enumerate(results):
        content = r.get("memory", {}).get("content", "")
        abstract = r.get("memory", {}).get("abstract", "")
        if expected_keyword.lower() in content.lower() or expected_keyword.lower() in abstract.lower():
            return {
                "hit": True,
                "rank": i + 1,
                "score": r.get("score", 0),
                "matched_content": content[:100],
                "source": r.get("source", ""),
            }
    return {"hit": False, "rank": -1, "score": 0, "matched_content": "", "source": ""}


def run_tests(rerank_mode: str = "off"):
    print("=" * 60)
    print("LocalMem 检索命中率测试 — 500 组查询")
    print(f"Rerank 模式: {rerank_mode}")
    print("=" * 60)

    # Step 1: Seed data
    print(f"\n[1/3] 写入 {len(SEED_MEMORIES)} 条种子数据...")
    created = 0
    for i, mem in enumerate(SEED_MEMORIES):
        mid = create_memory(mem)
        if mid:
            created += 1
        if (i + 1) % 20 == 0:
            print(f"  已写入 {i+1}/{len(SEED_MEMORIES)}")
    print(f"  完成: {created}/{len(SEED_MEMORIES)} 条")

    # Step 2: Run queries
    print(f"\n[2/3] 执行 {len(TEST_QUERIES)} 组查询...")
    results = []
    start_time = time.time()

    for i, (query, expected, category, difficulty) in enumerate(TEST_QUERIES):
        search_results = retrieve(query, rerank_mode=rerank_mode)
        hit_info = check_hit(search_results, expected)
        results.append({
            "index": i + 1,
            "query": query,
            "expected": expected,
            "category": category,
            "difficulty": difficulty,
            "hit": hit_info["hit"],
            "rank": hit_info["rank"],
            "score": hit_info["score"],
            "matched_content": hit_info["matched_content"],
            "source": hit_info["source"],
            "result_count": len(search_results),
        })
        if (i + 1) % 50 == 0:
            elapsed = time.time() - start_time
            hits = sum(1 for r in results if r["hit"])
            print(f"  {i+1}/500 — 命中 {hits}/{i+1} ({hits/(i+1)*100:.1f}%) — {elapsed:.1f}s")

    total_time = time.time() - start_time

    # Step 3: Generate report
    print(f"\n[3/3] 生成报告...")
    generate_report(results, total_time, created, rerank_mode)
    print(f"\n完成! 耗时 {total_time:.1f}s")


def generate_report(results: list, total_time: float, seed_count: int, rerank_mode: str):
    total = len(results)
    hits = sum(1 for r in results if r["hit"])
    miss = total - hits
    hit_rate = hits / total * 100

    # Per-category stats
    cat_stats = defaultdict(lambda: {"total": 0, "hits": 0, "scores": []})
    for r in results:
        cat = r["category"]
        cat_stats[cat]["total"] += 1
        if r["hit"]:
            cat_stats[cat]["hits"] += 1
            cat_stats[cat]["scores"].append(r["score"])

    # Per-difficulty stats
    diff_stats = defaultdict(lambda: {"total": 0, "hits": 0})
    for r in results:
        d = r["difficulty"]
        diff_stats[d]["total"] += 1
        if r["hit"]:
            diff_stats[d]["hits"] += 1

    # Top-N analysis
    top1 = sum(1 for r in results if r["hit"] and r["rank"] == 1)
    top3 = sum(1 for r in results if r["hit"] and r["rank"] <= 3)
    top5 = sum(1 for r in results if r["hit"] and r["rank"] <= 5)

    # Failed queries
    failures = [r for r in results if not r["hit"]]

    # Category labels
    cat_labels = {
        "exact": "精确匹配",
        "synonym": "近义词",
        "fuzzy": "模糊/口语",
        "cross_lang": "跨语言",
        "compound": "复合条件",
        "context": "上下文推理",
        "entity": "实体查询",
        "implicit": "隐含/间接",
        "negation": "否定/排除",
        "temporal": "时间相关",
    }

    diff_labels = {"easy": "简单", "medium": "中等", "hard": "困难"}

    # Category order
    cat_order = ["exact", "synonym", "fuzzy", "cross_lang", "compound", "context", "entity", "implicit", "negation", "temporal"]

    # Generate HTML
    html = f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>LocalMem 检索命中率测试报告</title>
<style>
:root {{
  --bg: #0d1117; --card: #161b22; --border: #30363d;
  --text: #c9d1d9; --text2: #8b949e; --green: #3fb950;
  --red: #f85149; --yellow: #d29922; --blue: #58a6ff;
  --purple: #bc8cff;
}}
* {{ margin: 0; padding: 0; box-sizing: border-box; }}
body {{ background: var(--bg); color: var(--text); font-family: -apple-system, 'Segoe UI', 'Noto Sans SC', sans-serif; padding: 24px; }}
.container {{ max-width: 1200px; margin: 0 auto; }}
h1 {{ font-size: 28px; margin-bottom: 8px; }}
.subtitle {{ color: var(--text2); margin-bottom: 32px; }}
.grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 32px; }}
.card {{ background: var(--card); border: 1px solid var(--border); border-radius: 8px; padding: 20px; }}
.card .label {{ color: var(--text2); font-size: 13px; margin-bottom: 4px; }}
.card .value {{ font-size: 32px; font-weight: 700; }}
.card .value.green {{ color: var(--green); }}
.card .value.red {{ color: var(--red); }}
.card .value.yellow {{ color: var(--yellow); }}
.card .value.blue {{ color: var(--blue); }}
.section {{ background: var(--card); border: 1px solid var(--border); border-radius: 8px; padding: 24px; margin-bottom: 24px; }}
.section h2 {{ font-size: 18px; margin-bottom: 16px; }}
table {{ width: 100%; border-collapse: collapse; font-size: 14px; }}
th {{ text-align: left; padding: 10px 12px; border-bottom: 2px solid var(--border); color: var(--text2); font-weight: 600; }}
td {{ padding: 8px 12px; border-bottom: 1px solid var(--border); }}
tr:hover {{ background: rgba(88,166,255,0.05); }}
.tag {{ display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 12px; font-weight: 500; }}
.tag-hit {{ background: rgba(63,185,80,0.15); color: var(--green); }}
.tag-miss {{ background: rgba(248,81,73,0.15); color: var(--red); }}
.tag-easy {{ background: rgba(63,185,80,0.15); color: var(--green); }}
.tag-medium {{ background: rgba(210,153,34,0.15); color: var(--yellow); }}
.tag-hard {{ background: rgba(248,81,73,0.15); color: var(--red); }}
.bar-bg {{ background: var(--border); border-radius: 4px; height: 24px; position: relative; overflow: hidden; }}
.bar-fill {{ height: 100%; border-radius: 4px; display: flex; align-items: center; padding: 0 8px; font-size: 12px; font-weight: 600; color: #fff; }}
.bar-fill.high {{ background: var(--green); }}
.bar-fill.mid {{ background: var(--yellow); }}
.bar-fill.low {{ background: var(--red); }}
.scroll-table {{ max-height: 600px; overflow-y: auto; }}
.scroll-table::-webkit-scrollbar {{ width: 6px; }}
.scroll-table::-webkit-scrollbar-thumb {{ background: var(--border); border-radius: 3px; }}
</style>
</head>
<body>
<div class="container">
<h1>LocalMem 检索命中率测试报告</h1>
<p class="subtitle">生成时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')} | 种子数据: {seed_count} 条 | 查询数: {total} | 耗时: {total_time:.1f}s | Rerank: {rerank_mode}</p>

<div class="grid">
  <div class="card"><div class="label">总命中率</div><div class="value {'green' if hit_rate >= 80 else 'yellow' if hit_rate >= 60 else 'red'}">{hit_rate:.1f}%</div></div>
  <div class="card"><div class="label">命中/总数</div><div class="value blue">{hits}/{total}</div></div>
  <div class="card"><div class="label">Top-1 命中</div><div class="value green">{top1/total*100:.1f}%</div></div>
  <div class="card"><div class="label">Top-3 命中</div><div class="value green">{top3/total*100:.1f}%</div></div>
  <div class="card"><div class="label">Top-5 命中</div><div class="value green">{top5/total*100:.1f}%</div></div>
  <div class="card"><div class="label">未命中</div><div class="value red">{miss}</div></div>
</div>

<div class="section">
<h2>按查询类别分析</h2>
<table>
<thead><tr><th>类别</th><th>查询数</th><th>命中数</th><th>命中率</th><th>分布</th></tr></thead>
<tbody>"""

    for cat in cat_order:
        s = cat_stats[cat]
        r = s["hits"] / s["total"] * 100 if s["total"] > 0 else 0
        bar_class = "high" if r >= 80 else "mid" if r >= 60 else "low"
        html += f"""
<tr>
  <td>{cat_labels.get(cat, cat)}</td>
  <td>{s['total']}</td>
  <td>{s['hits']}</td>
  <td>{r:.1f}%</td>
  <td><div class="bar-bg"><div class="bar-fill {bar_class}" style="width:{max(r,2)}%">{r:.0f}%</div></div></td>
</tr>"""

    html += """
</tbody></table></div>

<div class="section">
<h2>按难度分析</h2>
<table>
<thead><tr><th>难度</th><th>查询数</th><th>命中数</th><th>命中率</th><th>分布</th></tr></thead>
<tbody>"""

    for d in ["easy", "medium", "hard"]:
        s = diff_stats[d]
        r = s["hits"] / s["total"] * 100 if s["total"] > 0 else 0
        bar_class = "high" if r >= 80 else "mid" if r >= 60 else "low"
        html += f"""
<tr>
  <td><span class="tag tag-{d}">{diff_labels[d]}</span></td>
  <td>{s['total']}</td>
  <td>{s['hits']}</td>
  <td>{r:.1f}%</td>
  <td><div class="bar-bg"><div class="bar-fill {bar_class}" style="width:{max(r,2)}%">{r:.0f}%</div></div></td>
</tr>"""

    html += """
</tbody></table></div>

<div class="section">
<h2>未命中查询详情 (""" + str(len(failures)) + """ 条)</h2>
<div class="scroll-table">
<table>
<thead><tr><th>#</th><th>查询</th><th>期望关键词</th><th>类别</th><th>难度</th><th>返回结果数</th></tr></thead>
<tbody>"""

    for f in failures:
        html += f"""
<tr>
  <td>{f['index']}</td>
  <td>{f['query']}</td>
  <td>{f['expected']}</td>
  <td>{cat_labels.get(f['category'], f['category'])}</td>
  <td><span class="tag tag-{f['difficulty']}">{diff_labels.get(f['difficulty'], f['difficulty'])}</span></td>
  <td>{f['result_count']}</td>
</tr>"""

    html += """
</tbody></table></div></div>

<div class="section">
<h2>全部查询结果</h2>
<div class="scroll-table">
<table>
<thead><tr><th>#</th><th>查询</th><th>期望</th><th>命中</th><th>排名</th><th>分数</th><th>类别</th><th>难度</th></tr></thead>
<tbody>"""

    for r in results:
        hit_tag = f'<span class="tag tag-hit">HIT</span>' if r["hit"] else f'<span class="tag tag-miss">MISS</span>'
        html += f"""
<tr>
  <td>{r['index']}</td>
  <td>{r['query']}</td>
  <td>{r['expected']}</td>
  <td>{hit_tag}</td>
  <td>{r['rank'] if r['hit'] else '-'}</td>
  <td>{f"{r['score']:.6f}" if r['score'] else '-'}</td>
  <td>{cat_labels.get(r['category'], r['category'])}</td>
  <td><span class="tag tag-{r['difficulty']}">{diff_labels.get(r['difficulty'], r['difficulty'])}</span></td>
</tr>"""

    html += f"""
</tbody></table></div></div>

<div class="section" style="text-align:center; color:var(--text2); font-size:13px;">
  <p>LocalMem Retrieval Test Report | FTS5 (BM25, simple tokenizer) | SQLite only mode | Rerank: {rerank_mode}</p>
</div>

</div></body></html>"""

    report_path = REPORT_HTML
    with open(report_path, "w", encoding="utf-8") as f:
        f.write(html)
    print(f"  报告已生成: {report_path}")

    # Also dump JSON
    json_path = REPORT_JSON
    with open(json_path, "w", encoding="utf-8") as f:
        json.dump({
            "summary": {
                "total": total,
                "hits": hits,
                "miss": miss,
                "hit_rate": round(hit_rate, 2),
                "top1_rate": round(top1/total*100, 2),
                "top3_rate": round(top3/total*100, 2),
                "top5_rate": round(top5/total*100, 2),
                "total_time_sec": round(total_time, 1),
                "seed_count": seed_count,
                "rerank_mode": rerank_mode,
            },
            "by_category": {cat: {"total": s["total"], "hits": s["hits"], "rate": round(s["hits"]/s["total"]*100, 2)} for cat, s in cat_stats.items()},
            "by_difficulty": {d: {"total": s["total"], "hits": s["hits"], "rate": round(s["hits"]/s["total"]*100, 2)} for d, s in diff_stats.items()},
            "failures": failures,
        }, f, ensure_ascii=False, indent=2)
    print(f"  JSON 数据: {json_path}")

    # Print summary
    print(f"\n{'='*60}")
    print(f"  Rerank 模式: {rerank_mode}")
    print(f"  总命中率: {hit_rate:.1f}% ({hits}/{total})")
    print(f"  Top-1: {top1/total*100:.1f}% | Top-3: {top3/total*100:.1f}% | Top-5: {top5/total*100:.1f}%")
    print(f"{'='*60}")
    print(f"  按类别:")
    for cat in cat_order:
        s = cat_stats[cat]
        r = s["hits"] / s["total"] * 100 if s["total"] > 0 else 0
        bar = "#" * int(r / 5) + "." * (20 - int(r / 5))
        print(f"    {cat_labels.get(cat, cat):8s}: [{bar}] {r:5.1f}% ({s['hits']}/{s['total']})")
    print(f"  按难度:")
    for d in ["easy", "medium", "hard"]:
        s = diff_stats[d]
        r = s["hits"] / s["total"] * 100 if s["total"] > 0 else 0
        bar = "#" * int(r / 5) + "." * (20 - int(r / 5))
        print(f"    {diff_labels[d]:4s}: [{bar}] {r:5.1f}% ({s['hits']}/{s['total']})")
    print(f"{'='*60}")


def parse_args():
    parser = argparse.ArgumentParser(description="LocalMem 500-query retrieval benchmark")
    parser.add_argument(
        "--rerank",
        default="off",
        choices=["off", "overlap", "remote"],
        help="rerank mode to send in retrieve requests and annotate in reports",
    )
    parser.add_argument(
        "--dump-dataset",
        action="store_true",
        help="print seed memories and test queries as JSON, then exit",
    )
    return parser.parse_args()


if __name__ == "__main__":
    args = parse_args()
    if args.dump_dataset:
        json.dump(
            {
                "seed_memories": SEED_MEMORIES,
                "test_queries": TEST_QUERIES,
            },
            sys.stdout,
            ensure_ascii=False,
        )
        sys.stdout.write("\n")
        raise SystemExit(0)
    run_tests(args.rerank)
