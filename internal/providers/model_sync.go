package providers

// ============================================================================
// §W3-C 已启用 Provider 模型清单后台自动检测（对齐 Cline/zen 同步语义）：
// 周期性拉取每个启用 provider 的上游 /models——
//   - provider 声明且池内有条目、上游连续 2 次缺失 → 打 Delisted 标记（不删；
//     实测下架后往往仍可用，回退链照常尝试，直到上游明确报「模型不存在」由
//     markModelGone 清理）；重新出现时标记自动清除。
//   - 上游新增模型 → 绝不静默导入：分类后放入 §9.3 预览批次队列（Auto 批次，
//     每 provider 至多一个，重复检测时刷新），管理员勾选批准后才写入池。
// 展示状态（最近检测/错误/计数）落盘 .routenest-providers-sync.json；连续缺失
// 计数只存内存——重启后重新数满 2 次才下架，天然防单次抖动误标。
// ============================================================================

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/modelcatalog"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/store/syncstate"
)

const (
	providerRecheckInterval  = 6 * time.Hour
	providerRecheckMissLimit = 2 // 连续缺失次数阈值
	providerRecheckBootDelay = 2 * time.Minute
	providerRecheckGap       = 2 * time.Second // 相邻 provider 检测间隔，避免同时打同一上游
)

type providerRecheckState = syncstate.State

var (
	provRecheckMu        sync.Mutex
	providerRecheckStat  = map[string]*providerRecheckState{} // providerID → 展示状态
	provRecheckMiss      = map[string]map[string]int{}        // providerID → modelID → 连续缺失次数（内存）
	provRecheckLoaded    bool
	provRecheckBusy      bool
	providerSyncFilePath = ""
)

func init() { providerSyncFilePath = platform.ResolveDataPath(syncstate.FileName, nil) }

func LoadRecheckState() {
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if provRecheckLoaded {
		return
	}
	provRecheckLoaded = true
	persisted, err := syncstate.Load(providerSyncFilePath)
	if err != nil {
		platform.BackupCorruptFile(providerSyncFilePath, err)
		return
	}
	for id, st := range persisted {
		if st != nil {
			providerRecheckStat[id] = st
		}
	}
}

func saveProviderRecheckStateLocked() {
	out := make(map[string]providerRecheckState, len(providerRecheckStat))
	for id, st := range providerRecheckStat {
		out[id] = *st
	}
	if err := syncstate.Save(providerSyncFilePath, out); err != nil {
		log.Printf("provider sync state save failed: %v", err)
	}
}

// recheckCreds 检测用的只读凭据，与 Call 的连接注入逻辑同源：
// APIKey/自定义头 + 绑定连接时的连接 token（含刷新/兑换）与动态组织头。
// 绑定了连接但连接不可用（过期/需重登）返回 ok=false，跳过本轮检测。
func recheckCreds(p *CustomProvider) (apiKey string, headers map[string]string, ok bool) {
	headers = map[string]string{}
	for k, v := range p.Headers {
		if v != "" {
			headers[k] = v
		}
	}
	apiKey = p.APIKey
	if ids := connections.ProviderConnIDs(p); len(ids) == 0 {
		return apiKey, headers, true
	}
	// Any pooled connection works for catalog reads; the picker rotates.
	conn := connections.PickConnectionForProvider(p)
	if conn == nil {
		return "", nil, false
	}
	if spec, hasSpec := connections.ProviderDeviceFlows[conn.Provider]; hasSpec {
		apiKey = connections.ConnectionBearer(conn, spec)
		if spec.OrgHeader != "" {
			if oid := conn.ProviderSpecificData["orgId"]; oid != "" {
				headers[spec.OrgHeader] = oid
			}
		}
	} else {
		apiKey = conn.AccessToken
	}
	return apiKey, headers, true
}

