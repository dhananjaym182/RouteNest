package providers

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// The health snapshot must say when a provider is blocked by one of its own
// configured limits. A budget block is not a cooldown, so before this the
// dashboard listed "no cooldowns" for a provider that was refusing every call.
func TestProviderHealthSnapshotReportsBudgetBlocks(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	installProviders(t,
		&CustomProvider{ID: "prov_tok", Name: "Tok", TokenLimit: 3_000_000, TokenWindow: "daily", RpmLimit: 15},
		&CustomProvider{ID: "prov_rpm", Name: "Rpm", RpmLimit: 2},
		&CustomProvider{ID: "prov_day", Name: "Day", DailyLimit: 1},
		&CustomProvider{ID: "prov_mon", Name: "Mon", TokenLimit: 100},
		&CustomProvider{ID: "prov_ok", Name: "Ok", TokenLimit: 1000, TokenWindow: "daily", RpmLimit: 10},
		&CustomProvider{ID: "prov_none", Name: "None"},
	)
	admitTestUsage(t, *Find("prov_tok"), "m1", "tok-attempt", "tok-tokens", now, 3_034_885)
	for i := 0; i < 2; i++ {
		admitTestUsage(t, *Find("prov_rpm"), "fast", fmt.Sprintf("rpm-%d", i), fmt.Sprintf("rpm-tokens-%d", i), now, 1)
	}
	admitTestUsage(t, *Find("prov_rpm"), "slow", "rpm-slow", "rpm-slow-tokens", now, 1) // other model, under cap
	admitTestUsage(t, *Find("prov_day"), "m", "day-attempt", "day-tokens", now, 1)
	admitTestUsage(t, *Find("prov_mon"), "m", "mon-attempt", "mon-tokens", now, 100)
	admitTestUsage(t, *Find("prov_ok"), "m", "ok-attempt", "ok-tokens", now, 10)

	health := providerHealthSnapshot()

	tok := health["prov_tok"].Blocks
	if len(tok) != 1 || tok[0].Kind != "daily_tokens" || tok[0].Usage != 3_034_885 || tok[0].Limit != 3_000_000 || tok[0].Model != "" {
		t.Fatalf("daily token block = %+v, want one provider-wide daily_tokens block 3034885/3000000", tok)
	}
	wantReset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if !tok[0].ResetAt.Equal(wantReset) {
		t.Fatalf("daily token reset = %s, want next UTC midnight %s", tok[0].ResetAt, wantReset)
	}
	if health["prov_tok"].RecoveryAt == "" {
		t.Fatal("a blocked provider must report when it recovers")
	}

	rpm := health["prov_rpm"].Blocks
	if len(rpm) != 1 || rpm[0].Kind != "rpm" || rpm[0].Model != "fast" || rpm[0].Usage != 2 || rpm[0].Limit != 2 {
		t.Fatalf("rpm block = %+v, want exactly the saturated model 'fast' (the 'slow' model is under its cap)", rpm)
	}
	if rpm[0].ResetAt.After(now.Add(time.Minute+time.Second)) || !rpm[0].ResetAt.After(now.Add(-time.Second)) {
		t.Fatalf("rpm reset %s is not within the next minute", rpm[0].ResetAt)
	}

	if day := health["prov_day"].Blocks; len(day) != 1 || day[0].Kind != "daily_requests" {
		t.Fatalf("daily request block = %+v", day)
	}
	if mon := health["prov_mon"].Blocks; len(mon) != 1 || mon[0].Kind != "monthly_tokens" {
		t.Fatalf("monthly token block = %+v", mon)
	}

	for _, id := range []string{"prov_ok", "prov_none"} {
		if got := health[id].Blocks; got == nil || len(got) != 0 {
			t.Fatalf("%s: unblocked provider must report an empty (non-nil) list, got %#v", id, got)
		}
	}
	// 17_quota.js reads exactly these field names.
	blockJSON, err := json.Marshal(tok[0])
	if err != nil {
		t.Fatal(err)
	}
	var blockFields map[string]any
	if err := json.Unmarshal(blockJSON, &blockFields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"kind", "limit", "usage", "resetAt"} {
		if _, ok := blockFields[field]; !ok {
			t.Fatalf("block JSON %s lacks %q", blockJSON, field)
		}
	}
	if _, ok := blockFields["model"]; ok {
		t.Fatalf("a provider-wide block must omit model, got %s", blockJSON)
	}
	// The browser relies on an array, never null.
	raw, err := json.Marshal(health["prov_none"])
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if string(shape["blocks"]) != "[]" {
		t.Fatalf(`blocks JSON = %s, want []`, shape["blocks"])
	}
}

// A block ends by itself. The snapshot reads the real clock, so this admits the
// usage two UTC days ago and checks it no longer blocks today, while the same
// usage admitted now does.
func TestProviderHealthSnapshotIgnoresUsageFromPastWindows(t *testing.T) {
	installTestBudget(t, nil)
	installProviders(t,
		&CustomProvider{ID: "prov_old", Name: "Old", TokenLimit: 50, TokenWindow: "daily", DailyLimit: 1},
		&CustomProvider{ID: "prov_new", Name: "New", TokenLimit: 50, TokenWindow: "daily", DailyLimit: 1},
	)
	admitTestUsage(t, *Find("prov_old"), "m", "old-attempt", "old-tokens", time.Now().Add(-48*time.Hour), 50)
	admitTestUsage(t, *Find("prov_new"), "m", "new-attempt", "new-tokens", time.Now(), 50)

	health := providerHealthSnapshot()
	if got := health["prov_old"].Blocks; len(got) != 0 {
		t.Fatalf("usage from two days ago must not block today, got %+v", got)
	}
	if got := health["prov_old"].RecoveryAt; got != "" {
		t.Fatalf("an unblocked provider must not report a recovery time, got %q", got)
	}
	if got := health["prov_new"].Blocks; len(got) != 1 {
		t.Fatalf("usage from now must block (control), got %+v", got)
	}
}
