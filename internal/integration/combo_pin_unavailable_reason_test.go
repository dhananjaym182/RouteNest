package integration

// A combo entry pinned to a provider can be blocked for several different
// reasons. The trace must name the real one, so the operator knows whether to
// raise a limit, wait for a reset, or fix the configuration. Before this
// existed, every case was reported as "model is not served by provider".

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"routenest/internal/admin"
	"routenest/internal/combos"
	"routenest/internal/providers"
)

// exhaust records n admitted requests for model on the provider, then
// tokens usage against the last one, through the production budget store.
func exhaust(t *testing.T, id, model string, requests int, tokens int64) {
	t.Helper()
	p := providers.Find(id)
	if p == nil {
		t.Fatalf("provider %s not installed", id)
	}
	for i := 0; i < requests; i++ {
		attempt := fmt.Sprintf("%s-%s-%d", id, model, i)
		if _, err := providers.AdmitDispatch(context.Background(), *p, model, attempt, time.Now()); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
		if tokens > 0 && i == requests-1 {
			if err := providers.RecordTokenUsage(id, attempt, attempt+"-tokens", tokens); err != nil {
				t.Fatalf("record tokens: %v", err)
			}
		}
	}
}

func TestPinnedComboEntryReportsTheRealBlockReason(t *testing.T) {
	const model = "m-pin"
	cases := []struct {
		name     string
		prov     CustomProvider
		disabled bool // register the provider switched off
		unlisted bool // register the provider without the requested model
		arrange  func(t *testing.T)
		want     []string // every fragment must appear in the trace reason
	}{
		{
			name: "per-minute request limit",
			prov: CustomProvider{ID: "prov_blk1", Name: "Capped", RpmLimit: 2},
			arrange: func(t *testing.T) {
				exhaust(t, "prov_blk1", model, 2, 0)
			},
			want: []string{"Capped", "per-minute request limit", "2 of 2"},
		},
		{
			name: "daily request limit",
			prov: CustomProvider{ID: "prov_blk2", Name: "Capped", DailyLimit: 3},
			arrange: func(t *testing.T) {
				exhaust(t, "prov_blk2", model, 3, 0)
			},
			want: []string{"Capped", "daily request limit", "3 of 3", "UTC"},
		},
		{
			name: "daily token limit",
			prov: CustomProvider{ID: "prov_blk3", Name: "Capped", TokenLimit: 3_000_000, TokenWindow: "daily"},
			arrange: func(t *testing.T) {
				exhaust(t, "prov_blk3", model, 1, 3_034_885)
			},
			want: []string{"Capped", "daily token limit", "3,034,885 of 3,000,000", "UTC"},
		},
		{
			name: "monthly token limit",
			prov: CustomProvider{ID: "prov_blk4", Name: "Capped", TokenLimit: 500, TokenWindow: "monthly"},
			arrange: func(t *testing.T) {
				exhaust(t, "prov_blk4", model, 1, 500)
			},
			want: []string{"Capped", "monthly token limit", "500 of 500", "UTC"},
		},
		{
			name: "model cooling down",
			prov: CustomProvider{ID: "prov_blk5", Name: "Capped"},
			arrange: func(t *testing.T) {
				providers.SetCooldown("prov_blk5", model, time.Now().Add(5*time.Minute))
			},
			want: []string{"Capped", "cooling down", "UTC"},
		},
		{
			name:     "provider disabled",
			prov:     CustomProvider{ID: "prov_blk6", Name: "Capped"},
			disabled: true,
			want:     []string{"Capped", "disabled"},
		},
		{
			name:     "provider does not list the model",
			prov:     CustomProvider{ID: "prov_blk7", Name: "Capped"},
			unlisted: true,
			want:     []string{"Capped", "does not offer", model},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installPool(t, nil)
			installConfig(t, admin.DefaultProxyConfig())
			p := tc.prov
			p.BaseURL = "http://capped.test/v1"
			p.APIKey = "sk"
			p.Enabled = !tc.disabled
			p.ModelIDs = []string{model}
			if tc.unlisted {
				p.ModelIDs = []string{"some-other-model"}
			}
			healthy := &CustomProvider{ID: "prov_ok", Name: "Healthy", BaseURL: "http://healthy.test/v1",
				APIKey: "sk", ModelIDs: []string{model}, Enabled: true, Priority: 5}
			installProviders(t, &p, healthy)
			if tc.arrange != nil {
				tc.arrange(t)
			}

			var hosts []string
			installTransport(t, func(req *http.Request) *http.Response {
				hosts = append(hosts, req.URL.Host)
				return stubResp(req, http.StatusOK, okCompletion)
			})

			combo := ModelCombo{ID: "c", Name: "C", Enabled: true, Entries: []ComboEntry{
				{Model: model, Source: "provider", Provider: p.ID},
				{Model: model, Source: "provider", Provider: "prov_ok"},
			}}
			run := combos.RunCombo(chatParams("c"), combo, false, "")

			// The blocked provider is never called, and the combo moves on.
			for _, h := range hosts {
				if h == "capped.test" {
					t.Fatalf("blocked provider was called: hosts=%v", hosts)
				}
			}
			if run.Err != nil || len(run.Trace) != 2 {
				t.Fatalf("combo should fall through to the healthy entry: err=%v trace=%+v", run.Err, run.Trace)
			}
			first := run.Trace[0]
			if first.Outcome != "failed" && first.Outcome != "skipped" {
				t.Fatalf("first entry outcome = %q, want failed or skipped", first.Outcome)
			}
			if run.Trace[1].Outcome != "success" {
				t.Fatalf("second entry outcome = %q, want success", run.Trace[1].Outcome)
			}
			t.Logf("trace reason: %s", first.Reason)
			for _, fragment := range tc.want {
				if !strings.Contains(first.Reason, fragment) {
					t.Errorf("reason %q is missing %q", first.Reason, fragment)
				}
			}
			if !tc.unlisted && strings.Contains(first.Reason, "is not served by") {
				t.Errorf("reason %q wrongly claims the model is not served", first.Reason)
			}
			if len(first.Reason) > 200 {
				t.Errorf("reason is %d bytes; the trace truncates at 200: %q", len(first.Reason), first.Reason)
			}
		})
	}
}
