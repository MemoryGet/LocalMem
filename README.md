# IClude 企业级AI记忆体

## 项目简介
IClude 是一个企业级AI记忆体系统，提供多租户隔离、版本管理、增量更新等核心功能。

## 目录结构

```
iclude/
├── services/                     # 服务入口
│   ├── api-gateway/              # API网关服务
│   └── memory-core/              # 记忆核心服务
├── business/                     # 业务逻辑
│   ├── config/                   # 配置管理
│   ├── handlers/                 # HTTP接口处理
│   ├── logic/                    # 核心业务逻辑
│   ├── repository/               # 数据访问层
│   ├── models/                   # 数据模型定义
│   └── middlewares/              # 中间件
├── components/                   # 公共组件
│   ├── clients/                  # 外部服务客户端
│   ├── utils/                    # 工具函数
│   └── errors/                   # 错误定义
├── sdks/                         # 多语言SDK
│   ├── go/                       # Go SDK
│   ├── python/                   # Python SDK
│   └── nodejs/                   # Node.js SDK
├── deploy/                       # 部署配置
├── documentation/                # 文档
│   ├── api-reference/            # API参考文档
│   ├── user-guide/               # 用户指南
│   └── developer-docs/           # 开发者文档
└── testing/                      # 测试用例
    ├── unit-tests/               # 单元测试
    ├── integration-tests/        # 集成测试
    └── performance-tests/        # 性能测试
```

## 快速开始

### 环境要求
- Go 1.21+
- PostgreSQL 16+
- Milvus 2.3+
- Elasticsearch 8.x
- Redis 7.x

### 安装依赖
```bash
go mod download
```

### 运行服务
```bash
# 启动API网关
cd services/api-gateway
 go run main.go

# 启动记忆核心服务
cd services/memory-core
 go run main.go
```

## 开发规范

### 代码风格
- Go: 使用 `go fmt` 格式化
- Python: 遵循 PEP8 规范
- Node.js: 遵循 Airbnb JavaScript 风格指南

### 测试规范
- 单元测试覆盖率 ≥ 95%
- 集成测试覆盖所有场景
- 性能测试: 批量存储 QPS ≥ 100, 语义检索 QPS ≥ 200

## 文档

- [API参考文档](documentation/api-reference/)
- [用户指南](documentation/user-guide/)
- [开发者文档](documentation/developer-docs/)

## 许可证

MIT