package routing

import (
	"log"
	"net/http"

	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/zen"
)

// hasAnyFallbackLeft 判断点名模型之后是否还有候选（决定 500 是否透传）。
func hasAnyFallbackLeft(requested, current string) bool {
	return dispatch.HasFallbackAfter(ModelFallbackChain(requested), current)
}

// ZenFailoverToCline zen 调用失败后的透明降级：改走 cline 账号池 free 模型链。
// attempted=false 表示未启用故障转移，调用方维持原错误路径。
// 注意：失败计数由 zen.Call 内部标记（每请求恰好一次），此处不再重复计数，
// 否则限流/服务错误路径 + 此处各计一次，故障转移会被过早触发。
func ZenFailoverToCline(params map[string]any, stream bool) (*http.Response, *domain.Account, error, bool) {
	cfg := zen.GetConfig()
	if !cfg.Failover {
		return nil, nil, nil, false
	}
	orig, _ := params["model"].(string)
	log.Printf("  zen failover: %q unavailable upstream, falling back to cline free pool", orig)
	params["model"] = "free"
	resp, acc, err := CallFreeClineAPI(params, stream)
	return resp, acc, err, true
}

// ClineFailoverToZen Cline 侧（含 free 池链）全部耗尽后的反向故障转移：
// 落到 opencode zen 免费模型。与 ZenFailoverToCline 方向相反，形成双向闭环——
// 任一免费上游挂掉，流量自动落到另一条。zen 未启用或处于故障转移窗口
// （连续失败被判定不可达）时不尝试；最多试 3 个免费模型，避免在坏模型上反复烧时间。
func ClineFailoverToZen(params map[string]any, stream bool) (*http.Response, bool) {
	cfg := zen.GetConfig()
	if !cfg.Enabled || zen.FailedNow() {
		return nil, false
	}
	orig, _ := params["model"].(string)
	attempts := 0
	for _, m := range zen.CurrentModels() {
		if !zen.IsFreeModel(m) || m.ID == orig {
			continue
		}
		attempts++
		if attempts > egress.FailoverModelCap() {
			break
		}
		log.Printf("  cline failover: %q exhausted, trying zen free model %q", orig, m.ID)
		params["model"] = m.ID
		resp, err := zen.Call(params, stream)
		if err == nil {
			return resp, true
		}
		log.Printf("  cline failover: zen model %q failed: %v", m.ID, err)
	}
	return nil, false
}
