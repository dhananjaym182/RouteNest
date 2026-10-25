<div align="center">

# RouteNest

**一个端点，多个服务商，你的模型。**

RouteNest 是一个自托管、Go 驱动的 AI 网关，面向编码代理与 AI 应用。通过单一端点，将 OpenAI 与 Anthropic 兼容请求路由到已配置的服务商 API、本地模型运行时以及受支持的账号集成。

它提供服务商预设、账号管理、模型发现、有序回退、请求日志与内置管理控制台。

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue)](#从源码构建)
[![Provider presets](https://img.shields.io/badge/Provider%20presets-110-brightgreen)](#服务商覆盖)

**English:** [English README](README.md)

</div>

---

## RouteNest 提供的能力

- **多账号路由** — 选择并轮换已配置的账号，具备冷却与可重试错误处理。
- **服务商目录** — 110 个服务商预设定义，并支持自定义 OpenAI 兼容端点。可用性与协议支持因服务商而异。
- **多种 API 格式** — OpenAI Chat Completions、受支持的 Responses API 功能，以及 Anthropic Messages 端点。
- **模型组合** — 定义有序模型列表，在符合条件的上游失败时回退。
- **模型发现** — 同步受支持的模型目录，配置自定义模型 ID，并选择默认模型。
- **管理控制台** — 管理账号、连接、服务商、模型、组合、API 密钥、代理与请求日志。
- **出口代理池** — 为受支持的上游连接配置出站代理。
- **请求可观测性** — 查看请求日志、延迟、token 用量与可用的服务商统计。
- **灵活部署** — 运行 Go 二进制、使用桌面应用，或通过 Docker 部署。

服务商可用性、认证要求、免费额度与支持的功能取决于上游服务。预设只是配置模板，并不代表已经过测试的集成；收录预设并不保证每个模型都经过测试，也不保证每个账号都可用。

## 部署前安全须知

> **安全提示：** RouteNest 默认没有管理密码，也不内置 TLS。在未设置密码的实例上，任何能访问 `/admin/` 的人都可以添加或导出账号、读取已保存的凭据、修改路由并生成 API 密钥。把 RouteNest 暴露到局域网或互联网之前，请先设置管理密码并限制网络访问。

- **监听地址：** 二进制与桌面端默认绑定 `127.0.0.1`。Docker 镜像在容器内绑定 `0.0.0.0`，因此映射出去的端口在容器外部可达。
- **两套独立的控制：** API 密钥只保护 `/v1/*` 代理端点，**不会**保护 `/admin/*`。管理后台由管理密码及其会话 Cookie 控制（24 小时过期，保存在内存中，进程重启后失效）。仅设置 API 密钥不能保护管理控制台。
- **无 TLS：** RouteNest 使用明文 HTTP。流量经过不可信网络时，请在前面部署负责 TLS 终止的反向代理。
- **敏感数据：** 账号 refreshToken、服务商密钥与生成的 API 密钥以明文保存在数据目录中。请保护该目录，不要提交到 Git 或分享。
- **Docker：** 不要把 `3457` 端口公开到公网。请使用防火墙限制来源，或绑定到本机地址。

## 快速开始

### 桌面端（预编译版本）

从 [Releases](https://github.com/dhananjaym182/RouteNest/releases) 下载对应平台的桌面端构建并运行。

| 平台 | 运行发布版本所需条件 |
| --- | --- |
| Windows x64 | Microsoft Edge WebView2 运行时。Windows 11 已包含；保持更新的 Windows 10 通常也有。若应用提示缺少 WebView2，请安装 Microsoft 的 Evergreen Runtime。 |
| macOS Apple Silicon | 受支持的 macOS 版本（内置 WKWebView）。运行发布版本不需要 Xcode 工具。官方发布仅提供 arm64 版本；Intel Mac 可从源码构建。构建未公证，Gatekeeper 可能要求右键选择 **打开**，或在 **系统设置 → 隐私与安全性** 中放行。 |
| Linux x64 | GTK 3 与 WebKitGTK 4.0 运行时库，与官方构建匹配。运行不需要开发头文件。 |

Windows SmartScreen 可能因为构建未做代码签名而警告。只有在文件来自官方发布页时才选择 **更多信息 → 仍要运行**。

### 运行二进制

构建需要 Go 1.25 或更高版本：

```bash
go build -o routenest .
./routenest                # 127.0.0.1:3457
./routenest -port 8080     # 自定义端口
./routenest -host 0.0.0.0  # 所有网卡；请先阅读上方安全提示
```

打开 <http://127.0.0.1:3457/admin/> 进入管理控制台。监听地址也可以通过 `CLINE_PROXY_HOST` 设置。

### Docker

```bash
docker compose up -d
docker compose logs -f
docker compose down
```

容器监听 `0.0.0.0:3457`。状态存放在 `/app/data`，compose 已挂载到 `./data`；只要保留 `./data`，重建容器不会丢数据。compose 还会以只读方式挂载 `./override.md`；如需覆盖 System Prompt，请先创建该文件再执行 `docker compose up`。

### 首次运行

1. 打开 `/admin/`，设置管理密码。
2. 添加 Cline 账号、OpenCode Zen 连接，或服务商 API 密钥。
3. 为客户端生成 API 密钥。
4. 选择默认模型；不设置时使用第一个可用的免费模型。

## 客户端配置

两种协议共用同一端口（默认 `3457`）。API 密钥可以通过 `Authorization: Bearer <key>` 或 `x-api-key: <key>` 传入。

```bash
export ROUTENEST_KEY="<在控制台生成的密钥>"
curl http://127.0.0.1:3457/v1/models -H "Authorization: Bearer $ROUTENEST_KEY"
```

使用 `/v1/models` 返回的模型 ID，或已启用模型组合的名称。

### OpenAI 兼容

```bash
curl http://127.0.0.1:3457/v1/chat/completions \
  -H "Authorization: Bearer $ROUTENEST_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<模型或组合名>","messages":[{"role":"user","content":"hi"}]}'
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3457/v1",
    api_key="<在控制台生成的密钥>",
)
```

### Anthropic 兼容

```bash
curl http://127.0.0.1:3457/v1/messages \
  -H "x-api-key: $ROUTENEST_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"<模型或组合名>","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}'
```

让 Claude Code 指向 RouteNest：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:3457
export ANTHROPIC_AUTH_TOKEN="$ROUTENEST_KEY"
```

## API 兼容性

RouteNest 为受支持的请求模式提供 OpenAI 与 Anthropic 兼容端点。兼容性属于实现层面，不保证每个字段、服务商扩展或模型功能都可用。

| 端点 | 格式 | 说明 |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions | 流式与非流式；在上游提供时支持工具、reasoning 字段与 usage 元数据 |
| `POST /v1/responses` | OpenAI Responses | 转换为上游 chat-completions 调用；支持文本、工具、reasoning 内容、流式事件与 usage |
| `POST /v1/messages` | Anthropic Messages | 流式与非流式；工具调用/结果、thinking 内容与 usage |
| `POST /v1/messages/count_tokens` | Anthropic token 计量 | 本地估算，不是上游分词器 |
| `GET /v1/models` | OpenAI 模型列表 | 已启用的模型与已启用的组合 |
| `GET /v1/health` | 健康检查 | 版本与活跃账号数 |

已验证的行为包括：文本、工具调用与 reasoning 内容的 SSE 流式转发；OpenAI 与 Anthropic 工具定义/工具结果的双向转换；上游返回时透传 usage 元数据；Responses API 事件序列；流被截断或客户端断开时记录失败。

已知限制：

- 协议转换目前面向文本、工具调用与 thinking/reasoning 内容。非文本内容块（例如图片）不会在协议之间转换。
- `response_format` 等 OpenAI 字段会转发给 OpenAI 兼容上游，但 RouteNest 不会跨服务商规范化结构化输出行为。
- Responses API 通过转换为 chat completions 实现；无法用该形式表达的行为不受支持。
- `count_tokens` 返回保守的本地估算，不是服务商的精确 token 数。
- 上游模型、认证方式与服务商特定功能可能随时变化。

## 账号与上游

### Cline 账号（非官方）

RouteNest 可以通过 Cline 的设备授权流程、导入 refreshToken 或批量导入来添加 Cline 账号，并在账号池中轮换。该集成是**非官方的，未得到 Cline 支持或认可**；它会在 Cline 客户端之外使用 Cline 账号凭据与 Cline API。

Cline 文档说明：Cline API 不支持免费模型，免费模型仅限 Cline IDE 扩展和 CLI。RouteNest 会读取 Cline 的推荐模型接口（包含 free、Cline Pass 与 recommended 分组），但通过 RouteNest 使用免费模型或 Cline Pass 并非官方支持路径，可能随时失效或被限制。使用前请阅读 [Cline 免费模型文档](https://docs.cline.bot/getting-started/free-models) 与 [服务条款](https://cline.bot/tos)。

### OpenCode Zen

针对 OpenCode Zen 免费模型的独立集成。付费 Zen 模型会被拒绝，不会转发。Zen 拥有自己的出口代理池与冷却处理。

### Qoder

除 Cline 账号外，也支持 Qoder 设备登录连接。

### 服务商 API 与本地运行时

可以把任意 OpenAI 兼容端点添加为自定义服务商，并配置 Base URL、密钥、模型 ID、限额与出口代理。本地运行时（Ollama、vLLM、LM Studio、llama.cpp 等）无需密钥；回环与内网目标会绕过出口代理。

## 服务商覆盖

随附注册表包含 **110 个服务商预设定义**：

| 来源 | 数量 | 说明 |
| --- | --- | --- |
| 内置预设 | 17 | OpenRouter、Groq、Cerebras、Google AI Studio、Mistral、Together AI、DeepSeek、OpenAI、vLLM、Ollama、Kilo Code、GitHub Copilot、Qwen Chat、Kimi for Coding、CodeBuddy 国内/国际、WorkBuddy |
| 导入预设 | 93 | 来源于 ExtremeRouter 服务商注册表；包含免费、免费额度、带免费额度的 API Key 与本地运行时条目 |
| **合计** | **110** | **其中 35 个带有 `freeTier` 服务商级提示** |

`freeTier` 与 `kind` 是注册表中的展示/分组提示，不是按模型授予的访问权限。预设包含种子模型 ID，以及（在定义时）目录抓取器。注册表条目并不表示服务当前可访问、账号有权使用某个模型，或服务商特定功能可用。

认证方式取决于服务商：

- 大多数托管服务商使用 API 密钥。
- 部分预设使用浏览器或设备登录（例如 GitHub Copilot、Kilo Code、Kimi for Coding、Qwen Chat、CodeBuddy、WorkBuddy）。
- 本地运行时无需认证。
- Cline 账号凭据与 OpenCode Zen 连接用于对应集成。

只有部分预设与请求模式经过仓库测试和实际部署验证。请把目录当作起点，在依赖某个服务商前先用一个小请求验证。

## 模型发现

- RouteNest 在启动时拉取 Cline 推荐模型列表，每小时重新同步，并可在控制台按需触发。
- 带目录抓取器的预设可以刷新自己的模型或价格列表。
- 不在任何目录中的端点可以手动添加模型 ID。
- 未配置默认模型时，会回退到第一个可用的免费模型。
- 可选的「仅免费」模式会拒绝未被归类为免费的模型；该分类属于尽力而为。

## 路由与组合

1. 客户端把模型名或组合名发到某个 `/v1/*` 端点。
2. RouteNest 把模型解析到 Cline 账号池、OpenCode Zen 上游，或服务商预设/自定义服务商。
3. 账号池按 `round_robin`、`fill` 或 `random` 选择账号。命中 `402`/`429` 的账号按模型冷却并自动恢复。
4. 遇到符合条件的上游失败时，RouteNest 会尝试其他账号或服务商；对于组合，则按配置顺序进入下一个条目。
5. 响应按入站协议以 SSE 转发：OpenAI 客户端收到 OpenAI 分块，Anthropic 客户端收到 Anthropic 事件。

组合顺序是唯一权威：不健康的条目会被跳过，而不会被重排。回退到全局免费链属于显式开启的应急行为，并在控制台中标注。会话亲和与模型别名均可选配置。

## 管理控制台

`/admin/` 管理整个网关：

- 仪表盘、请求量与服务商健康度。
- 账号池、连接、轮换策略与导入/导出。
- 服务商预设、自定义服务商、密钥、Base URL、冷却与配额。
- 模型目录、自定义模型、默认模型与模型测试。
- 模型组合与有序回退。
- 请求日志，包括延迟、token、TPS、上游与模型。
- API 密钥、监听地址、管理密码、出口代理与运行时调优。

界面支持中英双语，跟随浏览器语言，可在侧栏切换。桌面宽度下侧栏可收起为图标轨道；`Ctrl+B` 切换。

## 出口代理

面向有区域限制或限流的上游：

- **Cline 出口代理池** — 用于对话、登录/令牌刷新与模型同步。
- **OpenCode Zen 出口代理池** — 独立代理池，限流出口会自动冷却。

优先级：应用内代理池 → 环境变量代理（`HTTPS_PROXY`）→ 直连。回环与内网目标始终直连。

## 数据与升级

数据按以下顺序查找：可执行文件目录、工作目录、`~/.cline2api/`。设置 `CLINE2API_DATA_DIR` 可把所有数据文件固定到一个目录。

| 文件 | 用途 |
| --- | --- |
| `.cline-accounts.json` | 账号池、API 密钥、模型设置、管理密码哈希 |
| `.cline-config.json` | 代理配置、别名、策略与模型组合 |
| `.cline-connections.json` | 非 Cline 连接元数据（Zen、Qoder 等） |
| `.cline-credentials.json` | 已保存的连接凭据 |
| `.cline-providers.json` | 自定义服务商 |
| `.cline-providers-cooldowns.json` | 服务商冷却状态 |
| `.cline-providers-sync.json` | 服务商目录同步状态 |
| `.cline-proxy.json` | Cline 出口代理池 |
| `.cline-zen.json` | OpenCode Zen 配置与出口代理池 |
| `.cline-request-logs.json` | 请求日志 |
| `override.md` | 可选的 System Prompt 覆盖，从工作目录读取 |

设置 `CLINE2API_DATA_DIR` 后，首次使用时会把可执行文件目录或工作目录中的旧数据文件复制到该目录，前提是目标文件尚不存在。`.cline-` 文件名被有意保留，以便旧部署原地升级。

> 数据文件包含明文 refreshToken、服务商密钥与生成的 API 密钥。请安全备份，不要公开，也不要提交到 Git。

## 从源码构建

CLI 二进制需要 Go 1.25 或更高版本。构建桌面端还需要下面列出的平台工具链。

### 二进制

```bash
go build -o routenest .
```

### 桌面端

Wails 使用各平台原生 WebView，需在目标系统上构建：

```bash
# Windows：Go + C 工具链；运行需要 WebView2 运行时
./desktop/build.sh

# macOS：Xcode Command Line Tools
xcode-select --install
./desktop/build.sh

# Linux：GTK 3 与 WebKitGTK 开发包，例如
# Debian/Ubuntu（按发行版使用 4.0 或 4.1）
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
# Fedora: sudo dnf install gtk3-devel webkit2gtk4.1-devel
# Arch:   sudo pacman -S gtk3 webkit2gtk
./desktop/build.sh
```

开发包仅编译时需要。运行发布版本的用户需要对应的运行时库，而不是头文件。

## 项目结构

```
main.go, desktop_main.go   CLI 与桌面端入口
proxy.go, responses.go     HTTP 路由、协议桥接、SSE
combos.go, providers.go    模型组合、预设与服务商派发
admin*.go                  管理 REST API
adminui/                   内嵌管理控制台
internal/                  配置、协议适配器、上游客户端、存储
desktop/                   Wails 外壳与构建脚本
Dockerfile, docker-compose.yml
```

## 署名与许可证

RouteNest 是 [Cline2API](https://github.com/luawei1/cline2api) 的独立分支，原作者为 luawei1，以 MIT 许可证分发。上游版权声明保留在 [LICENSE](LICENSE) 中。

93 个导入的服务商预设定义来源于 ExtremeRouter 服务商注册表（MIT）。相应的版权与许可证声明保留在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 中。

RouteNest 与 Cline、OpenCode、Qoder 以及目录中列出的任何服务商均无关联，也未获得其认可。请仅使用你有权使用的账号与凭据，并遵守各服务商的服务条款。服务商名称与商标归其各自所有者所有。

## 许可证

[MIT](LICENSE)
