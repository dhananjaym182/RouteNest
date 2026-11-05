package zen

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"routenest/internal/domain"
	zenupstream "routenest/internal/upstream/zen"
)

// ============ 客户端身份 ============
// 规范会话格式（ses_ + 12 位 hex 时间戳 + 14 位 base62）自 2026-09-16 起
// 被免费层强校验，其他形态一律 403 FreeTierError（参考 opencode2api 的实测结论）。
// 会话按对话内容稳定派生：同一会话复用同一 upstream session，保留 prompt-cache 亲和；
// request-id 每次随机，规避请求维度的限流记账。
// 格式对齐官方客户端（sst/opencode v1.18.x）request.ts：
//   User-Agent:          opencode/<version>
//   x-opencode-project:  "global"（官方无仓库场景的静态 project id）
//   x-opencode-session:  "ses_" + 26 位标识（6 位时间 hex + 14 位 base62）
//   x-opencode-request:  "msg" + 26 位标识（消息 id）
//   x-opencode-client:   "cli"

// zenClientVersion tracks the opencode release used by the client identity.
const zenClientVersion = zenupstream.ClientVersion

// zenProviderUtilsVersions maps protocols to ai-sdk provider-utils versions.
var zenProviderUtilsVersions = zenupstream.ProviderUtilsVersions

var zenIDs = zenupstream.NewIdentifier()

// opencodeIdentifier generates an opencode-shaped identifier.
func opencodeIdentifier(prefix string, descending bool) string {
	return zenIDs.New(prefix, descending)
}

// zenConversationSeed extracts a stable conversation seed.
func zenConversationSeed(params map[string]any) string {
	return zenupstream.ConversationSeed(params)
}

var zenSessions = zenupstream.NewSessionCache()

// SessionCredentialID returns the global redacted identity used for one
// registered OpenCode session. The raw session must never leave the Zen owner.
func SessionCredentialID(session string) domain.CredentialID {
	hash := sha256.Sum256([]byte(session))
	return domain.CredentialID("opencode-session:" + hex.EncodeToString(hash[:8]))
}

var (
	zenPoolMu    sync.Mutex
	zenPoolStamp time.Time
)

// zenRefreshSessionPool installs registered upstream sessions: configured ones
// first, otherwise read-only harvesting from local clients. The free tier only
// serves sessions the upstream already knows, so this pool is what makes free
// models usable at all. Harvesting is throttled; force skips the throttle.
func zenRefreshSessionPool(force bool) int {
	zenPoolMu.Lock()
	defer zenPoolMu.Unlock()
	if !force && time.Since(zenPoolStamp) < 30*time.Second {
		return zenSessions.PoolLen()
	}
	cfg := GetConfig()
	ids := cfg.SessionIDs
	if len(ids) == 0 && cfg.AutoHarvestSessions() {
		ids = zenupstream.HarvestRegisteredSessions()
	}
	zenSessions.SetPool(ids)
	zenPoolStamp = time.Now()
	if n := zenSessions.PoolLen(); n > 0 {
		log.Printf("  zen sessions: %d registered session(s) available", n)
	}
	return zenSessions.PoolLen()
}

// zenEnsureSessionPool loads the pool once per process.
func zenEnsureSessionPool() {
	zenPoolMu.Lock()
	loaded := !zenPoolStamp.IsZero()
	zenPoolMu.Unlock()
	if !loaded {
		zenRefreshSessionPool(true)
	}
}

// zenSessionID returns a conversation-stable opencode-shaped session ID.
func zenSessionID(params map[string]any) string {
	return zenSessions.SessionID(params)
}

// zenSessionLookup maps a seed to a fixed opencode-shaped session.
func zenSessionLookup(seed []byte) string {
	return zenSessions.Lookup(seed)
}

// zenUserAgent returns the opencode User-Agent for a protocol.
func zenUserAgent(proto string) string {
	return zenupstream.UserAgent(proto)
}
