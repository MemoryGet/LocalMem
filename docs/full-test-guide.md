# LocalMem 全量测试指南

## 前置条件

- Go 1.25+
- Docker 或本地安装 Qdrant（二选一）
- 有效的 `OPENAI_API_KEY`（已配置在 `.env` 中）

## 第 0 步：安装 Docker（已安装可跳过）

### macOS

```bash
# 方式 1：官方安装包（推荐）
# 下载 Docker Desktop: https://www.docker.com/products/docker-desktop/
# 打开 .dmg 拖入 Applications，启动后菜单栏出现鲸鱼图标即可

# 方式 2：Homebrew
brew install --cask docker
open /Applications/Docker.app
```

### Windows

```
1. 下载 Docker Desktop: https://www.docker.com/products/docker-desktop/
2. 运行安装程序，勾选 "Use WSL 2 instead of Hyper-V"（推荐）
3. 安装完成后重启电脑
4. 启动 Docker Desktop，等待左下角显示绿色 "Engine running"
```

前置要求：Windows 10 64-bit（Build 19041+）或 Windows 11，需开启 WSL 2：

```powershell
# 以管理员身份运行 PowerShell
wsl --install
# 重启后生效
```

### Linux (Ubuntu/Debian)

```bash
# 卸载旧版本
sudo apt-get remove docker docker-engine docker.io containerd runc 2>/dev/null

# 安装依赖
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg

# 添加 Docker 官方 GPG 密钥
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

# 添加仓库
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# 安装
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# 免 sudo 运行（可选，需重新登录生效）
sudo usermod -aG docker $USER
```

### Linux (CentOS/RHEL/Fedora)

```bash
# 安装依赖
sudo yum install -y yum-utils

# 添加仓库
sudo yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo

# 安装
sudo yum install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# 启动并设置开机自启
sudo systemctl start docker
sudo systemctl enable docker

# 免 sudo 运行（可选）
sudo usermod -aG docker $USER
```

### 验证安装

```bash
docker --version
docker run hello-world
# 看到 "Hello from Docker!" 即安装成功
```

---

## 第 1 步：启动 Qdrant

### 方式 A：Docker（推荐）

```bash
docker run -d --name qdrant-test \
  -p 6333:6333 -p 6334:6334 \
  qdrant/qdrant:v1.7.4
```

### 方式 B：二进制安装（无 Docker）

**macOS**

```bash
brew install qdrant/tap/qdrant
qdrant --config-path ./config/qdrant.yaml
# 或直接运行（使用默认配置）
qdrant
```

**Linux（x86_64）**

```bash
# 下载预编译二进制
curl -L -o qdrant.tar.gz \
  https://github.com/qdrant/qdrant/releases/download/v1.7.4/qdrant-x86_64-unknown-linux-musl.tar.gz
tar -xzf qdrant.tar.gz
chmod +x qdrant

# 启动（默认监听 6333/6334）
./qdrant
```

**Linux（ARM64 / Apple Silicon Linux）**

```bash
curl -L -o qdrant.tar.gz \
  https://github.com/qdrant/qdrant/releases/download/v1.7.4/qdrant-aarch64-unknown-linux-musl.tar.gz
tar -xzf qdrant.tar.gz
chmod +x qdrant
./qdrant
```

**Windows**

```powershell
# 下载: https://github.com/qdrant/qdrant/releases/download/v1.7.4/qdrant-x86_64-pc-windows-msvc.zip
# 解压后运行:
.\qdrant.exe
```

**从源码编译（需要 Rust 工具链）**

```bash
git clone https://github.com/qdrant/qdrant.git
cd qdrant
cargo build --release
./target/release/qdrant
```

### 验证服务就绪

```bash
curl http://localhost:6333/readyz
# 预期返回: OK
```

## 第 2 步：修改 config.yaml

将 `storage.qdrant.enabled` 改为 `true`（第 16 行）：

```yaml
qdrant:
  enabled: true          # false → true
  url: "http://localhost:6333"
  collection: "memories"
  dimension: 384
```

其余配置无需改动。Embedding 使用 `text-embedding-3-small`（384 维），与 Qdrant dimension 匹配。

## 第 3 步：运行测试

```bash
# 全量测试
go test ./testing/... -count=1 -v

# 按模块测试
go test ./testing/store/...    -count=1 -v   # 存储层（SQLite + Qdrant）
go test ./testing/search/...   -count=1 -v   # 检索（FTS + 向量 + 图谱 + RRF）
go test ./testing/api/...      -count=1 -v   # API 集成
go test ./testing/memory/...   -count=1 -v   # 记忆管理（CRUD、生命周期）
go test ./testing/mcp/...      -count=1 -v   # MCP 服务器
go test ./testing/reflect/...  -count=1 -v   # 反思引擎
go test ./testing/document/... -count=1 -v   # 文档处理
go test ./testing/eval/...     -count=1 -v   # 评估框架

# 单个测试
go test -run TestXxx ./testing/... -count=1 -v
```

## 第 4 步：生成测试报告（可选）

```bash
go test ./testing/report/ -v -count=1
# 报告输出 → testing/report/report.html
```

## 第 5 步：清理

```bash
# 停止并删除 Qdrant 容器
docker stop qdrant-test && docker rm qdrant-test

# 恢复配置（如不需要常驻）
# config.yaml 中 qdrant.enabled 改回 false
```

## 配置速查

| 组件 | 配置项 | 当前值 | 全量测试值 |
|------|--------|--------|-----------|
| Qdrant | `storage.qdrant.enabled` | `false` | `true` |
| Embedding | `llm.embedding.model` | `text-embedding-3-small` | 无需改动 |
| Dimension | `storage.qdrant.dimension` | `384` | 无需改动 |
| SQLite | `storage.sqlite.enabled` | `true` | 无需改动 |
| OPENAI_API_KEY | `.env` | 已配置 | 无需改动 |
