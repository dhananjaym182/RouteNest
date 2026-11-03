package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/modelcatalog"
	"routenest/internal/models"
	"routenest/internal/zen"
	"strings"
	"sync"
	"time"
)

type model = domain.Model

// clineRecommendedModelsURL 是 Cline 官方的「推荐/免费模型」接口（无需认证）。
// 参考 docs/model-api.md：Cline 4.1.15 的 Free Models 由该接口直接返回。
// var 便于测试注入 httptest 假服务。
var clineRecommendedModelsURL = "https://api.cline.bot/api/v1/ai/cline/recommended-models"

const modelSyncTimeout = 10 * time.Second

// clineModelSyncInterval 是 Cline 推荐模型的周期重同步间隔。
//
// 上游（Cline 官方）与各 provider 的免费/推荐模型列表每日都会调整：只在进程
// 启动时同步一次，长期运行的服务会继续拿着已下架的旧列表路由请求——写死在
// 配置里的模型随之下架失效，每个请求都要先撞一次必败的上游往返。
// 单次同步只是一个 10s 超时的 GET，每小时一次的成本可忽略；管理后台仍可手动触发。
const clineModelSyncInterval = time.Hour

// clineRemoteModel 对应接口返回的单个模型字段。
type clineRemoteModel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	ContextWin  int      `json:"contextWindow"`
	MaxInput    int      `json:"maxInputTokens"`
	MaxTokens   int      `json:"maxTokens"`
	ReleaseDate string   `json:"releaseDate"`
	Family      string   `json:"family"`
}

// clineRecommendedResponse 对应接口返回结构：recommended / free / clinePass 三个数组。
type clineRecommendedResponse struct {
	Recommended []clineRemoteModel `json:"recommended"`
	Free        []clineRemoteModel `json:"free"`
	ClinePass   []clineRemoteModel `json:"clinePass"`
}

