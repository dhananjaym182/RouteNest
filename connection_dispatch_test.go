package main

// Dispatch-level integration tests for the connection pool. The store and
// rotation primitives live in internal/connections; these tests exercise them
// through the root provider call path (callProvider/recordUsage), which stays
// in package main until the provider extraction.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/deviceflow"
)

// importTestConn creates a connection in the real store and removes it on cleanup.
func importTestConn(t *testing.T, provider, token, email string) *connections.Connection {
	t.Helper()
	c, err := connections.ImportConnectionToken(provider, token, email, "")
	if err != nil {
		t.Fatalf("import connection: %v", err)
	}
	t.Cleanup(func() { connections.RemoveConnection(c.ID) })
	return c
}

func TestCallProviderConnFailoverAndTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer tok-c1":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"bad token"}}`))
		case "Bearer tok-c2":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n"))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c1 := importTestConn(t, "testprov", "tok-c1", "failover-a@x.io")
	c2 := importTestConn(t, "testprov", "tok-c2", "failover-b@x.io")
	installProviders(t, &CustomProvider{ID: "fp1", Name: "FailProv", BaseURL: srv.URL, Enabled: true,
		ConnectionIDs: []string{c1.ID, c2.ID}, ModelIDs: []string{"x"}})

	params := map[string]any{"model": "x", "messages": []any{}}
	resp, err := callProvider(&CustomProvider{ID: "fp1", Name: "FailProv", BaseURL: srv.URL, ConnectionIDs: []string{c1.ID, c2.ID}}, params, false)
	if err != nil {
		t.Fatalf("want failover to c2, got err: %v", err)
	}
	conn := connections.ResponseConnection(resp)
	if conn == nil || conn.ID != c2.ID {
		t.Fatalf("response not tagged with c2: %+v", conn)
	}
	if c1.Status != "reauth_required" {
		t.Error("401 conn should be marked reauth, sibling keeps serving")
	}
	if c2.Status != "active" {
		t.Error("working conn must stay active")
	}
	resp.Body.Close()

	// recordUsage attributes stats to the tagged connection, not any account.
	rl := RequestLog{Model: "x"}
	usage := tokenUsage{Valid: true, Prompt: 3, Completion: 4, Total: 7}
	resp2, err := callProvider(&CustomProvider{ID: "fp1", Name: "FailProv", BaseURL: srv.URL, ConnectionIDs: []string{c2.ID}}, params, false)
	if err != nil {
		t.Fatal(err)
	}
	recordUsage(nil, resp2, &rl, "x", usage)
	if rl.ConnectionID != c2.ID || rl.ConnectionEmail != "failover-b@x.io" {
		t.Errorf("reqLog stamp = %+v", rl)
	}
	if c2.UsageCount != 1 || c2.ModelStats["x"].TotalTokens != 7 {
		t.Errorf("conn stats wrong: %+v", c2)
	}
	resp2.Body.Close()
}

func TestCallProviderRotatesAcrossConnections(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n"))
	}))
	defer srv.Close()
	c1 := importTestConn(t, "testprov", "tok-c1", "rotate-a@x.io")
	c2 := importTestConn(t, "testprov", "tok-c2", "rotate-b@x.io")
	installProviders(t, &CustomProvider{ID: "rp1", Name: "RotProv", BaseURL: srv.URL, Enabled: true,
		ConnectionIDs: []string{c1.ID, c2.ID}, ModelIDs: []string{"x"}})
	p := &CustomProvider{ID: "rp1", Name: "RotProv", BaseURL: srv.URL, ConnectionIDs: []string{c1.ID, c2.ID}}
	params := map[string]any{"model": "x", "messages": []any{}}
	want := []string{"Bearer tok-c1", "Bearer tok-c2", "Bearer tok-c1", "Bearer tok-c2"}
	for i := range want {
		resp, err := callProvider(p, params, false)
		if err != nil {
			t.Fatalf("dispatch %d: %v", i, err)
		}
		tag := connections.ResponseConnection(resp)
		wantID := []string{c1.ID, c2.ID}[i%2]
		if tag == nil || tag.ID != wantID {
			t.Errorf("dispatch %d tagged %+v, want %s", i, tag, wantID)
		}
		rl := RequestLog{Model: "x"}
		recordUsage(nil, resp, &rl, "x", tokenUsage{Valid: true, Prompt: 1, Completion: 1, Total: 2})
		resp.Body.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("upstream auth order = %v, want alternation %v", got, want)
	}
	if c1.UsageCount != 2 || c2.UsageCount != 2 {
		t.Errorf("usage split c1=%d c2=%d, want 2/2", c1.UsageCount, c2.UsageCount)
	}
}

func TestCallProviderAllConnsDeadFailsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c1 := importTestConn(t, "testprov", "t1", "dead-a@x.io")
	p := &CustomProvider{ID: "d1", Name: "D", BaseURL: srv.URL, ConnectionIDs: []string{c1.ID}}
	if _, err := callProvider(p, map[string]any{"model": "x"}, false); err == nil {
		t.Fatal("single dead conn should fail")
	}
	if c1.Status != "reauth_required" {
		t.Error("final 401 should still mark reauth")
	}
	// now no usable connections left at all: fail fast, request never sent
	if _, err := callProvider(p, map[string]any{"model": "x"}, false); err == nil {
		t.Fatal("want connection unavailable error")
	}
}

func TestRemoveConnectionPrunesProviders(t *testing.T) {
	c1 := importTestConn(t, "testprov", "t1", "prune-a@x.io")
	c2 := importTestConn(t, "testprov", "t2", "prune-b@x.io")
	installProviders(t, &CustomProvider{ID: "pp1", Name: "Pr", ConnectionIDs: []string{c1.ID, c2.ID}, ConnectionID: c2.ID})

	if !connections.RemoveConnection(c2.ID) {
		t.Fatal("remove failed")
	}
	loadProviders()
	providersMu.Lock()
	p := customProviders[0]
	providersMu.Unlock()
	if p.ConnectionID != "" {
		t.Errorf("legacy field not pruned: %q", p.ConnectionID)
	}
	if len(p.ConnectionIDs) != 1 || p.ConnectionIDs[0] != c1.ID {
		t.Errorf("pool not pruned: %v", p.ConnectionIDs)
	}
}

// 派发集成：绑定连接的 provider 用连接 token + 动态组织头；401 → 标重登并落选。
func TestCallProviderUsesConnectionAndMarksReauth(t *testing.T) {
	var gotAuth, gotOrg string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOrg = r.Header.Get("X-Test-Org")
		if gotAuth == "Bearer expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()

	conn, err := connections.ImportConnectionToken("t-hdr", "good", "hdr@x.y", "org-7")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connections.RemoveConnection(conn.ID) })
	restore := setTestFlow("t-hdr", deviceflow.Spec{OrgHeader: "X-Test-Org", IntervalSec: 1})
	defer restore()
	p := &CustomProvider{ID: "p1", Name: "ConnProv", BaseURL: upstream.URL, Enabled: true, ConnectionID: conn.ID, ModelIDs: []string{"t/model"}}
	installProviders(t, p)

	resp, err := callProvider(p, map[string]any{"model": "t/model", "messages": []any{}}, false)
	if err != nil {
		t.Fatalf("callProvider: %v", err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer good" || gotOrg != "org-7" {
		t.Errorf("connection creds not injected: auth=%q org=%q", gotAuth, gotOrg)
	}

	// 过期连接 → 该 provider 不入候选
	connections.ConnectionsMu.Lock()
	conn.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	connections.ConnectionsMu.Unlock()
	if got := resolveProvidersForModel("t/model"); len(got) != 0 {
		t.Errorf("expired connection provider must be filtered from selection, got %d", len(got))
	}
	if conn.Status != "reauth_required" {
		t.Errorf("expired no-refresh connection should be reauth_required, got %s", conn.Status)
	}

	// 401 → MarkConnectionReauth：还原可用 token 但上游故意拒绝
	conn2, err := connections.ImportConnectionToken("t-hdr", "expired", "hdr@x.y", "")
	if err != nil {
		t.Fatal(err)
	}
	p.ConnectionID = conn2.ID
	_, err = callProvider(p, map[string]any{"model": "t/model", "messages": []any{}}, false)
	if err == nil {
		t.Fatal("401 should surface as error")
	}
	if conn2.Status != "reauth_required" {
		t.Errorf("401 from upstream should mark connection reauth_required, got %s", conn2.Status)
	}
}

// setTestFlow registers a device flow for the duration of a test.
func setTestFlow(provider string, spec deviceflow.Spec) func() {
	old, had := connections.ProviderDeviceFlows[provider]
	connections.ProviderDeviceFlows[provider] = spec
	return func() {
		if had {
			connections.ProviderDeviceFlows[provider] = old
		} else {
			delete(connections.ProviderDeviceFlows, provider)
		}
	}
}
