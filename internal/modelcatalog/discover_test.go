package modelcatalog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseModelsShapes(t *testing.T) {
	body := []byte(`{
		"data": [{"id":" a/model ","name":"Alpha","pricing":{"prompt":"0","completion":0,"request":"0"},
		          "tags":["FREE"],"free":"true","mode":"free"},
		         {"id":"a/model","name":"duplicate"}],
		"models": [{"id":"b/model","display_name":"Beta (Free)"}]
	}`)
	entries, err := ParseModels(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	a := entries[0]
	if a.ID != "a/model" || a.Name != "Alpha" || !a.FreeFlag || a.Mode != "free" {
		t.Errorf("entry a = %+v", a)
	}
	if a.PricingPrompt == nil || *a.PricingPrompt != 0 || a.PricingRequest == nil || *a.PricingRequest != 0 {
		t.Errorf("pricing not parsed: %+v", a)
	}
	if len(a.Tags) != 1 || a.Tags[0] != "FREE" {
		t.Errorf("tags = %v", a.Tags)
	}
	if entries[1].ID != "b/model" || entries[1].Name != "Beta (Free)" {
		t.Errorf("entry b = %+v", entries[1])
	}
}

func TestParseModelsErrors(t *testing.T) {
	if _, err := ParseModels([]byte("not json")); err == nil || !strings.Contains(err.Error(), "unexpected response format") {
		t.Errorf("err = %v", err)
	}
	if _, err := ParseModels([]byte(`{"data":[]}`)); err == nil || !strings.Contains(err.Error(), "no models") {
		t.Errorf("err = %v", err)
	}
}

func TestParseModelsAlternatePricingKeys(t *testing.T) {
	body := []byte(`{
		"data": [
			{"id":"litellm/string","input_cost_per_token":"0.00000024","output_cost_per_token":"0.0000012"},
			{"id":"litellm/number","input_cost_per_token":0.00000024,"output_cost_per_token":0.0000012},
			{"id":"litellm/free","input_cost_per_token":"0","output_cost_per_token":"$0"},
			{"id":"priced/text","pricing":{"prompt_text":"0.5/1M","completion_text":"0.0 USD","request_cost":0}},
			{"id":"top/level","input_cost_per_token":"0","output_cost_per_token":0,"request_cost":"$0"},
			{"id":"junk/pricing","pricing":{"prompt":"open","completion":"free"}}
		]}`)
	entries, err := ParseModels(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.ID] = e
	}
	a := got["litellm/string"]
	if a.PricingPrompt == nil || *a.PricingPrompt != 0.00000024 || a.PricingComplet == nil || *a.PricingComplet != 0.0000012 {
		t.Errorf("litellm/string pricing = %v %v", a.PricingPrompt, a.PricingComplet)
	}
	b := got["litellm/number"]
	if b.PricingPrompt == nil || *b.PricingPrompt != 0.00000024 || b.PricingComplet == nil || *b.PricingComplet != 0.0000012 {
		t.Errorf("litellm/number pricing = %v %v", b.PricingPrompt, b.PricingComplet)
	}
	c := got["litellm/free"]
	if c.PricingPrompt == nil || *c.PricingPrompt != 0 || c.PricingComplet == nil || *c.PricingComplet != 0 {
		t.Errorf("litellm/free pricing = %v %v", c.PricingPrompt, c.PricingComplet)
	}
	d := got["priced/text"]
	if d.PricingPrompt == nil || *d.PricingPrompt != 0.5 || d.PricingComplet == nil || *d.PricingComplet != 0 || d.PricingRequest == nil || *d.PricingRequest != 0 {
		t.Errorf("priced/text pricing = %v %v %v", d.PricingPrompt, d.PricingComplet, d.PricingRequest)
	}
	e2 := got["top/level"]
	if e2.PricingPrompt == nil || *e2.PricingPrompt != 0 || e2.PricingComplet == nil || *e2.PricingComplet != 0 || e2.PricingRequest == nil || *e2.PricingRequest != 0 {
		t.Errorf("top/level pricing = %v %v %v", e2.PricingPrompt, e2.PricingComplet, e2.PricingRequest)
	}
	// Unparseable components must stay nil, not 0 ("free").
	j := got["junk/pricing"]
	if j.PricingPrompt != nil || j.PricingComplet != nil {
		t.Errorf("junk pricing should be unknown, got %v %v", j.PricingPrompt, j.PricingComplet)
	}
}

