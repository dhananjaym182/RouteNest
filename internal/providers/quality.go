package providers

import (
	"sync"
	"time"
)

// ---- 实测成绩（quality 策略的排序依据）----------------------------------

const (
	providerScoreWindow    = time.Hour // 成绩窗口：只看最近 1 小时的表现
	providerScoreRingSize  = 24
	providerNeutralSuccess = 0.5 // 无样本时的中性成功率：不打乱配置优先级
)

type providerOutcome struct {
	at    time.Time
	ok    bool
	latMS int64
}

type providerScore struct {
	ring  [providerScoreRingSize]providerOutcome
	idx   int
	count int
}

var (
	scoreMu        sync.Mutex
	providerScores = map[string]*providerScore{} // providerID:model
)

// recordProviderOutcome 记录一次上游调用的成败与时延（成功/失败都要记，
// 成功率才有意义）。数据仅内存持有，重启后从零学起。
func recordProviderOutcome(providerID, model string, ok bool, latMS int64) {
	if providerID == "" {
		return
	}
	scoreMu.Lock()
	s := providerScores[providerID+":"+model]
	if s == nil {
		s = &providerScore{}
		providerScores[providerID+":"+model] = s
	}
	s.ring[s.idx] = providerOutcome{at: time.Now(), ok: ok, latMS: latMS}
	s.idx = (s.idx + 1) % providerScoreRingSize
	if s.count < providerScoreRingSize {
		s.count++
	}
	scoreMu.Unlock()
}

// providerScoreStats 返回窗口内 (成功率, 成功样本平均时延 ms)；无样本返回
// 中性值 (0.5, 0)。只用窗口内样本：早就废弃的历史不拖累当下的判断。
func providerScoreStats(providerID, model string) (float64, float64) {
	scoreMu.Lock()
	defer scoreMu.Unlock()
	s := providerScores[providerID+":"+model]
	if s == nil || s.count == 0 {
		return providerNeutralSuccess, 0
	}
	cut := time.Now().Add(-providerScoreWindow)
	var ok, total int
	var latSum, latN int64
	for _, o := range s.ring {
		if o.at.IsZero() || o.at.Before(cut) {
			continue
		}
		total++
		if o.ok {
			ok++
			latSum += o.latMS
			latN++
		}
	}
	if total == 0 {
		return providerNeutralSuccess, 0
	}
	ratio := float64(ok) / float64(total)
	var latAvg float64
	if latN > 0 {
		latAvg = float64(latSum) / float64(latN)
	}
	return ratio, latAvg
}
