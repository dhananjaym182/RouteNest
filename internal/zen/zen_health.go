package zen

import (
	"log"
	"sync"
	"time"
)

// ============ 限流防御状态机 ============

var (
	zenSem    chan struct{} // 并发信号量（防上游瞬时超限）
	FailCount int           // 连续失败计数
	FailUntil time.Time     // 故障转移截止时间
	StateMu   sync.Mutex
)

func rebuildZenSem() {
	n := GetConfig().MaxConcurrency
	if n <= 0 {
		n = 8
	}
	StateMu.Lock()
	zenSem = make(chan struct{}, n)
	StateMu.Unlock()
}

func markZenSuccess() {
	StateMu.Lock()
	FailCount = 0
	FailUntil = time.Time{}
	StateMu.Unlock()
}

// ResetFailover clears the failure counter and any armed failover window. It is
// for tests that must not inherit zen failure state from earlier tests.
func ResetFailover() {
	markZenSuccess()
}

func MarkFail() {
	cfg := GetConfig()
	// 故障转移关闭时不计数也不报警：否则会打出 “routing to cline pool” 的
	// 误导性日志，而实际路由（RouteModel）与降级（zenFailoverToCline）都会跳过。
	// 同时清掉已武装的窗口，确保 UI 的 “故障转移中” 状态立即消失。
	if !cfg.Failover {
		StateMu.Lock()
		FailCount = 0
		FailUntil = time.Time{}
		StateMu.Unlock()
		return
	}
	thr := cfg.FailoverCount
	if thr <= 0 {
		thr = 3
	}
	window := cfg.FailoverMinutes
	if window <= 0 {
		window = 5
	}
	StateMu.Lock()
	FailCount++
	if FailCount >= thr {
		FailUntil = time.Now().Add(time.Duration(window) * time.Minute)
		log.Printf("  zen failover armed: %d consecutive failures, routing to cline pool for %dm", FailCount, window)
	}
	StateMu.Unlock()
}

// FailedNow 当前是否处于故障转移窗口内。
func FailedNow() bool {
	StateMu.Lock()
	defer StateMu.Unlock()
	if FailUntil.IsZero() {
		return false
	}
	if time.Now().After(FailUntil) {
		FailCount = 0
		FailUntil = time.Time{}
		return false
	}
	return true
}

func rebuildZenSemLocked() {
	// Config is replaced atomically by SetConfig; read it under ConfigMu rather
	// than touching the exported variable directly.
	ConfigMu.Lock()
	n := 0
	if Config != nil {
		n = Config.MaxConcurrency
	}
	ConfigMu.Unlock()
	if n <= 0 {
		n = 8
	}
	zenSem = make(chan struct{}, n)
}
