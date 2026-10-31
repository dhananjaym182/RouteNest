package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func stubDataPathRuntime(t *testing.T, configDir string, configErr error) {
	t.Helper()
	oldUserConfigDir, oldIsTesting := userConfigDir, isTesting
	userConfigDir = func() (string, error) { return configDir, configErr }
	isTesting = func() bool { return false }
	migrateOnce = sync.Once{}
	t.Cleanup(func() {
		userConfigDir, isTesting = oldUserConfigDir, oldIsTesting
		migrateOnce = sync.Once{}
	})
}

func TestResolveDataPathDefaultsToUserConfigDir(t *testing.T) {
	base := t.TempDir()
	stubDataPathRuntime(t, base, nil)
	t.Setenv(DataDirEnv, "")
	t.Setenv(LegacyDataDirEnv, "")

	got := ResolveDataPath(".routenest-accounts.json", func() []string { return nil })
	want := filepath.Join(base, "routenest", ".routenest-accounts.json")
	if got != want {
		t.Fatalf("ResolveDataPath = %q, want %q", got, want)
	}
}

func TestCurrentDataDirDefaultsToUserConfigDir(t *testing.T) {
	base := t.TempDir()
	stubDataPathRuntime(t, base, nil)
	t.Setenv(DataDirEnv, "")
	t.Setenv(LegacyDataDirEnv, "")

	if got, want := CurrentDataDir(), filepath.Join(base, "routenest"); got != want {
		t.Fatalf("CurrentDataDir = %q, want %q", got, want)
	}
}

func TestResolveDataPathFailsWhenUserConfigDirUnavailable(t *testing.T) {
	stubDataPathRuntime(t, "", errors.New("config home unavailable"))
	t.Setenv(DataDirEnv, "")
	t.Setenv(LegacyDataDirEnv, "")

	defer func() {
		got := recover()
		if got == nil || !strings.Contains(got.(string), "RouteNest data directory") || !strings.Contains(got.(string), "config home unavailable") {
			t.Fatalf("panic = %v, want actionable RouteNest data-directory error", got)
		}
	}()
	_ = ResolveDataPath(".routenest-accounts.json", nil)
}

// TestResolveDataPathLegacyEnvFallback ROUTENEST_DATA_DIR 未设置时，
// 旧的 CLINE2API_DATA_DIR 仍然生效（现有部署兼容）。
func TestResolveDataPathLegacyEnvFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, "")
	t.Setenv(LegacyDataDirEnv, dir)

	legacyDirs := func() []string { return nil }
	got := ResolveDataPath(".routenest-accounts.json", legacyDirs)
	want := filepath.Join(dir, ".routenest-accounts.json")
	if got != want {
		t.Fatalf("ResolveDataPath = %q, want %q", got, want)
	}
}

// TestResolveDataPathEnvOverridesLegacyEnv ROUTENEST_DATA_DIR 设置时优先于旧变量。
func TestResolveDataPathEnvOverridesLegacyEnv(t *testing.T) {
	dir := t.TempDir()
	legacy := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	t.Setenv(LegacyDataDirEnv, legacy)

	got := ResolveDataPath(".routenest-accounts.json", func() []string { return nil })
	if want := filepath.Join(dir, ".routenest-accounts.json"); got != want {
		t.Fatalf("ResolveDataPath = %q, want %q", got, want)
	}
}