// recheckProviderModels 对单个 provider 检测一轮：下架标记 + 新模型入队。
// 返回本轮展示状态副本。上游失败只记 error，不动缺失计数与标记。
func recheckProviderModels(p CustomProvider) providerRecheckState {
	LoadRecheckState()
	st := providerRecheckState{}
	apiKey, headers, ok := recheckCreds(&p)
	if !ok {
		st.LastError = "connection unavailable (expired or reauth required)"
		setProviderRecheckState(p.ID, st)
		return st
	}
	entries, err := discoverProviderModelEntries(&p, apiKey, headers)
	if err != nil {
		st.LastError = err.Error()
		setProviderRecheckState(p.ID, st)
		return st
	}

	upstream := make(map[string]bool, len(entries))
	for _, e := range entries {
		upstream[e.ID] = true
	}
	declared := make(map[string]bool, len(p.ModelIDs))
	for _, id := range p.ModelIDs {
		if id != "" {
			declared[id] = true
		}
	}

	// 1) 声明模型的缺失计数（连续 2 次才下架；出现即清零并撤标记）
	provRecheckMu.Lock()
	miss := provRecheckMiss[p.ID]
	if miss == nil {
		miss = map[string]int{}
		provRecheckMiss[p.ID] = miss
	}
	toMark, counting := modelcatalog.TrackMissing(p.ModelIDs, upstream, miss, providerRecheckMissLimit)
	provRecheckMu.Unlock()

	// 2) 写池：打/撤 Delisted（只动 Source=provider 且在本 provider 声明内的条目）
	marked := 0
	accounts.LoadPool() // ensure the pool is loaded before locking
	accounts.PoolMu.Lock()
	for i := range accounts.Pool.Models {
		m := &accounts.Pool.Models[i]
		if m.Source != "provider" || !declared[m.ID] {
			continue
		}
		if containsStr(toMark, m.ID) {
			if !m.Delisted {
				m.Delisted = true
				marked++
			}
		} else if upstream[m.ID] && m.Delisted {
			m.Delisted = false
		}
	}
	alreadyMarked := 0
	for _, m := range accounts.Pool.Models {
		if m.Source == "provider" && m.Delisted && declared[m.ID] {
			alreadyMarked++
		}
	}
	accounts.PoolMu.Unlock()
	if marked > 0 {
		accounts.SavePool()
	}

	// 3) 新上游模型 → Auto 预览批次（不在声明里也不在池里；每 provider 至多一个待审批次）
	poolHas := map[string]bool{}
	accounts.PoolMu.Lock()
	for _, m := range accounts.Pool.Models {
		poolHas[m.ID] = true
	}
	accounts.PoolMu.Unlock()
	fresh := modelcatalog.FilterNew(entries, declared, poolHas)
	batchID, queued := "", 0
	if len(fresh) > 0 {
		importsMu.Lock()
		for id, old := range pendingImports {
			if old.Auto && old.ProviderID == p.ID {
				delete(pendingImports, id)
			}
		}
		importsMu.Unlock()
		batch := buildImportBatch(p, fresh)
		batch.Auto = true
		for _, it := range batch.Items {
			if it.Action == "add" {
				queued++
			}
		}
		if queued == 0 {
			importsMu.Lock()
			delete(pendingImports, batch.ID)
			importsMu.Unlock()
		} else {
			batchID = batch.ID
		}
	}

	st = providerRecheckState{
		LastCheckedMs:  time.Now().UnixMilli(),
		DelistedMarked: alreadyMarked,
		MissCounting:   counting,
		NewQueued:      queued,
		BatchID:        batchID,
	}
	setProviderRecheckState(p.ID, st)
	if marked > 0 || queued > 0 {
		log.Printf("provider recheck %s (%s): +%d delisted-marked, %d new queued for review", p.ID, p.Name, marked, queued)
	}
	return st
}

func setProviderRecheckState(providerID string, st providerRecheckState) {
	LoadRecheckState()
	cp := st
	provRecheckMu.Lock()
	if prev, ok := providerRecheckStat[providerID]; ok && st.LastCheckedMs == 0 {
		cp.LastCheckedMs = prev.LastCheckedMs // 失败轮保留上次成功时间
	}
	providerRecheckStat[providerID] = &cp
	saveProviderRecheckStateLocked()
	provRecheckMu.Unlock()
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// clearProviderRecheck provider 删除时清掉其检测状态与待审 Auto 批次。
func clearProviderRecheck(providerID string) {
	provRecheckMu.Lock()
	delete(providerRecheckStat, providerID)
	delete(provRecheckMiss, providerID)
	saveProviderRecheckStateLocked()
	provRecheckMu.Unlock()
	importsMu.Lock()
	for id, b := range pendingImports {
		if b.Auto && b.ProviderID == providerID {
			delete(pendingImports, id)
		}
	}
	importsMu.Unlock()
}

// RunAllRechecks 对全部启用 provider 顺序检测一轮（单飞：并发触发时后来的直接跳过）。
func RunAllRechecks() map[string]providerRecheckState {
	LoadRecheckState()
	provRecheckMu.Lock()
	if provRecheckBusy {
		provRecheckMu.Unlock()
		return nil
	}
	provRecheckBusy = true
	provRecheckMu.Unlock()
	defer func() {
		provRecheckMu.Lock()
		provRecheckBusy = false
		provRecheckMu.Unlock()
	}()

	out := map[string]providerRecheckState{}
	for _, p := range List() {
		if !p.Enabled {
			continue
		}
		out[p.ID] = recheckProviderModels(p)
		time.Sleep(providerRecheckGap)
	}
	return out
}

// RecheckOne 手动触发单个 provider 检测。
func RecheckOne(providerID string) (providerRecheckState, error) {
	for _, p := range List() {
		if p.ID == providerID {
			return recheckProviderModels(p), nil
		}
	}
	return providerRecheckState{}, fmt.Errorf("provider not found")
}

// SyncStatusSnapshot returns the persisted/recent recheck state for every
// enabled provider, including an empty entry for providers not checked yet.
func SyncStatusSnapshot() map[string]any {
	LoadRecheckState()
	provRecheckMu.Lock()
	out := make(map[string]providerRecheckState, len(providerRecheckStat))
	for id, st := range providerRecheckStat {
		out[id] = *st
	}
	provRecheckMu.Unlock()
	for _, p := range List() {
		if p.Enabled {
			if _, ok := out[p.ID]; !ok {
				out[p.ID] = providerRecheckState{}
			}
		}
	}
	result := make(map[string]any, len(out))
	for id, st := range out {
		result[id] = st
	}
	return result
}

// StartModelRecheck 启动后台检测循环：开机延迟一轮（避开首启模型同步抢带宽），
// 之后 6h + 随机抖动周期执行。
func StartModelRecheck() {
	go func() {
		time.Sleep(providerRecheckBootDelay + time.Duration(rand.Intn(90))*time.Second)
		RunAllRechecks()
		for {
			interval := providerRecheckInterval + time.Duration(rand.Intn(45))*time.Minute
			time.Sleep(interval)
			RunAllRechecks()
		}
	}()
}
