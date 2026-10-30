package providerreg

// Structural tests for the imported (ExtremeRouter-derived) preset catalog:
// every entry must parse, be named, use a valid absolute URL, and free/paid-only
// entries must not share an upstream origin.

import (
	"net/url"
	"strings"
	"testing"
)

func TestLoadImportedStructure(t *testing.T) {
	reg, err := LoadImported()
	if err != nil {
		t.Fatalf("LoadImported: %v", err)
	}
	presets := reg.All()
	if len(presets) == 0 {
		t.Fatal("imported presets empty: registry import was lost")
	}
	seenName := map[string]string{} // display Name 在前端展示层应唯一
	for key, p := range presets {
		if key == "" || p.ID != key || p.Name == "" || p.BaseURL == "" {
			t.Errorf("preset %q: key/id/name/baseURL required", key)
			continue
		}
		if !strings.Contains(p.Notes, "registry") {
			t.Errorf("preset %q: Notes %q should trace provenance (--extremerouter registry)", key, p.Notes)
		}
		if u, err := url.Parse(p.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			t.Errorf("preset %q: invalid baseURL %q (%v)", key, p.BaseURL, err)
		}
		for _, id := range p.ModelIDs {
			if id == "" {
				t.Errorf("preset %q: empty ModelID", key)
			}
		}
		if p.ModelsFetcher != nil && p.ModelsFetcher.URL == "" {
			t.Errorf("preset %q: ModelsFetcher.URL empty", key)
		}
		if other, ok := seenName[p.Name]; ok {
			t.Errorf("preset %q: display Name %q duplicates %q", key, p.Name, other)
		}
		seenName[p.Name] = key
	}
}

func TestImportedFreePaidNoOriginOverlap(t *testing.T) {
	presets := MustImported().All()
	freeSite := map[string]string{}
	host := func(base string) string {
		u, err := url.Parse(base)
		if err != nil {
			return base
		}
		return strings.TrimPrefix(u.Hostname(), "www.")
	}
	for key, p := range presets {
		if p.FreeTier {
			freeSite[host(p.BaseURL)] = key
		}
	}
	for key, p := range presets {
		if p.FreeTier {
			continue
		}
		if k, ok := freeSite[host(p.BaseURL)]; ok {
			t.Errorf("paid-only preset %q shares origin with free preset %q — tier ambiguo, remove one", key, k)
		}
	}
}
