# AI Gateway

聚合多个 AI 模型 API，输出统一 OpenAI 兼容接口的单二进制网关。

## 特性

- **单二进制** — 无依赖，编译好直接跑，macOS/Linux/Windows 通吃
- **OpenAI 兼容** — 对外暴露 `/v1/chat/completions` 标准端点，Agent 无感知切换模型
- **多 Provider 支持** — OpenAI / Anthropic / Ollama / SiliconFlow / 火山方舟 等
- **高性能** — Go 原生并发，连接池复用
- **内置 Web UI** — 打开浏览器就能测试对话，SSE streaming 实时输出
- **协议转换** — Anthropic `/v1/messages` → OpenAI `/v1/chat/completions` 自动适配

## 快速开始

### 编译

```bash
git clone https://github.com/toller892/ai-gateway.git
cd ai-gateway
go build -o ai-gateway .
```

### 配置

复制 `config.yaml.example` 为 `config.yaml`，填入你的 API Key：

```bash
cp config.yaml.example config.yaml
# 编辑 config.yaml 填入真实 key
```

### 启动

```bash
./ai-gateway -config ./config.yaml
```

服务默认监听 `0.0.0.0:8080`。

### Web UI

打开 http://127.0.0.1:8080/web/ 直接测试对话。

### API 调用示例

```bash
# 非 streaming
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# streaming
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

## 项目结构

```
ai-gateway/
├── main.go                  # 入口，graceful shutdown
├── config.yaml              # Provider 配置
├── internal/
│   ├── config/
│   │   └── config.go        # YAML 配置解析
│   ├── provider/
│   │   └── provider.go      # Provider 适配接口
│   └── proxy/
│       ├── handler.go       # HTTP 路由 / 代理逻辑
│       └── web/index.html   # 内置 Web UI
└── ai-gateway              # 编译产物
```

## API 端点

| 端点 | 说明 |
|------|------|
| `GET /health` | 健康检查 |
| `GET /v1/models` | 列出所有可用模型 |
| `POST /v1/chat/completions` | OpenAI 兼容聊天接口 |
| `GET /web/` | Web UI |
| `GET /web-api/models` | Web UI 模型列表 |
| `POST /web-api/chat` | Web UI 聊天接口（SSE） |

## 适用场景

- **AI Agent** — 统一入口，动态切换后端模型
- **本地开发** — 一个端点调用多个 provider，无需改代码
- **私有部署** — 数据不出本机，支持自托管
- **模型对比** — 同一 UI 下快速切换不同模型对比效果
