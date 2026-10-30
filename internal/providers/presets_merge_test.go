package providers

// 合并语义：手写 preset 覆盖同名导入；管理端预设列表包含导入条目。
// 导入数据本身的结构性校验在 internal/providerreg（LoadImported）中完成。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergePresetsRuntimeWins(t *testing.T) {
	dst := map[string]providerPreset{"a": {Name: "runtime-a"}, "b": {Name: "runtime-b"}}
	src := map[string]providerPreset{"b": {Name: "imported-b"}, "c": {Name: "imported-c"}}
	got := mergePresets(dst, src)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got["b"].Name != "runtime-b" {
		t.Errorf("runtime preset must win on collision, got %q", got["b"].Name)
	}
	if got["a"].Name != "runtime-a" || got["c"].Name != "imported-c" {
		t.Errorf("merge lost entries: %+v", got)
	}
}

func TestHandleProviderPresetsIncludesImported(t *testing.T) {
	resp := httptest.NewRecorder()
	HandlePresets(resp, httptest.NewRequest(http.MethodGet, "/admin/api/providers/presets", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		Data struct {
			Presets []struct {
				Key      string `json:"key"`
				FreeTier bool   `json:"freeTier"`
			} `json:"presets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, p := range body.Data.Presets {
		if p.Key == "g4f-groq" {
			found = true
			if !p.FreeTier {
				t.Errorf("g4f-groq should be FreeTier")
			}
		}
	}
	if !found {
		t.Error("imported preset g4f-groq missing from /admin/api/providers/presets")
	}
}
