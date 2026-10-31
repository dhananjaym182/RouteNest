package providers

import (
	"sync"
	"time"
)

// ---- 预算上限：派发前就避开配额墙，别再拿 429 当发现手段 ----------------

var (
	budgetMu      sync.Mutex
	providerRPM   = map[string][]time.Time{}      // providerID:model → 分钟窗口内派发时间
	providerDaily = map[string]map[string]int64{} // providerID → UTC 日 → 计数
	// providerTokens token 用量：providerID → 时间键（月窗 "2006-01" / 日窗
	// "2006-01-02"）→ token 数，请求日志落盘时按实际计量归集，重启后从零学起。
	providerTokens = map[string]map[string]int64{}
)

const providerRPMWindow = time.Minute

// utcTokenKey 当前 UTC 时间键，按 token 配额窗口取粒度：daily 用整日
// （"2006-01-02"），monthly（含空/未知值）用整月（"2006-01"）。与 providerDaily
// 的日键同风格：直接按当前键计数，过期键自然废弃。
func utcTokenKey(tokenWindow string) string {
	if tokenWindow == "daily" {
		return time.Now().UTC().Format("2006-01-02")
	}
	return time.Now().UTC().Format("2006-01")
}

// providerBudgetExceeded 判断该 provider:model 是否已超配（超一票都算超）。
// rpmLimit/dailyLimit/tokenLimit 任一超限即返回 true；不超过顺带清理过期窗口数据。
// tokenWindow 为 token 配额窗口（"monthly"/"daily"，空视同 monthly）。
func providerBudgetExceeded(providerID, model string, rpmLimit, dailyLimit int, tokenLimit int64, tokenWindow string) bool {
	if rpmLimit <= 0 && dailyLimit <= 0 && tokenLimit <= 0 {
		return false
	}
	budgetMu.Lock()
	defer budgetMu.Unlock()
	if rpmLimit > 0 {
		key := providerID + ":" + model
		cut := time.Now().Add(-providerRPMWindow)
		kept := providerRPM[key][:0]
		for _, t := range providerRPM[key] {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		providerRPM[key] = kept
		if len(kept) >= rpmLimit {
			return true
		}
	}
	if dailyLimit > 0 {
		day := time.Now().UTC().Format("2006-01-02")
		if providerDaily[providerID][day] >= int64(dailyLimit) {
			return true
		}
	}
	// token 预算：本窗口（月或日）已归集 token 达上限即超配，派发前就避开
	if tokenLimit > 0 && providerTokens[providerID][utcTokenKey(tokenWindow)] >= tokenLimit {
		return true
	}
	return false
}

// recordProviderDispatch 派发即计数（成败都计入：已打出的上游请求都占配额）。
func recordProviderDispatch(providerID, model string) {
	if providerID == "" {
		return
	}
	budgetMu.Lock()
	defer budgetMu.Unlock()
	providerRPM[providerID+":"+model] = append(providerRPM[providerID+":"+model], time.Now())
	day := time.Now().UTC().Format("2006-01-02")
	if providerDaily[providerID] == nil {
		providerDaily[providerID] = map[string]int64{}
	}
	providerDaily[providerID][day]++
}

// TokenWindow 查询 provider 的 token 配额窗口；未找到或空值视同
// "monthly"。请求日志落盘归集用量时用它取窗口，避免调用方透传窗口。
func TokenWindow(providerID string) string {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	for _, p := range CustomProviders {
		if p.ID == providerID {
			if p.TokenWindow == "daily" {
				return "daily"
			}
			return "monthly"
		}
	}
	return "monthly"
}

// RecordTokenUsage 落盘请求日志时归集 token 用量（发出即消耗，不论
// 成败都按上游回报的计量累加）。tokens<0 的异常回报直接忽略。tokenWindow
// 决定时间键粒度（"daily" 用日键，其余用月键）。
func RecordTokenUsage(providerID string, tokens int64, tokenWindow string) {
	if providerID == "" || tokens < 0 {
		return
	}
	budgetMu.Lock()
	defer budgetMu.Unlock()
	if providerTokens[providerID] == nil {
		providerTokens[providerID] = map[string]int64{}
	}
	providerTokens[providerID][utcTokenKey(tokenWindow)] += tokens
}
