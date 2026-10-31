package integration

import (
	"cline-go-proxy/internal/platform"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveDataPathRedirectsUnderTest 证明 go test 时数据文件被重定向到临时目录，
// 绝不落到仓库根目录 / exe 目录（防止 fixture 覆盖 live 配置的真实回归）。
func TestResolveDataPathRedirectsUnderTest(t *testing.T) {
	if !testing.Testing() {
		t.Fatal("expected testing.Testing() == true under go test")
	}
	for _, name := range platform.KnownDataFiles() {
		p := platform.ResolveDataPath(name, nil)
		if !strings.HasPrefix(p, os.TempDir()) {
			t.Errorf("%s resolved to %s, want under os.TempDir()", name, p)
		}
		if _, err := os.Stat(filepath.Dir(p)); err != nil {
			t.Errorf("data dir %s: %v", filepath.Dir(p), err)
		}
	}
	// 数据目录所有文件共享同一个隔离目录（cache 生效）
	if a, b := platform.ResolveDataPath(".cline-config.json", nil), platform.ResolveDataPath(".cline-accounts.json", nil); filepath.Dir(a) != filepath.Dir(b) {
		t.Errorf("shared dir: %s vs %s", filepath.Dir(a), filepath.Dir(b))
	}
}
