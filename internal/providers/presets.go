package providers

import (
	"routenest/internal/providerreg"
)

// ============================================================================
// Provider 预设（市面常见免费/低成本 OpenAI 兼容上游）
// ============================================================================

type providerPreset = providerreg.Preset

// modelsFetcher 描述 preset 对应上游的模型目录端点（目前仅 openai 兼容格式）。
type modelsFetcher = providerreg.ModelsFetcher

// mergePresets 把 src 合入 dst，冲突时保留手写的运行时 preset。
func mergePresets(dst, src map[string]providerPreset) map[string]providerPreset {
	out := make(map[string]providerPreset, len(dst)+len(src))
	for k, v := range src {
		out[k] = v
	}
	for k, v := range dst {
		out[k] = v
	}
	return out
}

// providerPresets 常见 OpenAI 兼容上游预设。Key 为前端展示名。
var providerPresets = map[string]providerPreset{
	"openrouter": {
		Name:    "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Headers: map[string]string{
			"HTTP-Referer": "https://cline.bot",
			"X-Title":      "RouteNest",
		},
		Notes:    "300+ models; many :free variants. Key: openrouter.ai/keys",
		FreeTier: true,
	},
	"groq": {
		Name:     "Groq",
		BaseURL:  "https://api.groq.com/openai/v1",
		Notes:    "Fast free tier (Llama/Qwen). Key: console.groq.com/keys",
		FreeTier: true,
	},
	"cerebras": {
		Name:     "Cerebras",
		BaseURL:  "https://api.cerebras.ai/v1",
		Notes:    "Free tier, very high throughput. Key: cloud.cerebras.ai",
		FreeTier: true,
	},
	"gemini": {
		Name:     "Google AI Studio",
		BaseURL:  "https://generativelanguage.googleapis.com/v1beta/openai",
		Notes:    "Gemini free tier via OpenAI-compat layer. Key: aistudio.google.com/apikey",
		FreeTier: true,
	},
	"mistral": {
		Name:     "Mistral (La Plateforme)",
		BaseURL:  "https://api.mistral.ai/v1",
		Notes:    "Free experiment tier. Key: console.mistral.ai",
		FreeTier: true,
	},
	"together": {
		Name:     "Together AI",
		BaseURL:  "https://api.together.xyz/v1",
		Notes:    "Some free models (e.g. Llama Vision free). Key: api.together.ai",
		FreeTier: true,
	},
	"deepseek": {
		Name:    "DeepSeek",
		BaseURL: "https://api.deepseek.com/v1",
		Notes:   "Paid, very cheap. Key: platform.deepseek.com",
	},
	"openai": {
		Name:    "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		Notes:   "Paid. Key: platform.openai.com",
	},
	"vllm-local": {
		Name:     "Local vLLM / Ollama",
		BaseURL:  "http://127.0.0.1:8000/v1",
		Notes:    "Self-hosted OpenAI-compatible server (no key needed usually)",
		FreeTier: true,
	},
	"ollama-local": {
		Name:     "Local Ollama",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Kind:     "local",
		Notes:    "Ollama 自带 OpenAI 兼容端点（/api 同服务），无需密钥，模型列表来自本机 /v1/models",
		FreeTier: true,
	},
	// §8.3 登录类免费源：无 API Key，用「登录连接（OAuth）」设备码授权后在编辑器绑定连接
	"kilocode": {
		Name:      "Kilo Code (Free)",
		BaseURL:   "https://api.kilo.ai/api/openrouter",
		Notes:     "设备码登录（无需 API Key）；登录后在编辑器「账号连接」选连接，组织头自动注入",
		FreeTier:  true,
		LoginFlow: true,
		ModelIDs: []string{
			"anthropic/claude-sonnet-4-20250514",
			"anthropic/claude-opus-4-20250514",
			"google/gemini-2.5-pro",
			"google/gemini-2.5-flash",
			"openai/gpt-4.1",
			"openai/o3",
			"deepseek/deepseek-chat",
			"deepseek/deepseek-reasoner",
		},
	},
	"github-copilot": {
		Name:          "GitHub Copilot (Free plan)",
		BaseURL:       "https://api.githubcopilot.com",
		Notes:         "设备码登录 GitHub → 自动兑换 Copilot 作业票（免 Key）；Copilot Free 计划含少量高级请求配额",
		FreeTier:      true,
		LoginFlow:     true,
		ModelsFetcher: &modelsFetcher{URL: "https://api.githubcopilot.com/models", Type: "openai"},
	},
	"qwen-chat": {
		Name:      "Qwen Chat (Free)",
		BaseURL:   "https://portal.qwen.ai/v1",
		Notes:     "设备码登录 Qwen Chat（PKCE，无需 API Key）；OpenAI 兼容端点",
		FreeTier:  true,
		LoginFlow: true,
		ModelIDs:  []string{"qwen3-coder-plus", "qwen3-coder-flash", "vision-model", "coder-model"},
	},
	"kimi-coding": {
		Name:          "Kimi for Coding (Free)",
		BaseURL:       "https://api.kimi.com/coding/v1",
		Notes:         "设备码登录 Kimi（标准 RFC8628，无需 API Key）；OpenAI 兼容 /coding/v1，含 k2 编码模型免费额度",
		FreeTier:      true,
		LoginFlow:     true,
		ModelIDs:      []string{"kimi-k2.6", "kimi-k2.5", "kimi-k2.5-thinking", "kimi-latest"},
		ModelsFetcher: &modelsFetcher{URL: "https://api.kimi.com/coding/v1/models", Type: "openai"},
	},
	"codebuddy-cn": {
		Name:      "CodeBuddy CN (Free)",
		BaseURL:   "https://copilot.tencent.com/v2",
		Notes:     "浏览器授权轮询登录（state-poll，无需 API Key）；腾讯 CodeBuddy 国内站 /v2/chat/completions",
		FreeTier:  true,
		LoginFlow: true,
		ModelIDs:  []string{"glm-5.3", "glm-5.3-flash", "glm-5.2", "minimax-m3", "kimi-k2.7"},
	},
	"codebuddy-intl": {
		Name:      "CodeBuddy Intl (Free)",
		BaseURL:   "https://www.codebuddy.ai/v2",
		Notes:     "浏览器授权轮询登录（state-poll，无需 API Key）；CodeBuddy 国际站 /v2/chat/completions",
		FreeTier:  true,
		LoginFlow: true,
		ModelIDs:  []string{"glm-5.3", "glm-5.3-flash", "glm-5.2", "minimax-m3", "kimi-k3-1"},
	},
	"workbuddy": {
		Name:      "WorkBuddy (Free)",
		BaseURL:   "https://www.workbuddy.ai/v2",
		Notes:     "浏览器授权轮询登录（state-poll，无需 API Key）；WorkBuddy /v2/chat/completions，含混元预览模型",
		FreeTier:  true,
		LoginFlow: true,
		ModelIDs:  []string{"hy3", "hy4-preview", "glm-5.3", "glm-5.3-flash", "minimax-m3"},
	},
	// Qoder：COSY 签名协议（非 OpenAI 兼容），推理与模型目录均走 /algo 接口
	"qoder": {
		Name:     "Qoder",
		BaseURL:  "https://api3.qoder.sh",
		Protocol: "qoder",
		Notes:    "浏览器设备码登录（COSY 签名，非 OpenAI 兼容）；推理走 /algo/api/v2/service/pro/sse/agent_chat_generation，模型目录来自 /algo/api/v2/model/list",
		FreeTier: true,
		ModelIDs: []string{
			"qoder/auto",
			"qoder/performance",
			"qoder/efficient",
			"qoder/lite",
			"qoder/qmodel_38max",
			"qoder/qmodel_latest",
			"qoder/qmodel",
			"qoder/qfmodel",
			"qoder/kmodel_latest",
			"qoder/kmodel",
			"qoder/gmodel",
			"qoder/gfmodel",
			"qoder/dmodel",
			"qoder/dfmodel",
			"qoder/mmodel",
		},
		ModelsFetcher: &modelsFetcher{URL: "https://api3.qoder.sh/algo/api/v2/model/list", Type: "qoder"},
		LoginFlow:     true,
	},
	"qoder-cn": {
		Name:     "Qoder CN",
		BaseURL:  "https://api3.qoder.com.cn",
		Protocol: "qoder",
		Notes:    "浏览器设备码登录（COSY 签名，非 OpenAI 兼容）；Qoder 国内站推理走 /algo/api/v2/service/pro/sse/agent_chat_generation，模型目录来自 /algo/api/v2/model/list",
		FreeTier: true,
		ModelIDs: []string{
			"qoder/auto",
			"qoder/performance",
			"qoder/efficient",
			"qoder/lite",
			"qoder/qmodel_38max",
			"qoder/qmodel_latest",
			"qoder/qmodel",
			"qoder/qfmodel",
			"qoder/kmodel_latest",
			"qoder/kmodel",
			"qoder/gmodel",
			"qoder/gfmodel",
			"qoder/dmodel",
			"qoder/dfmodel",
			"qoder/mmodel",
		},
		ModelsFetcher: &modelsFetcher{URL: "https://api3.qoder.com.cn/algo/api/v2/model/list", Type: "qoder"},
		LoginFlow:     true,
	},
}

// providerPresetsImported 由 internal/providerreg 的 imported.json 提供
// （ExtremeRouter registry 导入，§7.3）。
var providerPresetsImported = providerreg.MustImported().All()
