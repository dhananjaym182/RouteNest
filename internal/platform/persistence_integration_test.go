package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cline-go-proxy/internal/platform"
)

// chdir 临时切换工作目录（使用它的测试不可并行）。
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
}

// TestResolveDataPathEnvOverride 设置了 CLINE2API_DATA_DIR 时，
// 无论工作目录/exe 目录是否存在同名文件，数据一律落到该目录，且目录自动创建。
func TestResolveDataPathEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dir)

	// 工作目录里故意放一个同名文件，验证 env 目录仍具有最高优先级
	chdir(t, t.TempDir())
	if err := os.WriteFile(".cline-accounts.json", []byte("{}"), 0600); err != nil {
		t.Fatalf("seed cwd file: %v", err)
	}

	got := platform.ResolveDataPath(".cline-accounts.json", nil)
	if want := filepath.Join(dir, ".cline-accounts.json"); got != want {
		t.Fatalf("resolveDataPath = %q, want %q", got, want)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
}

// TestResolveDataPathIsolationUnderGoTest go test 运行时（未设置环境变量），
// cwd/exe 里哪怕存在同名文件也不会被选中：隔离目录优先生效。旧启发式
// （cwd → exe → 主目录）只在编译出的正式二进制（testing.Testing()==false）
// 中可达，行为由 TestResolveDataPathLegacyHeuristicOnBinary 手工在二进制上回归。
func TestResolveDataPathIsolationUnderGoTest(t *testing.T) {
	t.Setenv(platform.DataDirEnv, "") // 显式置空，屏蔽运行环境中可能存在的同名变量

	const name = ".cline-test-fixture.json" // 不会被迁移逻辑使用的独立文件名
	work := t.TempDir()
	chdir(t, work)

	if err := os.WriteFile(filepath.Join(work, name), []byte("{}"), 0600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	got := platform.ResolveDataPath(name, nil)
	if !strings.HasPrefix(got, platform.TestIsolatedDataDir()) {
		t.Fatalf("go test: resolveDataPath = %q, want under isolated dir %q", got, platform.TestIsolatedDataDir())
	}
}

// TestMigrateLegacyDataFiles 切换数据目录时应把旧位置已有的旧版 .cline-* 数据文件
// 拷贝为目录下的新版 .routenest-* 文件名：仅当目标不存在时拷贝
// （不覆盖已有新数据），且重复调用幂等。
func TestMigrateLegacyDataFiles(t *testing.T) {
	t.Setenv(platform.DataDirEnv, "")
	work := t.TempDir()
	chdir(t, work)
	dir := filepath.Join(work, "data")

	// 隔离旧目录探测：只把当前临时工作目录视为旧数据位置，
	// 避免 exe 目录/主目录里的同名文件（如其他测试写出的默认配置）干扰断言。
	legacyDataDirsOverride := func() []string { return []string{work} }

	// 旧版名字 → 迁移后的新数据目录内容
	legacy := map[string]string{
		".cline-accounts.json": `{"keys":["sk-old"],"accounts":[]}`,
		".cline-config.json":   `{"strategy":"fill"}`,
	}
	for name, content := range legacy {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	platform.MigrateLegacyDataFiles(dir, legacyDataDirsOverride)

	for oldName, content := range legacy {
		name := platform.CurrentDataFileName(oldName)
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("migrated file %s missing: %v", name, err)
		}
		if strings.TrimSpace(string(data)) != content {
			t.Fatalf("migrated %s = %q, want %q", name, data, content)
		}
	}

	// 已存在的目标不被覆盖
	existing := filepath.Join(dir, ".routenest-accounts.json")
	if err := os.WriteFile(existing, []byte(`{"keys":["sk-new"],"accounts":[]}`), 0600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".cline-accounts.json"), []byte(`{"keys":["sk-older"],"accounts":[]}`), 0600); err != nil {
		t.Fatalf("reseed legacy: %v", err)
	}
	platform.MigrateLegacyDataFiles(dir, legacyDataDirsOverride)
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if strings.TrimSpace(string(data)) != `{"keys":["sk-new"],"accounts":[]}` {
		t.Fatalf("existing destination was overwritten: %q", data)
	}
}

// TestWriteFileAtomic 原子写：内容正确、可覆盖旧内容、不残留 .tmp。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cline-accounts.json")

	if err := platform.WriteFileAtomic(path, []byte("v1"), 0600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := platform.WriteFileAtomic(path, []byte("v2"), 0600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "v2" {
		t.Fatalf("read back = %q, err %v; want \"v2\"", data, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file left behind: %v", err)
	}
}

// TestLoadPoolCorruptFileBackup 账号池文件损坏时：
