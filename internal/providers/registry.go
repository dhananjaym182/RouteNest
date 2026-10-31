package providers

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"routenest/internal/connections"
	"routenest/internal/platform"
	providerstore "routenest/internal/store/providers"
)

// ============================================================================
// 自定义 OpenAI 兼容 Provider
// 用户可接入任意 OpenAI 兼容上游（OpenRouter / Groq / Cerebras / Gemini 兼容层 /
// Mistral / 自建 vLLM 等）。每个 provider 声明 baseURL、apiKey、自定义请求头、
// 模型列表；请求按模型归属路由，失败后按 回退链 → 自动兜底 降级。
// providerRegistry 内存态 + 落盘（.routenest-providers.json）。
var (
	Mu              sync.Mutex
	CustomProviders []*CustomProvider
	Loaded          bool
	FilePath        string
	CooldownsPath   string
	// modelCooldownsPerProvider providerID:model → 冷却截止
	Cooldowns = map[string]time.Time{}
)

func cloneProvider(p *CustomProvider) *CustomProvider {
	if p == nil {
		return nil
	}
	cp := *p
	cp.ModelIDs = append([]string(nil), p.ModelIDs...)
	cp.ConnectionIDs = append([]string(nil), p.ConnectionIDs...)
	if p.Headers != nil {
		cp.Headers = make(map[string]string, len(p.Headers))
		for key, value := range p.Headers {
			cp.Headers[key] = value
		}
	}
	return &cp
}

func init() {
	FilePath = platform.ResolveDataPath(providerstore.FileName, nil)
	CooldownsPath = platform.ResolveDataPath(providerstore.CooldownsFileName, nil)
	connections.RegisterProviderRefPruner(PruneConnectionRefs)
}

func Load() {
	Mu.Lock()
	defer Mu.Unlock()
	if Loaded {
		return
	}
	Loaded = true
	// 冷却表在 registry 前加载：无论 provider 文件是否存在都要重启冷却记忆
	loadProviderCooldownsLocked()
	list, err := providerstore.Load(FilePath)
	if err != nil {
		platform.BackupCorruptFile(FilePath, err)
		return
	}
	CustomProviders = list
}

func SaveLocked() {
	if err := providerstore.Save(FilePath, CustomProviders); err != nil {
		log.Printf("providers save failed: %v", err)
	}
}

// PruneConnectionRefs drops a deleted connection ID from every
// provider's binding list (both pool and legacy single field).
func PruneConnectionRefs(connID string) {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	changed := false
	for _, p := range CustomProviders {
		if p.ConnectionID == connID {
			p.ConnectionID = ""
			changed = true
		}
		if len(p.ConnectionIDs) > 0 {
			kept := make([]string, 0, len(p.ConnectionIDs))
			for _, id := range p.ConnectionIDs {
				if id != connID {
					kept = append(kept, id)
				}
			}
			if len(kept) != len(p.ConnectionIDs) {
				p.ConnectionIDs = kept
				changed = true
			}
		}
	}
	if changed {
		SaveLocked()
	}
	// A vanished connection must not keep a cursor pointing out of range;
	// modulo arithmetic already tolerates that, so nothing else to reset.
}

// List 返回副本列表（按 Priority, CreatedAt 排序）。
func List() []CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	out := make([]CustomProvider, 0, len(CustomProviders))
	for _, p := range CustomProviders {
		out = append(out, *cloneProvider(p))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Find returns a copy of one provider, or nil when it does not exist.
func Find(id string) *CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	for _, p := range CustomProviders {
		if p.ID == id {
			return cloneProvider(p)
		}
	}
	return nil
}

func GenID() string {
	return "prov_" + fmt.Sprintf("%x", time.Now().UnixNano())
}

// Upsert 新增或更新（按 ID）。返回最终 provider。
func Upsert(p *CustomProvider) CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	if p.ID == "" {
		p.ID = GenID()
		p.CreatedAt = time.Now()
		found := false
		for _, e := range CustomProviders {
			if e.ID == p.ID {
				*e = *cloneProvider(p)
				found = true
				break
			}
		}
		if !found {
			CustomProviders = append(CustomProviders, cloneProvider(p))
		}
	} else {
		found := false
		for _, e := range CustomProviders {
			if e.ID == p.ID {
				*e = *cloneProvider(p)
				found = true
				break
			}
		}
		if !found {
			p.CreatedAt = time.Now()
			CustomProviders = append(CustomProviders, cloneProvider(p))
		}
	}
	SaveLocked()
	return *cloneProvider(p)
}

func Delete(id string) bool {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	for i, p := range CustomProviders {
		if p.ID == id {
			CustomProviders = append(CustomProviders[:i], CustomProviders[i+1:]...)
			SaveLocked()
			// 冷却键是 "<providerID>:<model>"，删除 provider 时要连同全部
			// 模型冷却一起清掉；同时保留裸 id 删除以兼容历史残留键。
			delete(Cooldowns, id)
			for k := range Cooldowns {
				if strings.HasPrefix(k, id+":") {
					delete(Cooldowns, k)
				}
			}
			// 领到的粘贴会话不要再粘到已删除的 provider 上
			stickyClearForProvider(id)
			saveProviderCooldownsLocked()
			return true
		}
	}
	return false
}

// ResetRoutingState clears ephemeral sticky-session and provider cooldown
// state. It is used by integration tests and is also safe for administrative
// reset flows because no persistent provider definitions are changed.
func ResetRoutingState() {
	stickyMu.Lock()
	stickySessions = map[string]stickyPin{}
	stickyMu.Unlock()

	Mu.Lock()
	for key := range Cooldowns {
		delete(Cooldowns, key)
	}
	Mu.Unlock()
}