// TestResolveExistingInDirMigratesLegacySibling 同目录里只有旧版 .cline-* 文件时，
// 探测函数把它就地迁移为 .routenest-* 名称返回。
func TestResolveExistingInDirMigratesLegacySibling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cline-accounts.json"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}

	got, ok := resolveExistingInDir(dir, ".routenest-accounts.json")
	if !ok {
		t.Fatalf("resolveExistingInDir missed legacy sibling in %s", dir)
	}
	if want := filepath.Join(dir, ".routenest-accounts.json"); got != want {
		t.Fatalf("resolveExistingInDir = %q, want %q", got, want)
	}
	data, err := os.ReadFile(got)
	if err != nil || string(data) != "legacy" {
		t.Fatalf("migrated content = %q, err %v; want \"legacy\"", data, err)
	}

	// 幂等：现有新名文件不被再次迁移覆盖
	if err := os.WriteFile(got, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cline-accounts.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	_, ok = resolveExistingInDir(dir, ".routenest-accounts.json")
	if !ok {
		t.Fatal("existing current-name file should still resolve")
	}
	data, err = os.ReadFile(got)
	if err != nil || string(data) != "current" {
		t.Fatalf("idempotent check overwrote current file: %q, err %v", data, err)
	}
}

// TestMigrateLegacyDataFilesDoesNotOverwriteCurrent 二次迁移不覆盖已存在的新名文件。
func TestMigrateLegacyDataFilesDoesNotOverwriteCurrent(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".cline-credentials.json"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dst, ".routenest-credentials.json")
	if err := os.WriteFile(current, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}

	MigrateLegacyDataFiles(dst, func() []string { return []string{src} })
	data, err := os.ReadFile(current)
	if err != nil || string(data) != "current" {
		t.Fatalf("existing current file was overwritten: %q, err %v", data, err)
	}
}

func TestMigrateDataFilesCopiesCurrentStateAndPreservesSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	name := ".routenest-connections.json"
	sourcePath := filepath.Join(src, name)
	content := []byte(`{"connections":[{"id":"conn-1"}]}`)
	if err := os.WriteFile(sourcePath, content, 0600); err != nil {
		t.Fatal(err)
	}

	MigrateDataFiles(dst, func() []string { return []string{src} })

	got, err := os.ReadFile(filepath.Join(dst, name))
	if err != nil || string(got) != string(content) {
		t.Fatalf("migrated content = %q, err %v", got, err)
	}
	if source, err := os.ReadFile(sourcePath); err != nil || string(source) != string(content) {
		t.Fatalf("source was removed or changed: %q, err %v", source, err)
	}
}

func TestMigrateDataFilesIncludesProviderStateAddedAfterLegacyList(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	for _, name := range []string{".routenest-providers-cooldowns.json", ".cline-providers-sync.json"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}

	MigrateDataFiles(dst, func() []string { return []string{src} })

	for _, name := range []string{".routenest-providers-cooldowns.json", ".routenest-providers-sync.json"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("missing migrated %s: %v", name, err)
		}
	}
}

func TestMigrateDataFilesDoesNotOverwriteCurrentDestination(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	name := ".routenest-connections.json"
	if err := os.WriteFile(filepath.Join(src, name), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, name), []byte("destination"), 0600); err != nil {
		t.Fatal(err)
	}

	MigrateDataFiles(dst, func() []string { return []string{src} })

	got, err := os.ReadFile(filepath.Join(dst, name))
	if err != nil || string(got) != "destination" {
		t.Fatalf("destination = %q, err %v; existing data must win", got, err)
	}
}

func TestEnsurePrivateDirTightensExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions use ACLs rather than Unix mode bits")
	}
	dir := filepath.Join(t.TempDir(), "routenest")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("directory mode = %04o, want 0700", got)
	}
}

func TestSecureDataPermissionsTightensKnownFilesOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions use ACLs rather than Unix mode bits")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, ".routenest-accounts.json")
	unrelated := filepath.Join(dir, "notes.txt")
	for _, path := range []string{state, unrelated} {
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := SecureDataPermissions(dir); err != nil {
		t.Fatal(err)
	}
	stateInfo, _ := os.Stat(state)
	otherInfo, _ := os.Stat(unrelated)
	if got := stateInfo.Mode().Perm(); got != 0600 {
		t.Fatalf("state mode = %04o, want 0600", got)
	}
	if got := otherInfo.Mode().Perm(); got != 0644 {
		t.Fatalf("unrelated mode = %04o, want unchanged 0644", got)
	}
}
