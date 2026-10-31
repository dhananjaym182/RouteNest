package zen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"routenest/internal/models"
)

// SyncResult is one model-catalog synchronization result.
type SyncResult struct {
	Changed  bool     `json:"changed"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	SyncedAt string   `json:"syncedAt"`
	Total    int      `json:"total"`
	Error    string   `json:"error,omitempty"`
}

const zenModelSyncInterval = 10 * time.Minute

// ============ 模型同步 ============

// SyncModels 拉取 zen 官方 /models 并全量替换 pool 中 Source=="zen" 条目：
// 计算新增/移除清单 —— 官方下架的模型自动从列表消失，不留僵尸条目。
// 自定义模型（Custom=true 或其他 Source）不受影响。
func SyncModels() SyncResult {
	res := SyncResult{SyncedAt: time.Now().Format(time.RFC3339)}
	fail := func(err error) SyncResult {
		msg := err.Error()
		// 网络类错误给出代理配置提示（opencode.ai 被网络封锁时直连必然失败）
		if strings.Contains(msg, "timeout") || strings.Contains(msg, "handshake") ||
			strings.Contains(msg, "connection refused") || strings.Contains(msg, "unreachable") ||
			strings.Contains(msg, "connectex") {
			msg += "（opencode.ai 当前网络不可达，可在管理页「上游服务 → opencode 出口代理」配置代理后重试）"
		}
		log.Printf("zen models sync failed: %v", msg)
		res.Error = msg
		return res
	}

	cfg := GetConfig()
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/models"
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	req.Header.Set("User-Agent", "opencode/"+zenClientVersion)
	req.Header.Set("x-opencode-project", "global")
	req.Header.Set("x-opencode-session", opencodeIdentifier("ses", true))
	req.Header.Set("x-opencode-client", "cli")
	// 与 chat 同路：zen 代理池 + uTLS 指纹传输层，25s 超时
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := getZenHTTPClient().Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("models API returned status %d", resp.StatusCode))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fail(err)
	}

	// The build runs under the pool lock in models.ReplaceSource, against the zen
	// records as they are at write time. Free classification uses the -free
	// suffix or the seed allowlist, duplicates are dropped, and limits come from
	// the seed table. Locked limits (MetaLocked) win over the table.
	seedFree := make(map[string]bool, len(SeedModels))
	for _, sm := range SeedModels {
		seedFree[sm.ID] = true
	}
	update, err := models.ReplaceSource(models.SourceZen, func(current []Model) []Model {
		oldZen := make(map[string]Model, len(current))
		for _, m := range current {
			oldZen[m.ID] = m
		}
		seen := make(map[string]bool)
		var remote []Model
		for _, item := range payload.Data {
			id := item.ID
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			cost := "pass"
			if strings.HasSuffix(id, "-free") || seedFree[id] {
				cost = "free"
			}
			remote = append(remote, Model{
				ID:       id,
				Provider: "opencode",
				Cost:     cost,
				Status:   "active",
				Custom:   false,
				Source:   models.SourceZen,
			})
		}
		fillMeta := func(m Model) Model {
			if om, ok := oldZen[m.ID]; ok {
				// 用户手动停用的模型在重建条目时保留标记，避免同步冲掉停用状态。
				// 能力元数据（thinking/modalities）上游不提供，同样保留旧值。
				m.Disabled = om.Disabled
				m.ThinkingLevels = om.ThinkingLevels
				m.InputModalities = om.InputModalities
				m.OutputModalities = om.OutputModalities
				if om.MetaLocked && om.Context > 0 {
					m.Context, m.Output = om.Context, om.Output
					m.MetaLocked = true
					return m
				}
			}
			for _, sm := range SeedModels {
				if sm.ID == m.ID {
					m.Context, m.Output = sm.Context, sm.Output
					return m
				}
			}
			if m.Context == 0 {
				m.Context = 200000
			}
			if m.Output == 0 {
				m.Output = 32768
			}
			return m
		}
		for i := range remote {
			remote[i] = fillMeta(remote[i])
		}
		return remote
	})
	if errors.Is(err, models.ErrEmptySource) {
		return fail(fmt.Errorf("models API returned empty list"))
	}
	res.Added = update.Added
	res.Removed = update.Removed
	res.Total = update.Total
	res.Changed = len(res.Added) > 0 || len(res.Removed) > 0

	RemoteEnabledMu.Lock()
	RemoteEnabled = true
	RemoteEnabledMu.Unlock()

	log.Printf("zen models sync: %d models, +%d added, -%d removed", res.Total, len(res.Added), len(res.Removed))
	return res
}

// StartModelsRefresher 启动定时同步（10 分钟一次，不阻塞启动）。
func StartModelsRefresher() {
	go func() {
		time.Sleep(2 * time.Second) // 错开启动高峰
		if cfg := GetConfig(); cfg.Enabled {
			SetLastModelSync(SyncModels())
		}
		ticker := time.NewTicker(zenModelSyncInterval)
		defer ticker.Stop()
		for range ticker.C {
			if cfg := GetConfig(); cfg.Enabled {
				SetLastModelSync(SyncModels())
			}
		}
	}()
}

// ============ 最近一次同步结果（管理后台展示） ============

var (
	lastZenSync    SyncResult
	lastZenSyncRan bool
	lastZenSyncMu  sync.Mutex
)

func SetLastModelSync(res SyncResult) {
	lastZenSyncMu.Lock()
	lastZenSync = res
	lastZenSyncRan = true
	lastZenSyncMu.Unlock()
}

func LastModelSync() SyncResult {
	lastZenSyncMu.Lock()
	defer lastZenSyncMu.Unlock()
	if !lastZenSyncRan {
		return SyncResult{SyncedAt: ""}
	}
	return lastZenSync
}
