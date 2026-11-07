package routing

import (
	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/protocol/openaichat"
)

type TokenUsage = openaichat.Usage

func RecordTokenUsage(acc *domain.Account, model string, usage TokenUsage) {
	if acc == nil || !usage.Valid {
		return
	}

	isFree := model != "" && IsFreeModelID(model)
	accounts.PoolMu.Lock()
	acc.PromptTokens += usage.Prompt
	acc.CompletionTokens += usage.Completion
	acc.TotalTokens += usage.Total
	acc.CachedTokens += usage.Cached

	if isFree {
		if acc.ModelStats == nil {
			acc.ModelStats = make(map[string]*domain.ModelStat)
		}
		st := acc.ModelStats[model]
		if st == nil {
			st = &domain.ModelStat{ModelID: model, Cost: "free"}
			acc.ModelStats[model] = st
		}
		st.UsageCount++
		st.PromptTokens += usage.Prompt
		st.CompletionTokens += usage.Completion
		st.TotalTokens += usage.Total
		st.CachedTokens += usage.Cached
	}
	accounts.SavePoolLocked()
	accounts.PoolMu.Unlock()
}
