package connections

// ============================================================================
// Wave-4 §A: per-provider connection pooling + connection-scoped usage stats.
//
// Two capabilities live here:
//  1. Usage attribution: provider traffic previously called recordTokenUsage
//     with a nil account (no-op). Now every served response carries the
//     Connection that authenticated it, so per-model token/request stats are
//     recorded ON the connection instead of polluting Cline accounts.
//  2. Pooling: a CustomProvider may bind several connections
//     (ConnectionIDs). Requests rotate round-robin over the usable ones; a
//     connection killed by 401/403 (reauth) or 429 (timed cooldown) is skipped
//     while its siblings keep serving. Providers with the legacy single
//     ConnectionID keep working through the read-path alias in
//     ProviderConnIDs — no data migration.
// ============================================================================

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"cline-go-proxy/internal/protocol/openaichat"
)

// connPoolMu guards the round-robin cursors and the in-memory cooldown table.
// Kept separate from ConnectionsMu (the store lock) to avoid lock-ordering
// entanglement with pickers that also touch the store.
var connPoolMu sync.Mutex

// connCursor providerID -> next candidate index (round-robin position).
var connCursor = map[string]int{}

// connCooling connID -> cooldown expiry. In-memory only: a restart simply
// re-probes connections, mirroring provRecheckMiss policy for transient state.
var connCooling = map[string]time.Time{}

const ConnectionRateLimitCooldown = 2 * time.Minute

// IsFreeModel classifies a model as free for connection-scoped stats. The root
// package injects its catalog-aware implementation (isFreeModelID) at init; the
// default keeps package-local tests working without the model catalog.
var IsFreeModel = func(model string) bool {
	return strings.HasSuffix(model, ":free") || strings.Contains(model, "/free/")
}

var (
	providerRefPrunerMu sync.RWMutex
	providerRefPruner   func(connID string)
)

// RegisterProviderRefPruner installs the provider-owned cleanup callback.
// Providers import connections, so registration keeps the dependency acyclic.
func RegisterProviderRefPruner(fn func(connID string)) {
	providerRefPrunerMu.Lock()
	providerRefPruner = fn
	providerRefPrunerMu.Unlock()
}

// ProviderConnIDs returns the connection IDs bound to a provider:
// ConnectionIDs when present, else the legacy single ConnectionID.
// Unknown/duplicate IDs are tolerated here (handleProviderSave prunes them).
func ProviderConnIDs(p *CustomProvider) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range p.ConnectionIDs {
		add(id)
	}
	add(p.ConnectionID)
	return out
}

// connectionUsableLocked checks one stored connection; caller holds
// ConnectionsMu. Mirrors the historical activeConnectionForProvider semantics:
// expired without a refresh token -> marked reauth_required (persisted) and
// skipped; expired WITH a refresh token -> still returned (refresh is handled
// by ConnectionBearer/token exchange on the call path).
func connectionUsableLocked(c *Connection, now time.Time) bool {
	if c == nil || c.Status != "active" {
		return false
	}
	if c.ExpiresAt > 0 && now.UnixMilli() >= c.ExpiresAt && c.RefreshToken == "" {
		c.Status = "reauth_required"
		saveConnectionsLocked()
		return false
	}
	connPoolMu.Lock()
	defer connPoolMu.Unlock()
	if until, cool := connCooling[c.ID]; cool && now.Before(until) {
		return false
	}
	return true
}

// UsableConnectionsForProvider snapshots all currently usable connections of
// a provider in rotation order (starting at the provider's cursor). Copies
// are returned; callers never mutate the store through them.
func UsableConnectionsForProvider(p *CustomProvider) []*Connection {
	ids := ProviderConnIDs(p)
	if len(ids) == 0 {
		return nil
	}
	LoadConnections()
	now := time.Now()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	connPoolMu.Lock()
	start := connCursor[p.ID] % len(ids)
	connPoolMu.Unlock()
	out := make([]*Connection, 0, len(ids))
	for i := 0; i < len(ids); i++ {
		id := ids[(start+i)%len(ids)]
		for _, c := range connections {
			if c.ID != id {
				continue
			}
			if !connectionUsableLocked(c, now) {
				break
			}
			cp := *c
			out = append(out, &cp)
			break
		}
	}
	return out
}

// HasUsableConnection reports whether at least one bound connection is
// currently usable WITHOUT advancing the rotation cursor. Use for pure
// availability gates; only real dispatch may consume a cursor slot.
func HasUsableConnection(p *CustomProvider) bool {
	return len(UsableConnectionsForProvider(p)) > 0
}

