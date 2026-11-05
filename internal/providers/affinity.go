package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// ---- 粘贴会话：同一会话在 TTL 内钉在同一家 provider --------------------

// providerStickyTTL 粘贴会话窗口。会话中途换 provider 是客户端最直接能感知
// 的质量抖动，30 分钟窗口足以覆盖一次连续的编码会话。
const providerStickyTTL = 30 * time.Minute

type stickyPin struct {
	providerID string
	model      string
	until      time.Time // 到期时间，stickyGet 过期即视为未钉
}

var (
	stickyMu       sync.Mutex
	stickySessions = map[string]stickyPin{} // 会话键 → 钉住的 provider:model
)

// SessionKeyFromParams 从请求消息里派生会话键：取第一条 user 消息内容的
// 稳定哈希（sha256 前 16 hex）。agent 会话中首条 user 消息在整个对话过程中
// 不变，可稳定标识一次会话；无 user 消息或取不到内容时返回空串（不钉）。
func SessionKeyFromParams(params map[string]any) string {
	msgs, ok := params["messages"].([]any)
	if !ok {
		return ""
	}
	for _, msg := range msgs {
		m, ok := msg.(map[string]any)
		if !ok || m["role"] != "user" {
			continue
		}
		bb, err := json.Marshal(m["content"])
		if err != nil || len(bb) == 0 {
			return ""
		}
		sum := sha256.Sum256(bb)
		return hex.EncodeToString(sum[:])[:16]
	}
	return ""
}

func stickyGet(sk string) *stickyPin {
	stickyMu.Lock()
	pin, ok := stickySessions[sk]
	if ok && stickyExpired(pin) {
		delete(stickySessions, sk)
		ok = false
	}
	stickyMu.Unlock()
	if !ok {
		return nil
	}
	return &pin
}

// stickyExpired 到期判断（调方须已持锁）。
func stickyExpired(pin stickyPin) bool { return time.Now().After(pin.until) }

// stickySet 钉住/续期：TTL 内每次成功都被刷新，一次编码会话中途不换源。
// 顺带清扫过期条目，map 不随时间无界增长。
func stickySet(sk, providerID, model string) {
	stickyMu.Lock()
	stickySessions[sk] = stickyPin{providerID: providerID, model: model, until: time.Now().Add(providerStickyTTL)}
	for k, pin := range stickySessions {
		if stickyExpired(pin) {
			delete(stickySessions, k)
		}
	}
	stickyMu.Unlock()
}

func stickyClear(sk string) {
	stickyMu.Lock()
	delete(stickySessions, sk)
	stickyMu.Unlock()
}

// stickyClearForProvider 某家 provider 被冷却/删除后，把钉在它身上的会话解放，
// 后续请求立刻融入组内故障转移。
func stickyClearForProvider(providerID string) {
	stickyMu.Lock()
	for k, pin := range stickySessions {
		if pin.providerID == providerID {
			delete(stickySessions, k)
		}
	}
	stickyMu.Unlock()
}

// pinProviderPresent 粘贴会话命中后校验钉住的 provider 仍然可用：启用、
// 服务该模型、未冷却、未超预算。任何一条不满足都返回 nil（随后解钉转入
// 组内故障转移）。
func pinProviderPresent(providerID, model string) *CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	for _, p := range CustomProviders {
		if p.ID != providerID {
			continue
		}
		if !p.Enabled {
			return nil
		}
		serves := false
		for _, m := range p.ModelIDs {
			if m == model {
				serves = true
				break
			}
		}
		if !serves {
			return nil
		}
		if until, cool := Cooldowns[providerID+":"+model]; cool && time.Now().Before(until) {
			return nil
		}
		if providerBudgetExceeded(providerID, model, p.RpmLimit, p.DailyLimit, p.TokenLimit, p.TokenWindow) {
			return nil
		}
		cp := *cloneProvider(p)
		return &cp
	}
	return nil
}
