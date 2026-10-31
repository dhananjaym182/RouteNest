package main

import (
	"testing"
	"time"

	"cline-go-proxy/internal/compaction"
	"cline-go-proxy/internal/zen"
)

// resetZenTestState 恢复与 zen 相关的全局可变状态，避免测试间相互污染。
func resetZenTestState(t *testing.T) {
	t.Helper()
	zen.RemoteEnabledMu.Lock()
	oldZenEnabled := zen.RemoteEnabled
	zen.RemoteEnabled = false
	zen.RemoteEnabledMu.Unlock()

	zen.ConfigMu.Lock()
	oldCfg := zen.Config
	zen.Config = nil
	zen.ConfigMu.Unlock()

	zen.StateMu.Lock()
	oldCount, oldUntil := zen.FailCount, zen.FailUntil
	zen.FailCount, zen.FailUntil = 0, time.Time{}
	zen.StateMu.Unlock()

	compaction.ResetCompactStates()

	t.Cleanup(func() {
		zen.RemoteEnabledMu.Lock()
		zen.RemoteEnabled = oldZenEnabled
		zen.RemoteEnabledMu.Unlock()
		zen.ConfigMu.Lock()
		zen.Config = oldCfg
		zen.ConfigMu.Unlock()
		zen.StateMu.Lock()
		zen.FailCount, zen.FailUntil = oldCount, oldUntil
		zen.StateMu.Unlock()
		compaction.ResetCompactStates()
	})
}
