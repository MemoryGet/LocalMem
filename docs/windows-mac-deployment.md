# LocalMem 部署清单：Windows 基础设施 + Mac 运行 iclude

> **架构说明**
> - **Windows**：运行所有重型基础设施服务（LLM、向量库、分词、文档解析）
> - **Mac**：编译并运行 iclude 本体，通过网络连接 Windows 上的服务
> - **前提**：两台机器在同一局域网，或通过 VPN 互通

---

## 第一部分：Windows 环境部署

### 1. 安装 Docker Desktop

1. 前往 https://www.docker.com/products/docker-desktop 下载 Windows 版
2. 安装完成后启动 Docker Desktop，等待状态栏图标变为绿色（Running）
3. 验证：

```powershell
docker version
docker run hello-world
```

---

### 2. 安装 Ollama（本地 LLM + Embedding）

1. 前往 https://ollama.com 下载 Windows 安装包，按提示安装
2. 安装后 Ollama 自动在后台运行，监听 `127.0.0.1:11434`
3. **修改监听地址为 0.0.0.0**（允许 Mac 访问）：

   - 打开「系统环境变量」→ 新建系统变量：
     - 变量名：`OLLAMA_HOST`
     - 变量值：`0.0.0.0`
   - 重启 Ollama（任务栏右键退出后重新启动）

4. 拉取所需模型：

```powershell
# 推理模型（选一个，按显存决定）
ollama pull qwen2.5:7b        # 需要约 8G 显存
ollama pull qwen2.5:3b        # 需要约 4G 显存（显存不足时用这个）

# Embedding 模型
ollama pull bge-m3            # 多语言 embedding，支持中英文
```

5. 验证：

```powershell
curl http://localhost:11434/api/tags
```

---

### 3. 启动 Qdrant（向量数据库）

```powershell
docker run -d `
  --name qdrant `
  --restart unless-stopped `
  -p 6333:6333 `
  -v qdrant_storage:/qdrant/storage `
  qdrant/qdrant:v1.7.4
```

验证（浏览器打开或 curl）：

```powershell
curl http://localhost:6333/readyz
# 返回 "ok" 即正常
```

---

### 4. 启动 Jieba 分词服务

> 需要 Python 3.10+。若未安装，前往 https://www.python.org 下载。

```powershell
pip install jieba flask

# 进入项目目录（从 Mac 拷贝 tools/jieba_server.py 到 Windows，或直接在 Windows 克隆仓库）
python tools/jieba_server.py
# 监听 :8866，保持此窗口运行
```

验证：

```powershell
curl http://localhost:8866/tokenize -X POST -H "Content-Type: application/json" -d "{\"text\":\"深度学习模型\"}"
```

---

### 5. 启动 Apache Tika（文档解析，可选）

> 仅需要文档摄入功能（上传 PDF/DOCX 等）时启动。

```powershell
docker run -d `
  --name tika `
  --restart unless-stopped `
  -p 9998:9998 `
  apache/tika:latest
```

验证：

```powershell
curl http://localhost:9998/tika
```

---

### 6. 启动 Docling（文档解析 + OCR，可选）

> 比 Tika 更强（支持 OCR），但镜像较大、启动较慢，内存至少 4G。

```powershell
docker run -d `
  --name docling `
  --restart unless-stopped `
  -p 5001:5001 `
  -e DOCLING_BACKEND=dlparse_v2 `
  -e DOCLING_OCR_ENGINE=easyocr `
  --memory 4g `
  quay.io/docling-project/docling-serve:latest
```

验证：

```powershell
curl http://localhost:5001/health
```

---

### 7. 启动 BGE-Reranker（检索重排，可选）

> 提升检索结果排序质量，首次启动需下载 HuggingFace 模型（约 1.1G），需等待。

在项目根目录执行：

```powershell
docker compose -f deploy/docker-compose.yml --profile reranker up -d reranker
```

验证：

```powershell
curl http://localhost:8868/healthz
```

---

### 8. Windows 防火墙放行端口

以**管理员身份**打开 PowerShell，逐条执行：

```powershell
# Ollama
netsh advfirewall firewall add rule name="Ollama" dir=in action=allow protocol=TCP localport=11434

# Qdrant
netsh advfirewall firewall add rule name="Qdrant" dir=in action=allow protocol=TCP localport=6333

# Jieba
netsh advfirewall firewall add rule name="Jieba" dir=in action=allow protocol=TCP localport=8866

# Tika（可选）
netsh advfirewall firewall add rule name="Tika" dir=in action=allow protocol=TCP localport=9998

# Docling（可选）
netsh advfirewall firewall add rule name="Docling" dir=in action=allow protocol=TCP localport=5001

# BGE-Reranker（可选）
netsh advfirewall firewall add rule name="Reranker" dir=in action=allow protocol=TCP localport=8868
```