func TestRawNumHardening(t *testing.T) {
	cases := []struct {
		in   any
		want *float64
	}{
		{float64(0), ptrF(0)},
		{0.00000024, ptrF(0.00000024)},
		{1e6, ptrF(1000000)},
		{"1e6", ptrF(1000000)},
		{"$0", ptrF(0)},
		{"0 USD", ptrF(0)},
		{"$0.0 USD", ptrF(0)},
		{"0.0/1M", ptrF(0)},
		{"0.5/1M", ptrF(0.5)},
		{"$1.25/1M USD", ptrF(1.25)},
		{0, ptrF(0)},          // int
		{int64(42), ptrF(42)}, // int64
		{"open", nil},         // unparseable → unknown
		{"", nil},
		{"USD", nil},
		{"/1M", nil},
		{nil, nil},
		{true, nil},
	}
	for _, tc := range cases {
		got := rawNum(tc.in)
		if tc.want == nil {
			if got != nil {
				t.Errorf("rawNum(%v) = %v, want nil", tc.in, *got)
			}
			continue
		}
		if got == nil || *got != *tc.want {
			t.Errorf("rawNum(%v) = %v, want %v", tc.in, got, *tc.want)
		}
	}
}

func ptrF(v float64) *float64 { return &v }

func TestDiscoverSendsAuthAndParses(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("X-Custom") != "yes" {
			t.Errorf("custom header missing")
		}
		w.Write([]byte(`{"data":[{"id":"z"},{"id":"a"}]}`))
	}))
	defer srv.Close()

	entries, err := Discover(srv.Client(), srv.URL, "key-1", map[string]string{"X-Custom": "yes"}, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if gotAuth != "Bearer key-1" {
		t.Errorf("auth = %q", gotAuth)
	}
	if len(entries) != 2 || entries[0].ID != "a" || entries[1].ID != "z" {
		t.Errorf("entries not sorted: %+v", entries)
	}
}

func TestDiscoverRetriesV1On404(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"m"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	entries, err := Discover(srv.Client(), srv.URL, "", nil, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "m" {
		t.Errorf("entries = %+v", entries)
	}
	if len(paths) != 2 || paths[0] != "/models" || paths[1] != "/v1/models" {
		t.Errorf("paths = %v", paths)
	}
}

func TestDiscoverFallsBackToFetcherBase(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/catalog/models" {
			w.Write([]byte(`{"models":[{"id":"catalog/model"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	entries, err := Discover(srv.Client(), srv.URL, "", nil, srv.URL+"/catalog")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "catalog/model" {
		t.Errorf("entries = %+v", entries)
	}
	if len(paths) < 3 {
		t.Errorf("expected primary attempts then fetcher fallback, paths = %v", paths)
	}
}

func TestDiscoverErrors(t *testing.T) {
	if _, err := Discover(http.DefaultClient, "", "", nil, ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty base err = %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("x", 500), http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := Discover(srv.Client(), srv.URL, "", nil, "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("err = %v", err)
	}
	if len(err.Error()) > 260 {
		t.Errorf("error body not truncated: %d chars", len(err.Error()))
	}
}

func TestFetcherBaseFor(t *testing.T) {
	if got := FetcherBaseFor("https://opencode.ai/"); got != "https://opencode.ai/zen/v1" {
		t.Errorf("opencode fetcher base = %q", got)
	}
	if got := FetcherBaseFor("https://unknown.example/v1"); got != "" {
		t.Errorf("unknown fetcher base = %q", got)
	}
}
