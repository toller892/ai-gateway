# AI Gateway

聚合多个 AI 模型 API，输出统一 OpenAI 兼容接口的单二进制网关。

## 特性

- **单二进制** — 9MB，无依赖，编译好直接跑，macOS/Linux/Windows 通吃
- **OpenAI 兼容** — 对外暴露 `/v1/chat/completions` 标准端点，Agent 无感知切换模型
- **多 Provider 支持** — OpenAI / Anthropic / Ollama / SiliconFlow / 火山方舟 等
- **协议自适应** — 配置里显式指定 `type: openai` 或 `type: anthropic`，不再猜模型名前缀
- **入站鉴权** — 支持 Bearer Token，不开放到公网也能安全部署
- **高性能** — Go 原生并发，连接池复用，10MB 请求体限制
- **内置 Web UI** — 打开浏览器就能测试对话，SSE streaming 实时输出，支持 GLM thinking 模式
- **模型别名** — `fast` → `gpt-4o-mini`，Agent 调用更简单

## 快速开始

### 方式一：Docker（推荐）

```bash
# 1. 准备配置
cp config.yaml.example config.yaml
# 编辑 config.yaml 填入真实 API Key

# 2. 本地构建镜像
docker-compose up -d

# 或手动构建
docker build -t ai-gateway .
docker run -d \
  -p 8080:8080 \
  -v ./config.yaml:/app/config/config.yaml:ro \
  ai-gateway
```

> **Note**: GHCR 镜像 `ghcr.io/toller892/ai-gateway:latest` 将在 CI 配置完成后可用。

### 方式二：二进制

```bash
# 下载 release
curl -L https://github.com/toller892/ai-gateway/releases/latest/download/ai-gateway-darwin-arm64 -o ai-gateway
chmod +x ai-gateway

# 准备配置
cp config.yaml.example config.yaml
# 编辑 config.yaml

# 运行
./ai-gateway -config ./config.yaml
```

### 方式三：源码编译

```bash
git clone https://github.com/toller892/ai-gateway.git
cd ai-gateway
go build -o ai-gateway .
```

### Web UI

打开 http://127.0.0.1:8080/web/ 直接测试对话。

### Agent 接入示例

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="sk-gateway-local"  # 网关鉴权 token
)

# 方式1：直接用模型名
resp = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "hello"}],
    stream=True
)

# 方式2：用模型别名（推荐）
resp = client.chat.completions.create(
    model="fast",  # 配置里定义的别名
    messages=[{"role": "user", "content": "hello"}]
)
```

## 配置示例

```yaml
server:
  auth_tokens:
    - "sk-gateway-local"  # 入站鉴权
  max_body_size: 10485760   # 10MB
  timeouts:
    connect: 10
    request: 300

providers:
  openai:
    type: openai  # 显式指定协议类型
    api_key: "sk-..."
    base_url: "https://api.openai.com/v1"
    models:
      - "gpt-4o"
      - "gpt-4o-mini"

  anthropic:
    type: anthropic
    api_key: "sk-ant-..."
    base_url: "https://api.anthropic.com"
    models:
      - "claude-sonnet-4-5"

  ark:
    type: anthropic  # 火山方舟兼容 Anthropic 协议
    api_key: "xxx"
    base_url: "https://ark.cn-beijing.volces.com/api/coding/v1"
    models:
      - "glm-5.1"

# 模型别名
aliases:
  fast:
    provider: openai
    model: gpt-4o-mini
  strong:
    provider: anthropic
    model: claude-sonnet-4-5
```

## API 端点

| 端点 | 说明 |
|------|------|
| `GET /health` | 健康检查 |
| `GET /v1/models` | 列出可用模型 |
| `POST /v1/chat/completions` | OpenAI 兼容聊天接口 (支持 stream) |
| `GET /web/` | Web UI |
| `GET /web-api/models` | 模型列表（含 provider 来源） |
| `POST /web-api/chat` | Web UI 聊天接口（SSE） |

## 技术细节

- **路由逻辑**：配置显式 `type`，不再猜模型名前缀
- **Streaming**：同时检查 body `stream: true` 和 `Accept` 头
- **协议转换**：Anthropic `/v1/messages` → OpenAI `/v1/chat/completions` 自动适配
- **错误处理**：统一返回 OpenAI 格式错误

## 生产部署建议

1. **开启鉴权**：配置 `server.auth_tokens`
2. **设置超时**：根据模型调整 `server.timeouts`
3. **使用 HTTPS**：网关前加 Nginx 或 Cloudflare
4. **监控**：配置 Prometheus 采集（TODO）

## 授权

MIT License