<div align="center">

# RouteNest

**一个端点，多个服务商，你的模型。**

RouteNest 是一个自托管、Go 驱动的 AI 网关，面向编码代理与 AI 应用。通过单一端点，将 OpenAI 与 Anthropic 兼容请求路由到已配置的服务商 API、本地模型运行时以及受支持的账号集成。

它提供服务商预设、账号管理、模型发现、有序回退、请求日志与内置管理控制台。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue)](#从源码构建)
[![Provider definitions](https://img.shields.io/badge/Provider%20definitions-111-brightgreen)](#服务商覆盖)

**English:** [English README](README.md)

</div>

---

## RouteNest 提供的能力

拟议的契约权威、术语与尚未解决的行为决策见 [CONTRACTS.md](CONTRACTS.md)，等待评审。历史计划与验证报告属于带日期的证据，不代表当前测试结果。

- **多账号路由** - 选择并轮换已配置的账号，具备冷却与可重试错误处理。
- **服务商目录** - 111 个内置 Provider Definition，其中 109 个是可选接入预设，并支持自定义 OpenAI 兼容端点。可用性与协议支持因服务商而异。
- **多种 API 格式** - OpenAI Chat Completions、受支持的 Responses API 功能，以及 Anthropic Messages 端点。
- **模型组合** - 定义有序模型列表，在符合条件的上游失败时回退。
- **模型发现** - 同步受支持的模型目录，配置自定义模型 ID，并选择默认模型。
- **管理控制台** - 管理账号、连接、服务商、模型、组合、客户端 API 密钥、代理与请求日志。
- **出口代理池** - 配置共享的 Cline/默认出口池、原生 Zen 出口池与自定义 Provider 覆盖；不宣称提供通用 Profile 绑定界面。
- **请求可观测性** - 查看请求日志、延迟、token 用量与可用的服务商统计。
- **灵活部署** - 运行 Go 二进制、使用桌面应用，或通过 Docker 部署。

服务商可用性、认证要求、免费额度与支持的功能取决于上游服务。预设只是配置模板，并不代表已经过测试的集成；收录预设并不保证每个模型都经过测试，也不保证每个账号都可用。

## 部署前安全须知

> **安全提示：** RouteNest 不提供通用默认密码，也不允许匿名执行管理操作。全新安装进入一次性引导模式，必须设置永久本地管理员密码后才能正常管理。静态管理界面外壳以及少量状态、引导、登录与 SSO 协议入口为公开路由，但不会授予管理权限。

- **一次性引导：** CLI 只把随机引导密钥写到本地控制终端。首次启动没有控制终端时需设置 `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD`；只要已初始化的持久身份状态仍保留，后续启动就会忽略该变量。桌面端通过一次性原生对话框显示。
- **密码：** 永久管理员密码至少包含 8 个字符，最多 1024 个 UTF-8 字节。修改密码会撤销该管理员的全部会话。
- **两套独立控制：** 成功加载至少一个客户端 API 密钥后，密钥会保护模型与推理路由，包括受密钥包装的 `/v1/*` 路由；`/health` 与 `/v1/health` 始终公开。为兼容现有部署，密钥列表为空时，这些受密钥包装的路由会保持匿名开放。持久化管理员会话与服务端 RBAC 保护管理操作；已列出的引导、登录与 SSO 入口仍公开并接受独立校验。管理员会话只持久化哈希，可在重启后继续并可立即撤销；已认证变更需要会话绑定 CSRF。
- **仅删除账号：** **删除全部账号**只会移除 Cline 账号及账号内状态。客户端 API 密钥、模型、默认模型、监听设置、移除保留项与旧版迁移哨兵都会保留；只有在持久化完成后才会报告成功。
- **托管管理后台：** 二进制与桌面端默认绑定 `127.0.0.1`。生产环境的非回环监听必须配置精确公共 URL、受信反向代理模式及代理 CIDR，否则启动会被拒绝。HSTS 关闭时 RouteNest 仍接受 HTTP 规范源，因此由受信代理终止 HTTPS 是必需的部署实践，而不是所有路径都会强制执行的服务端不变量。
- **客户端密钥限制：** 账号池状态损坏、不可读、部分恢复或缺失状态不确定时，受密钥包装的路由会以认证存储错误失败关闭。确认是全新数据目录时，首次启动必须设置 `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true`，初始化后立即移除；经过验证且有意为空的密钥列表仍按兼容规则匿名开放。控制台生成的客户端密钥仍由时钟值派生，普通密钥写入仍采用尽力持久化。在进一步加固前，不要把它们当作互联网级安全边界；请在重启后核对密钥，并把网关置于可信网络控制之后。
- **敏感数据：** 账号 refreshToken、Provider Credential 与生成的客户端 API 密钥以明文保存在数据目录中。请保护该目录，不要提交到 Git 或分享。
- **Docker：** 示例 compose 只把后端端口绑定到宿主机回环，并要求显式配置引导、公共 URL 与代理信任。不要把后端端口直接暴露到互联网。

RBAC、OIDC、SAML、离线恢复与反向代理配置见 [管理员安全与 SSO](docs/admin-security.md)。

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

构建需要 Go 1.26.10 或更高版本：

```bash
go build -o routenest .
./routenest                # 127.0.0.1:3457
./routenest -port 8080     # 自定义端口
./routenest -host 0.0.0.0  # 需要配置托管管理后台安全参数
```

打开 <http://127.0.0.1:3457/admin/> 进入管理控制台。监听地址也可以通过 `CLINE_PROXY_HOST` 设置。

### Docker

```bash
docker compose up -d
docker compose logs -f
docker compose down
```

容器内监听 `0.0.0.0:3457`，示例 compose 仅把它发布到宿主机回环，供本机 TLS 反向代理使用。启动前先在 `.env` 中填写管理员安全变量，参见 [Docker 示例](docs/admin-security.md#docker-and-reverse-proxy)。状态位于 `/app/data` 并挂载到 `./data`，保留卷即可保留身份与网关数据。

### 首次运行

1. 确认使用全新数据目录时，仅在第一次启动设置 `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true`，随后立即移除。不得用该变量修复或重置已有安装。
2. 从本地终端/原生对话框读取一次性引导密钥，打开 `/admin/` 并设置永久密码。
3. 添加 Cline 账号、启用原生 OpenCode Zen，或配置服务商 API 密钥。
4. 为客户端生成 API 密钥。在至少生成一个密钥之前，受密钥包装的模型与推理路由接受匿名请求。
5. 选择默认模型。若不设置，RouteNest 会优先选择被归类为免费的活跃 Cline 模型；目录初始化后如无此类模型，可能回退到任意计费类别的第一个活跃已启用模型。

## 客户端配置

两种协议共用同一端口（默认 `3457`）。成功加载至少一个客户端 API 密钥后，可通过 `Authorization: Bearer <key>` 或 `x-api-key: <key>` 传入密钥。密钥列表为空时，模型与推理端点为兼容现有部署保持开放；`/health` 与 `/v1/health` 始终公开。

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

内置注册表包含 **111 个 Provider Definition（服务商定义）**：21 个由 RouteNest 维护，90 个来源于 ExtremeRouter。其中两个维护定义是固定运行时 Provider（Cline 与 OpenCode Zen），因此「连接 Provider」目录公开 **109 个可选预设**。

| 来源 | 定义数 | 可选接入预设 | 说明 |
| --- | ---: | ---: | --- |
| RouteNest 维护 | 21 | 19 | 包含作为固定运行时 Provider 的 Cline 与 OpenCode Zen |
| ExtremeRouter 导入 | 90 | 90 | 包含免费、免费额度、带免费额度的 API Key 与本地运行时条目 |
| **合计** | **111** | **109** | **34 个可选预设带有 `freeTier` 服务商级提示** |

`freeTier` 与 `kind` 是注册表中的展示/分组提示，不是按模型授予的访问权限。预设包含种子模型 ID，以及（在定义时）目录抓取器。注册表条目并不表示服务当前可访问、账号有权使用某个模型，或服务商特定功能可用。

认证方式取决于服务商：

- 大多数托管服务商使用 API 密钥。
- 部分预设使用浏览器或设备登录（例如 GitHub Copilot、Kilo Code、Kimi for Coding、Qwen Chat、CodeBuddy、WorkBuddy）。
- 本地运行时无需认证。
- Cline 账号凭据与原生 OpenCode Zen 配置用于对应集成。

只有部分预设与请求模式经过仓库测试和实际部署验证。请把目录当作起点，在依赖某个服务商前先用一个小请求验证。

## 模型发现

- RouteNest 在启动时拉取 Cline 推荐模型列表，每小时重新同步，并可在控制台按需触发。
- 带目录抓取器的预设可以刷新自己的模型或价格列表。
- 不在任何目录中的端点可以手动添加模型 ID。
- 未配置默认模型时，RouteNest 会先选择被归类为免费、活跃、已启用且未下架的 Cline 模型。目录初始化后如无此类模型，可能选择任意计费类别的第一个活跃、已启用且未下架的目录模型。
- 可选的「仅免费」模式会拒绝未被归类为免费的模型。**这不等于零成本保证：**
  - 过滤依赖 RouteNest 当前的模型分类，分类可能不完整、过期或不准确。
  - 模型或 Provider 被标记为免费，并不保证底层账号、路由或请求不会产生费用。
  - 在依赖该设置前，请核对 Provider 的服务条款、账号授权、定价与用量限制。路由/账号级访问未经独立验证时，不要把「仅免费」视为零成本保证。

## 路由与组合

1. 客户端把模型名或组合名发到某个 `/v1/*` 端点。
2. RouteNest 把模型解析到 Cline 账号池、OpenCode Zen 上游，或服务商预设/自定义服务商。
3. 账号池按 `round_robin`、`fill` 或 `random` 选择账号。命中 `402`/`429` 的账号按模型冷却并自动恢复。
4. 遇到符合条件的上游失败时，RouteNest 会尝试其他账号或服务商；对于组合，则按配置顺序进入下一个条目。
5. 响应按入站协议以 SSE 转发：OpenAI 客户端收到 OpenAI 分块，Anthropic 客户端收到 Anthropic 事件。

组合顺序是唯一权威：不健康的条目会被跳过，而不会被重排。回退到全局免费链属于显式开启的应急行为，并在控制台中标注。会话亲和与模型别名均可选配置。

## 管理控制台

`/admin/` 需要完成管理员引导或登录，用于管理整个网关：

- 仪表盘、请求量与服务商健康度。
- 账号池、连接、轮换策略与导入/导出。
- 服务商预设、自定义服务商、密钥、Base URL、冷却与配额。
- 模型目录、自定义模型、默认模型与模型测试。
- 模型组合与有序回退。
- 请求日志，包括延迟、token、TPS、上游与模型。
- 客户端 API 密钥、管理员用户/角色/会话、OIDC/SAML Provider、监听安全、管理密码、出口代理与运行时调优。

界面支持中英双语，跟随浏览器语言，可在侧栏切换。桌面宽度下侧栏可收起为图标轨道；`Ctrl+B` 切换。

## 出口代理

面向有区域限制或限流的上游：

- **Cline 出口代理池** - 用于对话、登录/令牌刷新与模型同步。
- **OpenCode Zen 出口代理池** - 独立代理池，限流出口会自动冷却。

优先级：应用内代理池 → 环境变量代理（`HTTPS_PROXY`）→ 直连。回环与内网目标始终直连。

## 数据与升级

RouteNest 默认把状态存放在操作系统的用户配置目录：

- Linux：`$XDG_CONFIG_HOME/routenest`，通常为 `~/.config/routenest`
- macOS：`~/Library/Application Support/routenest`
- Windows：`%AppData%\routenest`

设置 `ROUTENEST_DATA_DIR` 可指定目录（例如 Docker 挂载卷）。旧的
`CLINE2API_DATA_DIR` 仍作为低优先级兼容变量。

| 文件 | 用途 |
| --- | --- |
| `.routenest-accounts.json` | Cline 账号池、客户端 API 密钥、逻辑模型设置、已保存监听主机与旧管理员迁移哨兵 |
| `.routenest-accounts.json.initialized` | 账号/客户端认证存储的初始化见证文件；必须与账号池文件一起备份和恢复 |
| `admin-auth/.routenest-admin-auth.json` | 持久化管理员、角色/SSO 元数据与哈希会话 |
| `admin-auth/.routenest-admin-auth.journal` | 经 `fsync` 持久化、哈希链式的管理员安全与审计日志 |
| `admin-auth/sso-secrets/` | 仅所有者可读的 OIDC/SAML 文件密钥批准根目录 |
| `.routenest-config.json` | 代理配置、别名、策略与模型组合 |
| `.routenest-connections.json` | Provider Connection 记录及其访问/刷新令牌（Qoder 等非 Cline 登录） |
| `.routenest-credentials.json` | 为文件格式/迁移兼容保留的停用旧版单一 Cline refreshToken 辅助文件；当前账号登录与路由不会读写它 |
| `.routenest-providers.json` | 自定义服务商 |
| `.routenest-providers-cooldowns.json` | 服务商冷却状态 |
| `.routenest-providers-sync.json` | 服务商目录同步状态 |
| `.routenest-proxy.json` | Cline 出口代理池 |
| `.routenest-zen.json` | OpenCode Zen 配置与出口代理池 |
| `.routenest-request-logs.json` | 请求日志 |
| `capture-logs/` | 交互式 OAuth 抓包输出，可能包含授权流量 |
| `override.md` | 可选的 System Prompt 覆盖，从工作目录读取 |

首次使用时，RouteNest 会把可执行文件目录、工作目录、`~/.routenest/`
与 `~/.cline2api/` 中缺失的当前 `.routenest-*` 文件和旧 `.cline-*`
文件复制到所选目录。已存在的目标文件优先；迁移可重复执行，源文件保留作恢复备份。

如果账号池和见证文件同时缺失，RouteNest 不会把已有或部分恢复的数据目录猜测为全新安装。请恢复有效账号池文件并重启。恢复的账号池有效但缺少见证文件时，只有在账号池验证成功后才会重新创建见证文件。

在类 Unix 系统上，数据目录和抓包目录权限为 `0700`，已知状态文件为
`0600`；Windows 对私有路径使用仅当前用户与 SYSTEM 可访问的受保护 ACL。
如果无法建立安全权限，RouteNest 会以明确错误停止启动，不再回退到可执行文件旁写入凭据。

Windows 持久写入使用带 write-through 的 `MoveFileEx` 替换，并通过可写目录句柄刷新。当前修正已完成 Windows 交叉编译，但在真实 Windows 运行测试完成前，不应宣称 C1 已获得完整跨平台批准。

> 数据文件包含明文 refreshToken、Provider Credential 与生成的客户端 API 密钥。请安全备份，不要公开，也不要提交到 Git。

## 从源码构建

CLI 二进制需要 Go 1.26.10 或更高版本。构建桌面端还需要下面列出的平台工具链。

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
main.go, desktop_main.go   精简的 CLI 与桌面端入口
internal/app/              两个入口共享的服务装配
internal/proxycore/        HTTP 路由、协议桥接、Responses 与 SSE
internal/routing/          模型发现、策略与上游调用
internal/providers/, zen/  服务商注册表与 OpenCode Zen 集成
internal/egress/           共享 HTTP 传输、代理与重试策略
internal/combos/           具名模型组合与回退执行
internal/compaction/       上下文管理与压缩
internal/admin/            管理 API 与 internal/admin/ui/ 内嵌界面
internal/accounts/, connections/, requestlog/, store/   持久状态
desktop/                   Wails 外壳与构建脚本
CONTRACTS.md, DESIGN.md     Phase 1 契约提案与现行 UI 设计规则
CONTEXT.md                  领域术语表
docs/, scripts/             已跟踪参考、本地历史记录与运维脚本
```

Go 模块路径为 `routenest`。仓库根目录保留 CLI、桌面入口与本地管理员恢复命令；服务装配与服务实现代码均位于 `internal/`。

## 署名与许可证

RouteNest 是 [Cline2API](https://github.com/luawei1/cline2api) 的独立分支，原作者为 luawei1，以 MIT 许可证分发。上游版权声明保留在 [LICENSE](LICENSE) 中。

90 个导入的 Provider Definition 来源于 ExtremeRouter 服务商注册表（MIT）。相应的版权与许可证声明保留在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 中。

RouteNest 与 Cline、OpenCode、Qoder 以及目录中列出的任何服务商均无关联，也未获得其认可。请仅使用你有权使用的账号与凭据，并遵守各服务商的服务条款。服务商名称与商标归其各自所有者所有。

## 许可证

[MIT](LICENSE)
