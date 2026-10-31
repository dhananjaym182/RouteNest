package routing

import "testing"

// 已知硬限制表按基名匹配：Cline 路由前缀（cline-free/ cline-pass/ google/ cline/）
// 剥掉后查表；其他 provider 前缀（z-ai/ 等）不属于 Cline 路由，整段保留。
func TestModelBaseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cline-free/gemini-3.8-flash", "gemini-3.8-flash"},
		{"cline-pass/gemini-3.8-flash", "gemini-3.8-flash"},
		{"google/gemini-3.8-flash", "gemini-3.8-flash"},
		{"cline/gemini-3.8-flash", "gemini-3.8-flash"},
		{"gemini-3.8-flash", "gemini-3.8-flash"},
		{"z-ai/glm-5.3-flash", "z-ai/glm-5.3-flash"},
		{"", ""},
	}
	for _, c := range cases {
		if got := modelBaseName(c.in); got != c.want {
			t.Errorf("modelBaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLookupClineModelMeta(t *testing.T) {
	for _, id := range []string{"gemini-3.8-flash", "google/gemini-3.8-flash", "cline-free/gemini-3.8-flash"} {
		meta, ok := LookupClineModelMeta(id)
		if !ok {
			t.Errorf("lookupClineModelMeta(%q) should hit", id)
			continue
		}
		if meta.Output != 65536 {
			t.Errorf("lookupClineModelMeta(%q).Output = %d, want 65536", id, meta.Output)
		}
		if meta.Context != 1048576 {
			t.Errorf("lookupClineModelMeta(%q).Context = %d, want 1048576", id, meta.Context)
		}
	}
	if _, ok := LookupClineModelMeta("z-ai/glm-5.3-flash"); ok {
		t.Error("unknown model should miss the meta table")
	}
}
