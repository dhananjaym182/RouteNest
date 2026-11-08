package integration

import (
	"os"
	"path/filepath"
	"routenest/internal/app"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/requestlog"
)

// TestMain 把池文件与请求日志重定向到临时目录：
// 单元测试绝不读写用户真实的 .cline-accounts.json。
func TestMain(m *testing.M) {
	_ = os.Setenv(accounts.InitializeEmptyAccountStoreEnv, "true")
	app.Configure()
	tmp, err := os.MkdirTemp("", "cline-proxy-test-*")
	if err != nil {
		panic(err)
	}
	accounts.PoolPath = filepath.Join(tmp, ".cline-accounts.json")
	requestlog.Path = filepath.Join(tmp, ".cline-request-logs.json")

	accounts.PoolMu.Lock()
	accounts.Pool = nil // 强制从临时路径重新加载
	accounts.PoolMu.Unlock()

	code := m.Run()

	// The process exits immediately below. Do not restore these globals while
	// server-owned cooldown recovery goroutines may still be reading them.
	os.RemoveAll(tmp)
	os.Exit(code)
}