查看 Windows 机器 IP（后续 Mac 配置需要）：

```powershell
ipconfig
# 记录「以太网适配器」或「WLAN」下的 IPv4 地址，例如 192.168.1.100
```

---

### Windows 部署完成检查表

```
[ ] Docker Desktop 正在运行（状态栏绿色）
[ ] Ollama 已启动，OLLAMA_HOST=0.0.0.0，模型已拉取
[ ] Qdrant 容器运行中，:6333 可访问
[ ] Jieba server 运行中，:8866 可访问
[ ] Tika 容器运行中，:9998 可访问（如需文档摄入）
[ ] Docling 容器运行中，:5001 可访问（如需文档摄入）
[ ] 防火墙规则已添加
[ ] 已记录 Windows 机器 IP
```

---

## 第二部分：Mac 环境部署

### 1. 安装 Go 1.25+

```bash
# 推荐使用 Homebrew
brew install go

# 验证
go version   # 应显示 go1.25 或更高
```

---

### 2. 获取代码并安装依赖

```bash
git clone <仓库地址> LocalMem
cd LocalMem
go mod download
```

---

### 3. 编译

```bash
make build
# 产物：dist/iclude-mcp 和 dist/iclude-cli
```

---

### 4. 配置 config.yaml

复制标准模板并修改：

```bash
cp config/templates/config-standard.yaml config.yaml
```

编辑 `config.yaml`，将所有服务地址改为 Windows 机器 IP（以 `192.168.1.100` 为例）：

```yaml
storage:
  sqlite:
    enabled: true
    path: "./data/iclude.db"
    tokenizer:
      provider: jieba
      jieba_url: "http://192.168.1.100:8866"   # Windows Jieba
  qdrant:
    enabled: true
    url: "http://192.168.1.100:6333"            # Windows Qdrant
    collection: "memories"
    dimension: 1024                              # bge-m3 的维度

llm:
  default_provider: openai
  openai:
    api_key: "ollama"                           # Ollama 不校验 key，填任意字符串
    base_url: "http://192.168.1.100:11434/v1"  # Windows Ollama
    model: "qwen2.5:7b"
  embedding:
    provider: openai
    model: "bge-m3"

document:
  enabled: true
  docling:
    url: "http://192.168.1.100:5001"           # Windows Docling（如已启动）
  tika:
    url: "http://192.168.1.100:9998"           # Windows Tika（如已启动）
```

---

### 5. 启动 iclude

```bash
# API 服务（端口 8080）
go run ./cmd/server/

# 或使用编译后的二进制
./dist/iclude-cli --config config.yaml

# MCP 服务（端口 8081，可选）
go run ./cmd/mcp/
```

---

### 6. 验证连通性

```bash
# 检查 iclude 本身
curl http://localhost:8080/v1/memories

# 检查 Qdrant 连通（从 Mac 访问 Windows）
curl http://192.168.1.100:6333/readyz

# 检查 Ollama 连通
curl http://192.168.1.100:11434/api/tags

# 检查 Jieba 连通
curl http://192.168.1.100:8866/tokenize \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"text":"深度学习模型"}'
```

---

### 7. 写入一条记忆测试全链路

```bash
curl -X POST http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -d '{
    "content": "LocalMem 部署测试，Windows 提供基础设施，Mac 运行 iclude",
    "kind": "episodic",
    "source_type": "manual",
    "team_id": "test-team",
    "owner_id": "test-user"
  }'
```

返回带 `id` 的 JSON 即表示写入成功（SQLite + Qdrant 双写均已完成）。

---

### Mac 部署完成检查表

```
[ ] Go 1.25+ 已安装
[ ] go mod download 完成，无报错
[ ] make build 成功
[ ] config.yaml 中所有服务地址已改为 Windows IP
[ ] go run ./cmd/server/ 启动无错误日志
[ ] curl localhost:8080/v1/memories 返回正常
[ ] 写入测试记忆成功
```

---

## 附：服务端口速查

| 服务 | 机器 | 端口 | 必须 |
|------|------|------|------|
| iclude API | Mac | 8080 | ✅ |
| iclude MCP | Mac | 8081 | 按需 |
| Ollama | Windows | 11434 | ✅ |
| Qdrant | Windows | 6333 | ✅ |
| Jieba | Windows | 8866 | ✅（中文内容） |
| Tika | Windows | 9998 | 文档摄入时 |
| Docling | Windows | 5001 | 文档摄入时 |
| BGE-Reranker | Windows | 8868 | 可选 |
