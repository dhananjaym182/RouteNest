package egress

import (
	"strings"
	"sync"
	"testing"
)

func TestProfileNormalize(t *testing.T) {
	profile, err := Profile{ID: " global ", Proxies: []string{" http://proxy:8080 ", "", "http://proxy:8080", "socks5://127.0.0.1:1080"}, Strategy: "bogus"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "global" || profile.Name != "global" || profile.Strategy != StrategyRoundRobin {
		t.Fatalf("normalized profile = %+v", profile)
	}
	if len(profile.Proxies) != 2 || profile.Proxies[0] != "http://proxy:8080" {
		t.Fatalf("proxies = %v", profile.Proxies)
	}
	if _, err := (Profile{ID: ""}).Normalize(); err == nil {
		t.Fatal("empty id must fail")
	}
	if _, err := (Profile{ID: "bad", Proxies: []string{"ftp://proxy:21"}}).Normalize(); err == nil {
		t.Fatal("unsupported scheme must fail")
	}
	if _, err := (Profile{ID: "bad", Proxies: []string{"http://proxy"}}).Normalize(); err == nil {
		t.Fatal("missing port must fail")
	}
}

func TestRedactProxyURLRemovesSecrets(t *testing.T) {
	got := RedactProxyURL("http://user:pass@proxy.example:8080/path?token=secret#frag")
	if got != "http://proxy.example:8080/path" {
		t.Fatalf("redacted = %q", got)
	}
	if strings.Contains(got, "user") || strings.Contains(got, "secret") {
		t.Fatalf("secret leaked: %q", got)
	}
	if got := RedactProxyURL("not a url"); got != "[invalid proxy]" {
		t.Fatalf("invalid redaction = %q", got)
	}
}

func TestRegistrySnapshotsAreRedactedDeterministicCopies(t *testing.T) {
	sourceA := &mutableSource{id: "b", profile: Profile{ID: "b", Proxies: []string{"http://u:p@b:1"}, Strategy: StrategyFill}}
	sourceZ := &mutableSource{id: "a", profile: Profile{ID: "a", Proxies: []string{"socks5://z:2"}}}
	registry, err := NewRegistry("a", sourceA, sourceZ)
	if err != nil {
		t.Fatal(err)
	}
	profiles := registry.Profiles()
	if len(profiles) != 2 || profiles[0].ID != "a" || profiles[1].ID != "b" {
		t.Fatalf("order = %+v", profiles)
	}
	if profiles[1].Proxies[0] != "http://b:1" {
		t.Fatalf("profile not redacted: %+v", profiles[1])
	}
	profiles[1].Proxies[0] = "mutated"
	sourceA.profile.Proxies[0] = "http://u:p@changed:1"
	again := registry.Profiles()
	if again[1].Proxies[0] != "http://changed:1" {
		t.Fatalf("snapshot aliases source: %+v", again[1])
	}
}

func TestRegistryRejectsBadCompositionAndSkipsInvalidSources(t *testing.T) {
	if _, err := NewRegistry("", &mutableSource{id: "a"}); err == nil {
		t.Fatal("empty global id must fail")
	}
	if _, err := NewRegistry("missing", &mutableSource{id: "a"}); err == nil {
		t.Fatal("missing global source must fail")
	}
	if _, err := NewRegistry("a", &mutableSource{id: "a"}, &mutableSource{id: "a"}); err == nil {
		t.Fatal("duplicate source must fail")
	}
	registry, err := NewRegistry("a",
		&mutableSource{id: "a", profile: Profile{ID: "a"}},
		&mutableSource{id: "broken", profile: Profile{ID: "broken", Proxies: []string{"ftp://nope:1"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("broken"); ok {
		t.Fatal("invalid profile should be skipped")
	}
	if _, ok := registry.Get("unknown"); ok {
		t.Fatal("unknown profile should be skipped")
	}
}

func TestRegistryConcurrentReads(t *testing.T) {
	registry, err := NewRegistry("a", &mutableSource{id: "a", profile: Profile{ID: "a", Proxies: []string{"http://p:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = registry.Profiles()
				_, _ = registry.Global()
			}
		}()
	}
	wg.Wait()
}

type mutableSource struct {
	id      string
	profile Profile
}

func (s *mutableSource) ProfileID() string { return s.id }
func (s *mutableSource) Snapshot() Profile {
	return s.profile.Clone()
}
