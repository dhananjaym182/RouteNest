<div align="center">

<img src="docs/assets/routenest-icon.svg" alt="RouteNest Logo" width="108" height="108" />

# RouteNest

### Free-First AI API Routing
**Multiple Providers · Intelligent Routing · Cost-Aware Choices**
*(免费优先 AI API 路由 · 多服务商接入 · 智能路由 · 成本感知选择)*

RouteNest 是一个自托管、Go 驱动的 AI 网关，面向编码代理与 AI 应用。通过单一端点，将 OpenAI 与 Anthropic 兼容请求路由到已配置的服务商 API、本地模型运行时以及受支持的账号集成。

它提供服务商预设、账号管理、模型发现、有序回退、请求日志与内置管理控制台。

[![Go](https://img.shields.io/badge/Go-1.26.11-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue)](#首次公开预览)
[![Provider definitions](https://img.shields.io/badge/Provider%20definitions-111-brightgreen)](#服务商覆盖)

**English:** [English README](README.md)

</div>

---

## 首次公开预览

**[RouteNest v0.1.0-beta.1 - First public preview](https://github.com/dhananjaym182/RouteNest/releases/tag/v0.1.0-beta.1)** 是**供评估使用的预发布版本**，不是稳定版，也未获生产环境认证。Beta 期间 API、状态格式与上游集成可能变化。

- **发布产物：** 提供 **Windows x64、macOS Apple Silicon 与 Linux x64** 的带版本号桌面归档、三个对应的 SPDX SBOM 与 `SHA256SUMS`。每个归档包含一个可执行文件、许可证 / 声明与发布说明。本次预览**没有 Intel macOS 产物**、安装器、独立 CLI 下载或已发布的容器镜像；CLI / 服务端与 Docker 需从源码构建。
- **运行条件与签名：** 运行下载文件无需 Go 或 Node.js。Windows 需要 WebView2，macOS 使用系统 WKWebView，Linux 需要兼容 glibc 的桌面环境、GTK 3 与 **WebKitGTK 4.0**；只有 4.1 并不足够。Windows **未签名**；macOS **没有 Developer ID 签名，也未公证**。参见[桌面运行条件](#桌面端预编译版本)。
- **解压前验证：** 用 `SHA256SUMS` 检查归档与 SBOM，再验证这两个文件及校验和清单的 GitHub 构建证明（attestation），限定到本仓库的 Build 工作流与发布标签。校验不匹配、验证失败或缺少证明时请停止。来源证明与 SBOM 不等于代码安全认证，也不能代替操作系统签名。[安装与校验命令](RELEASE_NOTES.md#install-and-verify)。
- **能力与限制：** 支持 OpenAI / Anthropic 流式请求、**Responses API 子集**、账号轮换、有序回退、模型发现、限额、出口控制与双语管理台。服务商预设只是模板，**不是集成认证**；免费优先 / 仅免费标签不是零成本保证。只使用有权使用的账号，遵守上游条款、定价与授权；Cline 账号集成为非官方路径。
- **设置与数据：** 没有默认管理员密码；保存首次设置的恢复码，并在依赖客户端认证前生成 API 密钥。经验证的空密钥列表仍按兼容规则允许匿名模型 / 推理访问。服务商凭据、刷新令牌与生成的客户端密钥以**明文**保存，依赖文件系统权限保护，而非静态加密。升级前备份整个数据目录；托管管理后台应使用 HTTPS 与范围严格的受信代理配置。
- **验证范围：** CI 包含自动化 Go / race、前端 / 本地化、Docker 与浏览器检查、原生构建及无窗口就绪自检。模拟与编译不代表对打包 WebView、真实操作系统凭据存储、干净系统安装、签名升级或每个在线服务商的认证。参见 [CI 验证范围](#ci-验证范围)与标签工作流的实际结果。

[发布说明](RELEASE_NOTES.md) · [变更日志](CHANGELOG.md) · [安全指南](docs/admin-security.md) · [升级与备份](#数据与升级)

## RouteNest 提供的能力

拟议的契约权威、术语与尚未解决的行为决策见 [CONTRACTS.md](CONTRACTS.md)，等待评审。历史计划与验证报告属于带日期的证据，不代表当前测试结果。

- **多账号路由** - 选择并轮换已配置的账号，具备冷却与可重试错误处理。
- **服务商目录** - 111 个内置 Provider Definition，其中 109 个是可选接入预设，并支持自定义 OpenAI 兼容端点。可用性与协议支持因服务商而异。
- **多种 API 格式** - OpenAI Chat Completions、基于 chat completions 的 Responses API 子集，以及 Anthropic Messages 端点，支持上游可提供的流式内容、工具、reasoning 与 usage。
- **模型组合** - 定义有序模型列表，在符合条件的上游失败时回退。
- **模型发现** - 同步受支持的模型目录，审批新发现的模型，管理按服务商划分的模型供应，配置自定义模型 ID，并选择默认模型。
- **管理控制台** - 13 个页面，中英双语与明暗主题，管理账号、连接、服务商、模型、组合、客户端 API 密钥、代理与请求日志；快速开始页生成客户端示例。
- **管理员访问控制** - 一次性设置、角色权限、OIDC / SAML、可撤销桌面信任、安全审计日志与恢复码。
- **出口代理池** - 配置一个对所有 Provider 生效的默认（全局）出口池、独立的原生 Zen 出口池，以及单个 Provider 的覆盖；不宣称提供通用 Profile 绑定界面。
- **请求可观测性** - 查看请求日志、延迟、token 用量与可用的服务商统计。
- **灵活部署** - 运行 Go 二进制、使用桌面应用，或通过 Docker 部署。

服务商可用性、认证要求、免费额度与支持的功能取决于上游服务。预设只是配置模板，并不代表已经过测试的集成；收录预设并不保证每个模型都经过测试，也不保证每个账号都可用。

## 部署前安全须知

> **安全提示：** RouteNest 不提供通用默认密码，也不允许匿名执行管理操作。全新安装进入一次性引导模式，必须设置永久本地管理员密码后才能正常管理。静态管理界面外壳以及少量状态、引导、登录与 SSO 协议入口为公开路由，但不会授予管理权限。

- **一次性引导：** CLI 只把随机引导密钥写到本地控制终端。首次启动没有控制终端时需设置 `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD`；只要已初始化的持久身份状态仍保留，后续启动就会忽略该变量。桌面端提供原生首次设置，无需终端或环境变量。
- **桌面登录：** 密码认证后可显式信任桌面应用。RouteNest 会在 Windows Credential Manager、macOS Keychain 或 Linux Secret Service 中保存随机且可撤销的凭据，绝不保存管理员密码。普通浏览器仍需独立认证。受保护存储不可用或损坏时，会安全回退到密码登录，不使用明文存储。
- **密码：** 永久管理员密码至少包含 8 个字符，最多 1024 个 UTF-8 字节。修改密码会撤销该管理员的全部会话和受信任设备。
- **锁定与退出：** 锁定只结束当前会话。退出会结束会话并暂停桌面自动登录，不会仅为退出而要求密码。忘记此设备会撤销设备授权并验证本机凭据删除。密码登录不会静默恢复已暂停的信任。
- **原生隔离：** Wails v2.17.1 将设置与控制台保留在内嵌源，额外绑定源为空。桌面会话交接与受保护 API 调用只在进程内完成；普通浏览器或被其他进程占用的本机端口不会收到原生权限。打包 WebView 和真实操作系统凭据存储验证仍与单元测试、交叉编译分开记录。
- **两套独立控制：** 成功加载至少一个客户端 API 密钥后，密钥会保护模型与推理路由，包括受密钥包装的 `/v1/*` 路由；`/health` 与 `/v1/health` 是公开的存活探针。`/ready` 与 `/v1/ready` 是公开的就绪探针，客户端认证存储不可用时返回 HTTP 503。只有在有效存储已加载后，空密钥列表才按兼容规则保持匿名开放。持久化管理员会话与服务端 RBAC 保护管理操作；已列出的引导、登录与 SSO 入口仍公开并接受独立校验。管理员会话只持久化哈希，可在重启后继续并可立即撤销；已认证变更需要会话绑定 CSRF。
- **仅删除账号：** **删除全部账号**只会移除 Cline 账号及账号内状态。客户端 API 密钥、模型、默认模型、监听设置、移除保留项与旧版迁移哨兵都会保留；只有在持久化完成后才会报告成功。
- **托管管理后台：** 二进制与桌面端默认绑定 `127.0.0.1`。生产环境的非回环监听必须配置精确公共 URL、受信反向代理模式及代理 CIDR，否则启动会被拒绝。HSTS 关闭时 RouteNest 仍接受 HTTP 规范源，因此由受信代理终止 HTTPS 是必需的部署实践，而不是所有路径都会强制执行的服务端不变量。
- **客户端密钥安全：** 账号池状态损坏、不可读、部分恢复或缺失状态不确定时，受密钥包装的路由会以认证存储错误失败关闭。确认是全新数据目录时，首次启动必须设置 `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true`，初始化后立即移除。生成的密钥使用 256 位密码学随机数，密钥变更只有在持久化完成后才会发布。删除密钥需要 `credential.write` 与 `dangerous.execute`；删除最后一个密钥还必须明确确认将禁用客户端 API 认证。经过验证且有意为空的密钥列表仍按兼容规则匿名开放。
- **敏感数据：** 账号刷新令牌、服务商凭据与生成的客户端 API 密钥以明文保存在数据目录中，依赖文件系统权限保护，而非静态加密。操作系统保护的桌面信任不会加密服务商秘密。请保护目录与备份，不要提交到 Git 或分享；系统凭据存储无法抵御同用户恶意软件或主机失陷。
- **Docker：** 示例 compose 只把后端端口绑定到宿主机回环，并要求显式配置引导、公共 URL 与代理信任。不要把后端端口直接暴露到互联网。

RBAC、OIDC、SAML、离线恢复与反向代理配置见 [管理员安全与 SSO](docs/admin-security.md)。`routenest auth --help` 与 `routenest auth status` 提供不修改状态的帮助和经过验证的只读状态；`routenest auth recover` 仍是所有者控制的离线恢复命令。

## 快速开始

### 桌面端（预编译版本）

从 [v0.1.0-beta.1 发布页](https://github.com/dhananjaym182/RouteNest/releases/tag/v0.1.0-beta.1) 下载对应归档、SPDX SBOM 与 `SHA256SUMS`，[解压前完成校验](#验证下载文件)，再打开解压后的应用。这些是独立可执行文件的归档，不是安装器。

确认是全新安装时，正常打开桌面应用即可。原生设置会创建管理员密码、显示一次性恢复码，并可选择信任此设备以便后续自动登录，设置过程无需终端命令或环境变量。`-initialize-empty-account-store` 仍是高级首次运行控制，不是密码重置机制。

| 平台 | 发布归档 | 运行条件 |
| --- | --- | --- |
| Windows x64 | `routenest_0.1.0-beta.1_windows_amd64.zip` | Microsoft Edge WebView2 运行时。Windows 11 已包含；保持更新的 Windows 10 通常也有。缺少时安装 Microsoft Evergreen Runtime。 |
| macOS Apple Silicon | `routenest_0.1.0-beta.1_darwin_arm64.tar.gz` | 带系统 WKWebView 的 macOS；在 macOS 15 上构建。没有 Intel 产物。运行无需 Xcode 工具；Intel Mac 需从源码构建。 |
| Linux x64 | `routenest_0.1.0-beta.1_linux_amd64.tar.gz` | 兼容 glibc 的桌面环境、GTK 3 与 WebKitGTK 4.0 运行时库；在 Ubuntu 22.04 上构建。Ubuntu 22.04 对应包为 `libgtk-3-0` 与 `libwebkit2gtk-4.0-37`。只有 WebKitGTK 4.1 并不足够。 |

每个归档解压到 `routenest_0.1.0-beta.1_<platform>/`，包含 Windows 的 `routenest-desktop.exe` 或 macOS / Linux 的 `routenest-desktop`，以及 `LICENSE`、`THIRD_PARTY_NOTICES.md` 和以 `README.md` 保存的发布说明。对应 SBOM 为 `routenest_0.1.0-beta.1_windows_amd64.spdx.json`、`routenest_0.1.0-beta.1_darwin_arm64.spdx.json` 与 `routenest_0.1.0-beta.1_linux_amd64.spdx.json`。它们列出构建产物的组成，不代表没有漏洞。

运行这些产物无需 Go、Node.js 或开发头文件。Linux 自动桌面登录可选依赖已解锁的 Secret Service；缺少时使用密码登录。旧版操作系统与干净系统安装未获广泛认证。

**未签名 / 未公证：** Windows 没有可信代码签名；macOS 没有 Developer ID 签名，也未公证。完成校验后，SmartScreen 可能要求 **更多信息 → 仍要运行**，Gatekeeper 可能要求 **打开** 或在 **系统设置 → 隐私与安全性** 中批准。只批准已验证的文件，不要关闭系统级保护。

### 验证下载文件

使用同一版本的归档、对应 SBOM 与 `SHA256SUMS`。清单覆盖三个归档与三个 SBOM；解压前验证清单及已下载文件。Linux x64 下载这三个文件后执行：

```bash
(
set -euo pipefail
base=routenest_0.1.0-beta.1_linux_amd64
for file in "$base.tar.gz" "$base.spdx.json"; do
    awk -v file="$file" '$2 == file { print; n++ } END { if (n != 1) exit 1 }' SHA256SUMS | sha256sum --check -
done
for file in "$base.tar.gz" "$base.spdx.json" SHA256SUMS; do
    gh attestation verify "$file" --repo dhananjaym182/RouteNest --signer-workflow dhananjaym182/RouteNest/.github/workflows/build.yml --source-ref refs/tags/v0.1.0-beta.1
done
)
```

归档与 SBOM 校验都必须显示 `OK`，所有证明必须验证本仓库的 Build 工作流与发布标签。校验不匹配、验证失败或缺少证明时请停止。macOS 使用 `shasum`，Windows 使用 `Get-FileHash`。完整下载、各平台验证、解压与启动命令见[发布说明](RELEASE_NOTES.md#install-and-verify)。校验和检查打包文件，不能把归档摘要当作解压后可执行文件的摘要；构建证明检查来源，不保证代码安全、操作系统签名或服务商认证。若发布版本启用不可变性，发布说明还提供 `gh release verify` 与 `gh release verify-asset`，用于检查 GitHub 签名的发布版本 / 产物绑定。

### 运行二进制

构建需要 Go 1.26.11 或更高版本：

```bash
go build -o routenest .
./routenest                # 127.0.0.1:3457
./routenest -port 8080     # 自定义端口
./routenest -host 0.0.0.0  # 需要配置托管管理后台安全参数
```

该命令构建的是 **CLI / 服务端**，不是原生桌面应用。需要原生密码设置和操作系统保护的设备信任时，请使用[桌面构建说明](desktop/README.md#building-for-each-platform)。
运行 `./routenest --help` 或 `./routenest auth help`，可查看密码创建、修改、忘记密码恢复、受信任设备以及只读 `auth status` 命令的说明。

打开 <http://127.0.0.1:3457/admin/> 进入管理控制台。`/admin` 会重定向到 `/admin/`。
直接通过本机 `localhost` 访问时，会重定向到已配置的规范回环地址；这不会放宽认证，也不允许管理员 API 使用其他来源。
未认证的浏览器会显示独立登录页或首次密码创建页，只有会话验证成功后才显示仪表盘。
已有有效浏览器会话时会直接进入仪表盘；退出登录或使用隐私窗口即可查看登录页。
监听地址也可以通过 `CLINE_PROXY_HOST` 设置。

### Docker

```bash
docker compose up -d
docker compose logs -f
docker compose down
```

容器内监听 `0.0.0.0:3457`，示例 compose 仅把它发布到宿主机回环，供本机 TLS 反向代理使用。启动前先在 `.env` 中填写管理员安全变量，参见 [Docker 示例](docs/admin-security.md#docker-and-reverse-proxy)。状态位于 `/app/data` 并挂载到 `./data`，保留卷即可保留身份与网关数据。

### 首次运行

1. **桌面版：** 正常打开 RouteNest。受信任的原生设置窗口会确认全新数据目录、创建管理员密码、显示一次性恢复码，并让用户显式选择是否信任此设备以自动登录。
2. **CLI / 无头服务 / Docker：** 确认使用全新数据目录时，仅在第一次启动设置 `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true`，随后立即移除。CLI 也可使用一次 `-initialize-empty-account-store`。这些控制都不能修复或重置已有安装；一次性管理员引导密钥仍须通过文档规定的受信任本地路径交付：从终端启动时，密钥会打印在该终端或控制台窗口（Windows、macOS、Linux）；Docker、系统服务以及任何没有终端的启动方式需要设置 `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD`。浏览器设置完成后会一次性显示恢复码，请先保存再继续。
3. 按“概览”中的设置清单连接 Cline 账号、原生 OpenCode Zen 或 API Provider，确认模型和客户端访问，然后发送第一条网关请求。
4. 需要客户端认证时生成 API 密钥。在至少生成一个密钥之前，受密钥包装的模型与推理路由仍按已记录的兼容策略接受匿名请求。
5. 保存管理员恢复码。“忘记密码？”会消费并轮换恢复码、撤销管理员会话和受信任设备，同时保留 Provider、模型和路由数据。

内置更新检查只跟随稳定版本，可能不会提示本 Beta；请从对应标签发布页显式下载预览版本。

## 客户端配置

两种协议共用同一端口（默认 `3457`）。成功加载至少一个客户端 API 密钥后，可通过 `Authorization: Bearer <key>` 或 `x-api-key: <key>` 传入密钥。经过验证的密钥列表为空时，模型与推理端点为兼容现有部署保持开放。`/health` 与 `/v1/health` 报告进程存活；`/ready` 与 `/v1/ready` 报告客户端认证存储是否可用。

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
| `POST /v1/responses` | OpenAI Responses 子集 | 转换为上游 chat-completions 调用；支持文本、工具、reasoning 内容、流式事件与 usage |
| `POST /v1/messages` | Anthropic Messages | 流式与非流式；工具调用/结果、thinking 内容与 usage |
| `POST /v1/messages/count_tokens` | Anthropic token 计量 | 本地估算，不是上游分词器 |
| `GET /v1/models` | OpenAI 模型列表 | 已启用的模型与已启用的组合 |
| `GET /v1/health` | 健康检查 | 版本与活跃账号数 |
| `GET /v1/ready` | 就绪检查 | 仅客户端认证存储可用时返回 HTTP 200 |

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
- 客户端 API 密钥、管理员用户/角色/会话、受信任桌面设备、OIDC/SAML Provider、监听安全、管理密码、出口代理与运行时调优。

界面支持中英双语，跟随浏览器语言，可在侧栏切换。桌面宽度下侧栏可收起为图标轨道；`Ctrl+B` 切换。

## 出口代理

面向有区域限制或限流的上游：

- **默认出口代理池**（管理台：**全局出口代理**）- 所有没有专属代理的 Provider 都使用它，Cline 与 OAuth 的登录、令牌刷新和模型同步也一样。轮询与随机按新建连接选择；某个代理连接失败时自动改用列表中的下一个，该代理 30 秒内排到最后。
- **Provider 专属覆盖** - 单个 Provider 设置了自己的出口代理（或填 `-` 直连）时，该 Provider 不再使用默认池。
- **OpenCode Zen 出口代理池** - 独立代理池，限流出口会自动冷却。

优先级：Provider 专属覆盖 → 默认出口代理池 → 环境变量代理（`HTTPS_PROXY`）→ 直连。`localhost` 和内网 IP 地址始终直连。用域名访问的本地服务（例如 `ollama.lan`）仍会经代理池转发，请把该 Provider 的覆盖设为 `-`。

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
| `admin-auth/.routenest-admin-auth.json` | 持久化管理员、角色/SSO 元数据、受信任设备验证器与哈希会话 |
| `admin-auth/.routenest-admin-auth.journal` | 经 `fsync` 持久化、哈希链式的管理员安全与审计日志 |
| `admin-auth/sso-secrets/` | 仅所有者可读的 OIDC/SAML 文件密钥批准根目录 |
| `.routenest-config.json` | 代理配置、别名、策略与模型组合 |
| `.routenest-connections.json` | Provider Connection 记录及其访问/刷新令牌（Qoder 等非 Cline 登录） |
| `.routenest-credentials.json` | 为文件格式/迁移兼容保留的停用旧版单一 Cline refreshToken 辅助文件；当前账号登录与路由不会读写它 |
| `.routenest-providers.json` | 自定义服务商 |
| `.routenest-providers-cooldowns.json` | 服务商冷却状态 |
| `.routenest-providers-sync.json` | 服务商目录同步状态 |
| `.routenest-proxy.json` | 默认（全局）出口代理池 |
| `.routenest-zen.json` | OpenCode Zen 配置与出口代理池 |
| `.routenest-onboarding.json` | 设置清单进度：已验证服务商与首次成功网关请求 |
| `.routenest-request-logs.json` | 请求日志 |
| `capture-logs/` | 交互式 OAuth 抓包输出，可能包含授权流量 |
| `override.md` | 可选的 System Prompt 覆盖，从工作目录读取 |

首次使用时，RouteNest 会把可执行文件目录、工作目录、`~/.routenest/`
与 `~/.cline2api/` 中缺失的当前 `.routenest-*` 文件和旧 `.cline-*`
文件复制到所选目录。已存在的目标文件优先；迁移可重复执行，源文件保留作恢复备份。

如果账号池和见证文件同时缺失，RouteNest 不会把已有或部分恢复的数据目录猜测为全新安装。请恢复有效账号池文件并重启。恢复的账号池有效但缺少见证文件时，只有在账号池验证成功后才会重新创建见证文件。

在类 Unix 系统上，数据目录和抓包目录权限为 `0700`，已知状态文件为 `0600`。Windows 私有路径使用只向当前用户与 SYSTEM 授予访问权限的受保护 ACL，并接受 Windows 为本进程创建的文件指定的所有者（提升权限时为 Administrators 组）。如果无法建立安全权限，RouteNest 会以明确错误停止启动，不会回退到可执行文件旁写入凭据。

安全敏感的持久写入会先同步临时文件，替换后尝试刷新所在目录。Windows 替换使用 Go 的目录根约束重命名，而不是保证 write-through 的 `MoveFileEx`；替换后刷新失败会报告不确定状态，不会当作普通成功。这些机制不能保证抵御故障存储硬件或所有文件系统 / 断电场景。

### 升级或回退之前

1. 停止所有 RouteNest 进程，安全备份**整个所选数据目录**。把 `.routenest-accounts.json` 与 `.initialized` 见证文件保存在一起，把 `admin-auth/.routenest-admin-auth.json` 与审计日志保存在一起。包含被引用的 SSO 秘密文件，并另外保护环境变量秘密、已保存恢复码及工作目录中的 `override.md`。
2. 用 `SHA256SUMS` 与 GitHub 构建证明验证新归档、SBOM 与清单，解压到单独目录后，只替换可执行文件。继续使用相同数据目录和操作系统用户；不要加入首次初始化参数或引导变量来重置已有安装。
3. 重启、登录、检查就绪状态，并向每个依赖的服务商发送小请求。存储缺失、损坏或部分恢复时应停止并恢复有效备份，不要删除身份文件或绕过初始化保护。
4. Beta 状态格式可能变化，旧程序可能拒绝新管理员元数据。回退时使用旧可执行文件**与完整兼容备份**，不要混用快照 / 日志版本或手工修改身份状态。桌面信任凭据保存在操作系统凭据库，不在目录备份中；更换用户、机器或目录可能需要密码登录并显式重新注册。恢复的会话 / 密钥同样敏感；备份可能泄露时应撤销或轮换。

> 数据文件包含明文刷新令牌、服务商凭据与生成的客户端 API 密钥。请加密或以其他方式安全保护备份，不要公开，也不要提交到 Git。

## 从源码构建

CLI 二进制需要 Go 1.26.11 或更高版本。构建桌面端还需要目标操作系统工具链与原生 WebView 依赖；参见[桌面构建说明](desktop/README.md#building-for-each-platform)。

### 二进制

```bash
go build -o routenest .
```

### 桌面端

Wails 使用原生 WebView 绑定。请在目标系统构建，不要把交叉编译当作运行验证。安装工具链后执行：

```bash
./desktop/build.sh
```

Windows 需要 Go 与 C 工具链。macOS 需要 Xcode Command Line Tools；原生构建还需按桌面指南链接 `UniformTypeIdentifiers`。默认 Linux 脚本与发布构建使用 GTK 3 和 WebKitGTK **4.0**，例如 Ubuntu 22.04 的 `libgtk-3-dev` 与 `libwebkit2gtk-4.0-dev`。从源码使用 4.1 时需要额外的 `webkit2_41` 标签，不能只安装 4.1 开发包；参见 [Linux 构建选项](desktop/README.md#linux)。

开发包仅编译时需要。运行发布版本需要匹配的运行时库，而不是头文件。官方 macOS 产物仅支持 Apple Silicon。

## CI 验证范围

[Test 工作流](.github/workflows/test.yml)配置了：

- **Linux 与 Windows** 上的模块验证、Go 单元 / 集成测试、desktop 标签认证测试、vet 和 race 检查，包括专门的 Windows 持久化运行测试。
- Linux 上的 JavaScript 语法 / 单元契约与本地化检查。
- Linux 上经过认证的 Chromium 导航、服务商设置与安全流程，以及独立的桌面设置 / 登录**桥接模拟**。
- Linux 上从源码构建的 Docker 持久化冒烟测试。

[Build 工作流](.github/workflows/build.yml)依赖 Test 工作流，验证发布标签元数据，仅编译而不执行 production 标签桌面测试，在 Windows x64、macOS 15 arm64 和 Ubuntu 22.04 x64 上原生构建，并对每个可执行文件执行**无窗口 `/ready` 自检**。随后打包带版本号归档与许可证 / 声明 / 说明，生成 SPDX SBOM，验证精确产物清单与 `SHA256SUMS`，生成标签绑定的构建 / SBOM 证明后创建可评审的预发布草稿。自检启动内嵌网关，但不打开窗口，也不访问操作系统凭据库。草稿不等于已发布，也不能证明发布版本不可变性。

实际通过 / 失败以标签工作流结果为准。自动化测试、浏览器流程、模拟、校验和与来源证明**不等于**对打包 WebView、真实凭据库、干净安装 / 重开、签名升级、全部操作系统版本或在线服务商的全面认证。发布这些文档不代表真实运行验证通过报告。

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

## 发布文档

- [RELEASE_NOTES.md](RELEASE_NOTES.md)：预览限制、下载、校验与安装命令。
- [CHANGELOG.md](CHANGELOG.md)：按版本记录的用户可见变更。
- [desktop/README.md](desktop/README.md)：桌面运行条件、构建与打包。
- [CONTRIBUTING.md](CONTRIBUTING.md)：贡献者工作流程与文档检查要求；历史报告不是当前工作流结果。

## 署名与许可证

RouteNest 是 [Cline2API](https://github.com/luawei1/cline2api) 的独立分支，原作者为 luawei1，以 MIT 许可证分发。上游版权声明保留在 [LICENSE](LICENSE) 中。

90 个导入的 Provider Definition 来源于 ExtremeRouter 服务商注册表（MIT）。相应的版权与许可证声明保留在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 中。

RouteNest 与 Cline、OpenCode、Qoder 以及目录中列出的任何服务商均无关联，也未获得其认可。请仅使用你有权使用的账号与凭据，并遵守各服务商的服务条款。服务商名称与商标归其各自所有者所有。

## 许可证

[MIT](LICENSE)
