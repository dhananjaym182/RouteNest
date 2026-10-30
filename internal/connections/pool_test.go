package connections

import (
	"path/filepath"
	"testing"
	"time"

	"cline-go-proxy/internal/protocol/openaichat"
)

// isolateConnections resets the connection store + pool state to a temp dir.
func isolateConnections(t *testing.T) {
	t.Helper()
	oldConns, oldLoaded, oldPath := connections, connectionsLoaded, FilePath
	oldCursor, oldCooling := connCursor, connCooling
	FilePath = filepath.Join(t.TempDir(), ".cline-connections.json")
	connections, connectionsLoaded = nil, true
	connCursor = map[string]int{}
	connCooling = map[string]time.Time{}
	t.Cleanup(func() {
		connections, connectionsLoaded, FilePath = oldConns, oldLoaded, oldPath
		connCursor, connCooling = oldCursor, oldCooling
	})
}

func mkActiveConn(id, email, token string) *Connection {
	return &Connection{ID: id, Provider: "testprov", AuthType: "import_token",
		Email: email, AccessToken: token, Status: "active", CreatedAt: time.Now()}
}

func findConn(t *testing.T, id string) *Connection {
	t.Helper()
	for _, c := range connections {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("connection %s not in store", id)
	return nil
}

func TestConnectionPoolRotationAndSkip(t *testing.T) {
	isolateConnections(t)
	connections = []*Connection{mkActiveConn("c1", "a@x.io", "tok-c1"), mkActiveConn("c2", "b@x.io", "tok-c2")}
	p := &CustomProvider{ID: "p1", Name: "P", BaseURL: "http://127.0.0.1:1", ConnectionIDs: []string{"c1", "c2"}}

	if got := PickConnectionForProvider(p); got == nil || got.ID != "c1" {
		t.Fatalf("first pick = %+v, want c1", got)
	}
	if got := PickConnectionForProvider(p); got == nil || got.ID != "c2" {
		t.Fatalf("second pick = %+v, want c2 (round-robin)", got)
	}
	if got := PickConnectionForProvider(p); got == nil || got.ID != "c1" {
		t.Fatalf("third pick = %+v, want c1", got)
	}

	// reauth'd connection is skipped; the sibling keeps serving.
	findConn(t, "c2").Status = "reauth_required"
	for i := 0; i < 3; i++ {
		if got := PickConnectionForProvider(p); got == nil || got.ID != "c1" {
			t.Fatalf("pick %d with dead c2 = %+v, want c1", i, got)
		}
	}

	// rate-limit cooldown parks one connection for its duration.
	findConn(t, "c2").Status = "active"
	SetConnectionCooldown("c1", time.Now().Add(ConnectionRateLimitCooldown))
	if got := PickConnectionForProvider(p); got == nil || got.ID != "c2" {
		t.Fatalf("pick with cooled c1 = %+v, want c2", got)
	}
	connCooling["c1"] = time.Now().Add(-time.Second) // expire it
	if got := PickConnectionForProvider(p); got == nil || got.ID != "c1" {
		t.Fatalf("pick after cooldown expiry = %+v, want c1", got)
	}
}

func TestConnectionPoolLegacyIDAndUnion(t *testing.T) {
	isolateConnections(t)
	connections = []*Connection{mkActiveConn("c1", "a@x.io", "t1"), mkActiveConn("c2", "b@x.io", "t2")}
	legacy := &CustomProvider{ID: "pL", ConnectionID: "c1"}
	if ids := ProviderConnIDs(legacy); len(ids) != 1 || ids[0] != "c1" {
		t.Fatalf("legacy alias ids = %v", ids)
	}
	if got := PickConnectionForProvider(legacy); got == nil || got.ID != "c1" {
		t.Fatalf("legacy pick = %+v", got)
	}
	both := &CustomProvider{ID: "pB", ConnectionIDs: []string{"c2"}, ConnectionID: "c1"}
	if ids := ProviderConnIDs(both); len(ids) != 2 {
		t.Fatalf("union ids = %v, want 2", ids)
	}
	if got := PickConnectionForProvider(both); got == nil {
		t.Fatal("union pick returned nil")
	}
	gone := &CustomProvider{ID: "pG", ConnectionID: "nope"}
	if got := PickConnectionForProvider(gone); got != nil {
		t.Fatal("missing connection must not be picked")
	}
}

func TestConnectionExpiryWithoutRefreshMarksReauth(t *testing.T) {
	isolateConnections(t)
	dead := mkActiveConn("c1", "a@x.io", "t1")
	dead.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	connections = []*Connection{dead, mkActiveConn("c2", "b@x.io", "t2")}
	p := &CustomProvider{ID: "p1", ConnectionIDs: []string{"c1", "c2"}}
	if got := PickConnectionForProvider(p); got == nil || got.ID != "c2" {
		t.Fatalf("pick = %+v, want c2", got)
	}
	if findConn(t, "c1").Status != "reauth_required" {
		t.Error("expired no-refresh connection should be marked reauth_required")
	}
	// expired WITH refresh token stays usable (refresh handled on the call path)
	live := mkActiveConn("c3", "c@x.io", "t3")
	live.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	live.RefreshToken = "r"
	p2 := &CustomProvider{ID: "p2", ConnectionIDs: []string{"c3"}}
	connections = append(connections, live)
	if got := PickConnectionForProvider(p2); got == nil || got.ID != "c3" {
		t.Fatalf("refreshable connection should stay pickable, got %+v", got)
	}
}

func TestRecordConnectionUsageRoundTrip(t *testing.T) {
	isolateConnections(t)
	connections = []*Connection{mkActiveConn("c1", "a@x.io", "t1")}
	RecordConnectionUsage("c1", "m:free", openaichat.Usage{Valid: true, Prompt: 10, Completion: 5, Total: 15, Cached: 2})
	RecordConnectionUsage("c1", "m-paid", openaichat.Usage{Valid: true, Prompt: 1, Completion: 1, Total: 2})
	RecordConnectionUsage("c1", "", openaichat.Usage{}) // no model: totals still count
	c := findConn(t, "c1")
	if c.UsageCount != 3 || c.TotalTokens != 17 || c.CachedTokens != 2 {
		t.Fatalf("totals wrong: %+v", c)
	}
	if c.ModelStats["m:free"].UsageCount != 1 || c.ModelStats["m:free"].Cost != "free" {
		t.Errorf("m:free stat wrong: %+v", c.ModelStats["m:free"])
	}
	if c.ModelStats["m-paid"].Cost != "paid" {
		t.Errorf("paid classification wrong: %+v", c.ModelStats["m-paid"])
	}
	// persistence round-trip through the store file
	connections, connectionsLoaded = nil, false
	LoadConnections()
	c = findConn(t, "c1")
	if c.UsageCount != 3 || len(c.ModelStats) != 2 {
		t.Fatalf("stats lost after reload: %+v", c)
	}
}

func TestUpsertConnectionPreservesStats(t *testing.T) {
	isolateConnections(t)
	old := mkActiveConn("c1", "a@x.io", "t1")
	old.UsageCount, old.TotalTokens = 7, 100
	old.ModelStats = map[string]*ModelStat{"m": {ModelID: "m", UsageCount: 7}}
	connections = []*Connection{old}
	re := UpsertConnection(&Connection{Provider: "testprov", Email: "a@x.io", AccessToken: "t1-new", AuthType: "device_code", Status: "active"})
	if re.ID != "c1" || re.UsageCount != 7 || re.TotalTokens != 100 || re.ModelStats["m"] == nil {
		t.Fatalf("re-login upsert lost usage history: %+v", re)
	}
}