// AdvanceConnCursor moves the provider cursor past connID in the full id list,
// so a cooled-down entry does not stall rotation for its siblings.
func AdvanceConnCursor(p *CustomProvider, connID string) {
	ids := ProviderConnIDs(p)
	if len(ids) == 0 {
		return
	}
	connPoolMu.Lock()
	defer connPoolMu.Unlock()
	for i, id := range ids {
		if id == connID {
			connCursor[p.ID] = (i + 1) % len(ids)
			return
		}
	}
}

// PickConnectionForProvider returns the next usable connection (advancing the
// round-robin cursor), or nil when the provider binds connections but none is
// usable. Replaces activeConnectionForProvider on the call path.
func PickConnectionForProvider(p *CustomProvider) *Connection {
	conns := UsableConnectionsForProvider(p)
	if len(conns) == 0 {
		if len(ProviderConnIDs(p)) > 0 {
			// All bound connections are dead: remember nothing (do not advance
			// a cursor over an empty set) but leave cooldowns intact.
			return nil
		}
		return nil
	}
	chosen := conns[0]
	AdvanceConnCursor(p, chosen.ID)
	return chosen
}

// SetConnectionCooldown parks one connection for rate-limit recovery (429).
// Auth failures (401/403) use MarkConnectionReauth instead — they need a human.
func SetConnectionCooldown(connID string, until time.Time) {
	connPoolMu.Lock()
	connCooling[connID] = until
	connPoolMu.Unlock()
}

// connectionCooldownActive reports whether a connection is currently parked.
func connectionCooldownActive(connID string) bool {
	connPoolMu.Lock()
	defer connPoolMu.Unlock()
	until, ok := connCooling[connID]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(connCooling, connID)
		return false
	}
	return true
}

// ConnectionExists helper for save-time validation of bound connection IDs.
func ConnectionExists(id string) bool {
	LoadConnections()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	for _, c := range connections {
		if c.ID == id {
			return true
		}
	}
	return false
}

// PruneConnectionRefs drops a deleted connection ID from every provider's
// binding list (both pool and legacy single field). Called by RemoveConnection.
func PruneConnectionRefs(connID string) {
	providerRefPrunerMu.RLock()
	pruner := providerRefPruner
	providerRefPrunerMu.RUnlock()
	if pruner != nil {
		pruner(connID)
	}
}

// ---------- connection-scoped usage recording ----------

// RecordConnectionUsage adds request/token counters to a connection, keyed per
// model. Unlike Account.ModelStats (free models only, a Cline quota concern),
// connection stats cover EVERY model: the whole point is showing which models
// each logged-in upstream account actually served.
func RecordConnectionUsage(connID, model string, usage openaichat.Usage) {
	if connID == "" {
		return
	}
	LoadConnections()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	for _, c := range connections {
		if c.ID != connID {
			continue
		}
		c.UsageCount++
		c.PromptTokens += usage.Prompt
		c.CompletionTokens += usage.Completion
		c.TotalTokens += usage.Total
		c.CachedTokens += usage.Cached
		if model != "" {
			if c.ModelStats == nil {
				c.ModelStats = make(map[string]*ModelStat)
			}
			st := c.ModelStats[model]
			if st == nil {
				cost := "paid"
				if IsFreeModel(model) {
					cost = "free"
				}
				st = &ModelStat{ModelID: model, Cost: cost}
				c.ModelStats[model] = st
			}
			st.UsageCount++
			st.PromptTokens += usage.Prompt
			st.CompletionTokens += usage.Completion
			st.TotalTokens += usage.Total
			st.CachedTokens += usage.Cached
		}
		saveConnectionsLocked()
		return
	}
}

// connTaggedBody ties the connection that authenticated a (200) response to
// the response itself, by wrapping its body. The tag shares the response's
// lifetime, so abandoned responses can never leak map entries.
type connTaggedBody struct {
	io.ReadCloser
	conn *Connection
}

func TagResponseConn(resp *http.Response, conn *Connection) {
	if resp != nil && conn != nil {
		resp.Body = &connTaggedBody{ReadCloser: resp.Body, conn: conn}
	}
}

func ResponseConnection(resp *http.Response) *Connection {
	if resp == nil {
		return nil
	}
	if tb, ok := resp.Body.(*connTaggedBody); ok {
		return tb.conn
	}
	return nil
}
