package providers

import (
	"testing"
	"time"
)

func TestProviderPrioritySelection(t *testing.T) {
	installProviders(t,
		&CustomProvider{ID: "prov_p1", Name: "Low", BaseURL: "http://a/v1", ModelIDs: []string{"m"}, Enabled: true, Priority: 5},
		&CustomProvider{ID: "prov_p2", Name: "High", BaseURL: "http://b/v1", ModelIDs: []string{"m"}, Enabled: true, Priority: 1},
	)
	p := ResolveOne("m")
	if p == nil || p.Name != "High" {
		t.Fatalf("want High (priority 1), got %+v", p)
	}
	SetCooldown("prov_p2", "m", time.Now().Add(time.Minute))
	p = ResolveOne("m")
	if p == nil || p.Name != "Low" {
		t.Fatalf("after cooldown want Low, got %+v", p)
	}
}

func TestSplitProviderPin(t *testing.T) {
	cases := []struct{ in, bare, pin string }{
		{"deepseek-chat@MyProvider", "deepseek-chat", "MyProvider"},
		{"org/model:free@KeyP", "org/model:free", "KeyP"},
		{"a@b@c", "a@b", "c"},
		{"plain/model", "plain/model", ""},
		{"@leading", "@leading", ""},
		{"trail@", "trail@", ""},
		{"x@ ", "x@ ", ""},
	}
	for _, c := range cases {
		bare, pin := SplitPin(c.in)
		if bare != c.bare || pin != c.pin {
			t.Errorf("SplitPin(%q) = %q/%q want %q/%q", c.in, bare, pin, c.bare, c.pin)
		}
	}
}

func TestProviderStorageAndSnapshotsDeepCopy(t *testing.T) {
	installProviders(t)
	original := &CustomProvider{
		ID: "copy", Name: "Copy", BaseURL: "https://copy.test/v1", Enabled: true,
		ModelIDs: []string{"m1"}, ConnectionIDs: []string{"c1", "c2"},
		Headers: map[string]string{"X-Test": "original"},
	}
	Upsert(original)

	// Caller-owned data must not mutate storage after Upsert.
	original.ModelIDs[0] = "caller-mutated"
	original.ConnectionIDs[0] = "caller-mutated"
	original.Headers["X-Test"] = "caller-mutated"
	stored := Find("copy")
	if stored.ModelIDs[0] != "m1" || stored.ConnectionIDs[0] != "c1" || stored.Headers["X-Test"] != "original" {
		t.Fatalf("Upsert retained caller aliases: %+v", stored)
	}

	// Returned snapshots must not mutate storage either.
	stored.ModelIDs[0] = "snapshot-mutated"
	stored.ConnectionIDs[0] = "snapshot-mutated"
	stored.Headers["X-Test"] = "snapshot-mutated"
	again := Find("copy")
	if again.ModelIDs[0] != "m1" || again.ConnectionIDs[0] != "c1" || again.Headers["X-Test"] != "original" {
		t.Fatalf("Find returned aliased data: %+v", again)
	}
	listed := List()
	listed[0].ModelIDs[0] = "list-mutated"
	if Find("copy").ModelIDs[0] != "m1" {
		t.Fatal("List returned aliased model IDs")
	}
}