// SyncResult 是一次模型同步的结果（供管理后台弹窗展示）。
type SyncResult struct {
	Changed  bool     `json:"changed"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	SyncedAt string   `json:"syncedAt"`
	Total    int      `json:"total"`
	Error    string   `json:"error,omitempty"`
}

var (
	SyncMu        sync.Mutex
	lastModelSync SyncResult
	SyncRan       bool // 启动后是否已同步过（避免重复）
	modelSyncBusy bool // 同步进行中（防并发触发）
)

// remoteModelsEnabled 远程同步成功后置 true：此后 getAllModels 以远程模型为主，
// 内置硬编码模型（已失效）仅作为离线 fallback。
var (
	remoteModelsEnabled   bool
	remoteModelsEnabledMu sync.Mutex
)

// fetchClineRecommendedModels 拉取并解析 Cline 官方推荐模型接口。
func fetchClineRecommendedModels() (clineRecommendedResponse, error) {
	// 复用全局 transport：模型同步与 Cline 对话同源（api.cline.bot），共用出口代理
	client := &http.Client{Timeout: modelSyncTimeout, Transport: egress.Transport}
	resp, err := client.Get(clineRecommendedModelsURL)
	if err != nil {
		return clineRecommendedResponse{}, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return clineRecommendedResponse{}, fmt.Errorf("models API returned status %d", resp.StatusCode)
	}

	var data clineRecommendedResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return clineRecommendedResponse{}, fmt.Errorf("decode models: %w", err)
	}
	return data, nil
}

// ModelMeta 是已知模型的上下文窗口 / 最大输出 token 元数据。
type ModelMeta struct {
	Context int
	Output  int
}

// clineModelMeta 是已知 Cline 侧模型的硬限制表（按模型基名匹配）。
// Cline 官方 recommended-models 接口不返回 contextWindow/maxTokens 元数据，
// 只能按实测维护；上游网关按路由（vertex/google）强制校验 maxOutputTokens。
// gemini-3.8-flash：vertex 路由实测支持范围 1~65536 —— maxOutputTokens=128000
// 会被直接 400 invalid argument（2026-09 实测，含 google 路由的同源报错）。
var clineModelMeta = map[string]ModelMeta{
	"gemini-3.8-flash": {Context: 1048576, Output: 65536},
}

// clineModelPrefixes 是 Cline 侧模型 ID 的路由前缀（基名之前的限定段）。
var clineModelPrefixes = []string{"cline-free/", "cline-pass/", "google/", "cline/"}

// modelBaseName 去掉模型 ID 的路由前缀得到基名：
// cline-free/gemini-3.8-flash / google/gemini-3.8-flash → gemini-3.8-flash。
func modelBaseName(id string) string {
	id = strings.TrimSpace(id)
	if idx := strings.Index(id, "/"); idx > 0 {
		base := id[idx+1:]
		for _, p := range clineModelPrefixes {
			if strings.HasPrefix(id, p) {
				return base
			}
		}
	}
	return id
}

// LookupClineModelMeta 按基名查已知模型元数据；未收录时 ok=false。
func LookupClineModelMeta(id string) (ModelMeta, bool) {
	meta, ok := clineModelMeta[modelBaseName(id)]
	return meta, ok
}

// remoteProvider 从模型 ID 前缀推断 provider，无前缀时归为 "cline"。
func remoteProvider(id string) string {
	if idx := strings.Index(id, "/"); idx > 0 {
		return id[:idx]
	}
	return "cline"
}

// syncClineModels 执行一次模型同步并持久化：
//  1. 拉取远程推荐模型（free / clinePass / recommended）
//  2. 与池中现有 remote 模型比较，得到 added / removed
//  3. 更新 AccountPool.Models（替换 Source=remote 的旧条目），保存
//  4. 记录 lastModelSync 供管理后台弹窗
//
// 任何一步失败都会把错误写进 lastModelSync，不阻塞服务启动。
func syncClineModels() SyncResult {
	SyncMu.Lock()
	if modelSyncBusy {
		SyncMu.Unlock()
		return lastModelSync
	}
	modelSyncBusy = true
	SyncMu.Unlock()
	defer func() { SyncMu.Lock(); modelSyncBusy = false; SyncMu.Unlock() }()

	res := SyncResult{SyncedAt: time.Now().Format(time.RFC3339)}
	fail := func(err error) SyncResult {
		log.Printf("models sync failed: %v", err)
		res.Error = err.Error()
		SyncMu.Lock()
		lastModelSync = res
		SyncMu.Unlock()
		return res
	}

	data, err := fetchClineRecommendedModels()
	if err != nil {
		return fail(err)
	}

	// The rebuild runs under the pool lock in models.ReplaceSource, against the
	// records as they are at write time. Free models come first, and duplicates
	// are dropped.
	now := time.Now().Unix()
	update, err := models.ReplaceSource(models.SourceRemote, func(current []domain.Model) []domain.Model {
		oldRemote := make(map[string]model, len(current))
		for _, m := range current {
			oldRemote[m.ID] = m
		}
		var remote []model
		seen := make(map[string]bool)
		addGroup := func(group []clineRemoteModel, inFree bool) {
			for _, m := range group {
				if m.ID == "" || seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				tier, signal := modelcatalog.Classify(modelcatalog.Entry{
					ID: m.ID, Name: m.Name, Tags: m.Tags, FreeFlag: inFree,
				}, false, nil)
				entry := model{
					ID:            m.ID,
					Provider:      remoteProvider(m.ID),
					Cost:          tier,
					Status:        "active",
					Custom:        false,
					Source:        models.SourceRemote,
					TierSignal:    signal,
					TierDecidedAt: now,
				}
				// 用户手动停用的模型（Disabled）在重建条目时保留标记，
				// 避免周期同步把停用状态冲掉（与 MetaLocked 同思路）。
				// 能力元数据（thinking/modalities）上游不提供，同样保留旧值。
				if old, ok := oldRemote[m.ID]; ok {
					entry.Disabled = old.Disabled
					entry.ThinkingLevels = old.ThinkingLevels
					entry.InputModalities = old.InputModalities
					entry.OutputModalities = old.OutputModalities
					if old.TierSignal == "manual" {
						entry.Cost, entry.TierSignal, entry.TierDecidedAt = old.Cost, old.TierSignal, old.TierDecidedAt
					} else if old.Cost == entry.Cost && old.TierSignal == entry.TierSignal && old.TierDecidedAt != 0 {
						entry.TierDecidedAt = old.TierDecidedAt
					}
				}
				// 远程接口不带 context/maxTokens：按已知硬限制表补全，
				// 用户锁定过的值（MetaLocked）优先于表；未收录的模型默认 1M
				// 上下文（主流模型现状，与 zen 同步的默认一致，仅展示与编辑
				// 预填，不参与压缩/封顶）。Output 保持 0=未知，不引入意外封顶。
				if old, ok := oldRemote[m.ID]; ok && old.MetaLocked {
					entry.Context, entry.Output, entry.MetaLocked = old.Context, old.Output, true
				} else if meta, ok := LookupClineModelMeta(m.ID); ok {
					entry.Context, entry.Output = meta.Context, meta.Output
				} else if entry.Context == 0 {
					entry.Context = 1048576
				}
				remote = append(remote, entry)
			}
		}
		addGroup(data.Free, true)
		addGroup(data.ClinePass, false)
		addGroup(data.Recommended, false)
		return remote
	})
	if errors.Is(err, models.ErrEmptySource) {
		return fail(fmt.Errorf("models API returned empty list"))
	}
	res.Added = update.Added
	res.Removed = update.Removed
	res.Total = update.Total
	res.Changed = len(res.Added) > 0 || len(res.Removed) > 0

	remoteModelsEnabledMu.Lock()
	remoteModelsEnabled = true
	remoteModelsEnabledMu.Unlock()

	SyncMu.Lock()
	lastModelSync = res
	SyncRan = true
	SyncMu.Unlock()

	log.Printf("models sync: %d models, +%d added, -%d removed",
		res.Total, len(res.Added), len(res.Removed))
	return res
}

// TriggerModelSync 供管理后台手动触发同步；非阻塞等待完成并返回结果。
func TriggerModelSync() SyncResult {
	return syncClineModels()
}

// IsModelGoneError reports whether an upstream response means the model no
// longer exists. The classifier lives in zen, which uses it on its own requests.
func IsModelGoneError(status int, body string) bool {
	return zen.IsModelGoneError(status, body)
}

// MarkModelGone removes a delisted model that upstream reports as gone. See
// zen.MarkModelGone.
func MarkModelGone(model string) {
	zen.MarkModelGone(model)
}

// GetModelSyncResult 返回最近一次同步结果（供管理后台展示）。
func GetModelSyncResult() SyncResult {
	SyncMu.Lock()
	defer SyncMu.Unlock()
	if !SyncRan {
		return SyncResult{SyncedAt: ""}
	}
	return lastModelSync
}

// StartModelSync 在服务启动时异步同步一次（不阻塞启动），
// 之后按 clineModelSyncInterval 周期重同步，让长期运行的进程跟上模型上下架。
func StartModelSync() {
	go func() {
		if !SyncRan {
			syncClineModels()
		}
		runModelSyncLoop(nil, clineModelSyncInterval, syncClineModels)
	}()
}

// runModelSyncLoop 按 interval 周期执行 sync，stop 关闭后返回（测试注入短间隔用）。
func runModelSyncLoop(stop <-chan struct{}, interval time.Duration, sync func() SyncResult) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			sync()
		}
	}
}

// RemoteModelsActive 返回远程模型是否已启用（同步成功过）。
func RemoteModelsActive() bool {
	remoteModelsEnabledMu.Lock()
	defer remoteModelsEnabledMu.Unlock()
	return remoteModelsEnabled
}
