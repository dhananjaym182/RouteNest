package main

import (
	"os"
	"path/filepath"
	"testing"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/requestlog"
)

// TestMain 把池文件与请求日志重定向到临时目录：
// 单元测试绝不读写用户真实的 .cline-accounts.json。
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "cline-proxy-test-*")
	if err != nil {
		panic(err)
	}
	oldPoolPath, oldLogsPath := accounts.PoolPath, requestlog.Path
	accounts.PoolPath = filepath.Join(tmp, ".cline-accounts.json")
	requestlog.Path = filepath.Join(tmp, ".cline-request-logs.json")

	accounts.PoolMu.Lock()
	accounts.Pool = nil // 强制从临时路径重新加载
	accounts.PoolMu.Unlock()

	code := m.Run()

	accounts.PoolPath, requestlog.Path = oldPoolPath, oldLogsPath
	os.RemoveAll(tmp)
	os.Exit(code)
}
