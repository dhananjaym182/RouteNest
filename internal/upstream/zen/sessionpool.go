package zen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// sessionIDPattern is the canonical opencode session id shape.
var sessionIDPattern = regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

// ValidSessionID reports whether id is shaped like an opencode session id.
func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

// SetPool installs the registered upstream sessions and restarts assignment.
// The slice is copied; callers may keep mutating theirs.
func (c *SessionCache) SetPool(ids []string) {
	clean := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || !ValidSessionID(id) || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pool = clean
	c.assignIdx = 0
	c.lastIdx = 0
	c.bySeed = map[string]string{}
}

// PoolLen reports how many registered sessions are installed.
func (c *SessionCache) PoolLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pool)
}

// PoolSessions returns a copy of the installed sessions, in rotation order.
func (c *SessionCache) PoolSessions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.pool...)
}

// Rotate moves to the next registered session and drops seed affinity so
// subsequent requests pick the new one. It reports false when no pool exists.
func (c *SessionCache) Rotate() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pool) == 0 {
		return "", false
	}
	next := (c.lastIdx + 1) % len(c.pool)
	c.lastIdx = next
	c.assignIdx = next
	c.bySeed = map[string]string{}
	return c.pool[next], true
}

// nextPooled hands out the next pool entry in rotation order.
func (c *SessionCache) nextPooled() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pool) == 0 {
		return NewIdentifier().New("ses", true)
	}
	idx := c.assignIdx % len(c.pool)
	c.lastIdx = idx
	c.assignIdx = (idx + 1) % len(c.pool)
	return c.pool[idx]
}

// lookupPooled pins a conversation seed to a pool entry so prompt caching keeps
// working across a multi-turn exchange.
func (c *SessionCache) lookupPooled(seed []byte) string {
	sum := sha256.Sum256(seed)
	key := hex.EncodeToString(sum[:8])
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bySeed == nil {
		c.bySeed = map[string]string{}
	}
	if id, ok := c.bySeed[key]; ok {
		return id
	}
	var id string
	if len(c.pool) == 0 {
		id = NewIdentifier().New("ses", true)
	} else {
		idx := c.assignIdx % len(c.pool)
		c.lastIdx = idx
		c.assignIdx = (idx + 1) % len(c.pool)
		id = c.pool[idx]
	}
	if len(c.bySeed) >= 1000 {
		c.bySeed = map[string]string{}
	}
	c.bySeed[key] = id
	return id
}

// sessionHarvestPaths lists the read-only locations a registered session id can
// be recovered from. RouteNest never talks to these apps at runtime; it only
// reads ids they already registered with the upstream.
func sessionHarvestPaths() []string {
	paths := []string{}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "wu", "agent_sessions"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, ".local", "share", "wu", "agent_sessions"),
			filepath.Join(home, ".local", "share", "opencode", "session"),
		)
	}
	return paths
}

type sessionMeta struct {
	NativeSessionID string `json:"native_session_id"`
	ID              string `json:"id"`
}

// HarvestRegisteredSessions collects session ids already registered upstream,
// newest first. Only ids that a real client minted are useful: a locally shaped
// id is rejected by the free tier no matter how valid it looks.
func HarvestRegisteredSessions() []string {
	type candidate struct {
		id  string
		mod int64
	}
	var found []candidate
	seen := map[string]bool{}

	for _, dir := range sessionHarvestPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			var meta sessionMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				continue
			}
			id := meta.NativeSessionID
			if id == "" {
				id = meta.ID
			}
			if !ValidSessionID(id) || seen[id] {
				continue
			}
			seen[id] = true
			var mod int64
			if info, err := e.Info(); err == nil {
				mod = info.ModTime().UnixNano()
			}
			found = append(found, candidate{id: id, mod: mod})
		}
	}

	sort.SliceStable(found, func(i, j int) bool { return found[i].mod > found[j].mod })
	out := make([]string, 0, len(found))
	for _, c := range found {
		out = append(out, c.id)
	}
	return out
}
