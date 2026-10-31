package accounts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/platform"
)

type Account = domain.Account
type AccountPool = domain.AccountPool

// usePoolPath 把账号池文件指向指定路径并清空内存缓存，测试结束后恢复。
func usePoolPath(t *testing.T, path string) {
	t.Helper()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.Pool, accounts.PoolPath = nil, path
	t.Cleanup(func() { accounts.Pool, accounts.PoolPath = oldPool, oldPath })
}

// 这是防「损坏 → 静默空池 → 空池写回覆盖」数据永久丢失链路的核心测试。
func TestLoadPoolCorruptFileBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cline-accounts.json")
	corrupt := `{"accounts": [ {"accountId": "a1"` // 模拟写入中途被杀留下的截断 JSON
	if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	usePoolPath(t, path)

	p := accounts.LoadPool()
	if p == nil || len(p.Accounts) != 0 || len(p.Keys) != 0 {
		t.Fatalf("expected fresh empty accounts.Pool, got %+v", p)
	}

	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected exactly 1 corrupt backup, got %d (glob err %v)", len(backups), err)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil || string(data) != corrupt {
		t.Fatalf("backup content = %q, err %v; want original %q", data, err, corrupt)
	}

	accounts.SavePool()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fresh pool file missing after save: %v", err)
	}
	data, err = os.ReadFile(backups[0])
	if err != nil || string(data) != corrupt {
		t.Fatalf("backup modified after save: %q, err %v", data, err)
	}
}

// TestPoolPersistenceRoundTrip 添加账号 → 落盘 → 丢弃内存缓存重新加载（模拟重启），数据完好。
func TestPoolPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cline-accounts.json")
	usePoolPath(t, path)

	accounts.AddAccount(&Account{AccountID: "acc-1", Email: "a@b.c", RefreshToken: "rt-1", Status: "active"})

	accounts.Pool = nil // 模拟进程重启
	p := accounts.LoadPool()
	if len(p.Accounts) != 1 || p.Accounts[0].AccountID != "acc-1" || p.Accounts[0].RefreshToken != "rt-1" {
		t.Fatalf("round-trip account mismatch: %+v", p.Accounts)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pool file: %v", err)
	}
	var check AccountPool
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("pool file is not valid JSON: %v", err)
	}
	if len(check.Accounts) != 1 {
		t.Fatalf("pool file on disk has %d accounts, want 1", len(check.Accounts))
	}
}

// TestFindCredentialsFileEnv 凭据文件路径与数据目录环境变量保持一致
// （ROUTENEST_DATA_DIR 优先，旧的 CLINE2API_DATA_DIR 仍兜底）。
func TestFindCredentialsFileEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dir)
	if got := accounts.FindCredentialsFile(); got != filepath.Join(dir, ".routenest-credentials.json") {
		t.Fatalf("findCredentialsFile = %q, want %q", got, filepath.Join(dir, ".routenest-credentials.json"))
	}
}
