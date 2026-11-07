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
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/models"
	"routenest/internal/platform"
	"routenest/internal/store/syncstate"
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

// ensureSyncPath resolves the recheck-state path on first use. Tests that set
// providerSyncFilePath keep their value.
func ensureSyncPath() {
	if providerSyncFilePath == "" {
		providerSyncFilePath = platform.ResolveDataPath(syncstate.FileName, nil)
	}
}

func LoadRecheckState() {
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if provRecheckLoaded {
		return
	}
	if err := loadProviderRuntimeStateLocked(nil); err != nil {
		log.Printf("provider sync state load failed: %v", err)
	}
}

func saveProviderRecheckStateLocked() error {
	ensureSyncPath()
	if owner := providerRuntimeStateOwner; owner != nil && owner.path == providerSyncFilePath {
		if err := owner.availableLocked(); err != nil {
			return err
		}
		if err := owner.saveLocked(providerRecheckStat); err != nil {
			log.Printf("provider sync state save failed: %v", err)
			return err
		}
		return nil
	}
	out := make(map[string]providerRecheckState, len(providerRecheckStat))
	for id, st := range providerRecheckStat {
		if st != nil {
			out[id] = cloneProviderRecheckState(*st)
		}
	}
	if err := syncstate.Save(providerSyncFilePath, out); err != nil {
		log.Printf("provider sync state save failed: %v", err)
		return err
	}
	return nil
}

// recheckProviderModels 对单个 provider 检测一轮：下架标记 + 新模型入队。
// 返回本轮展示状态副本。上游失败只记 error，不动缺失计数与标记。
func recheckProviderModels(p CustomProvider) providerRecheckState {
	LoadRecheckState()
	st := providerRecheckState{}
	entries, err := DiscoverModels(p, DiscoveryOverrides{})
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

	records := models.Snapshot()
	offeringStates := providerOfferingStates(p, records)
	current := map[string]models.PresenceState{}
	for modelID, state := range offeringStates {
		delisted := state.Delisted != nil && *state.Delisted
		protected := state.Custom != nil && *state.Custom
		current[modelID] = models.PresenceState{Delisted: delisted, Protected: protected}
	}

	// 1) 声明模型的缺失计数（连续 2 次才下架；出现即清零并撤标记）
	provRecheckMu.Lock()
	miss := provRecheckMiss[p.ID]
	if miss == nil {
		miss = map[string]int{}
		provRecheckMiss[p.ID] = miss
	}
	presence, reconcileErr := models.ReconcilePresence(p.ModelIDs, upstream, current, miss, models.ReconcilePolicy{
		ProviderID:       domain.ProviderInstanceID(p.ID),
		MissingThreshold: providerRecheckMissLimit,
		Authoritative:    true,
	})
	provRecheckMu.Unlock()
	if reconcileErr != nil {
		st.LastError = reconcileErr.Error()
		setProviderRecheckState(p.ID, st)
		return st
	}

	// 2) 写 provider-scoped offering：同 ID 的其他 provider 不受影响。
	marked, _ := applyProviderOfferingDelisting(p, presence.ToDelist, upstream)
	syncProviderDelistedProjection(providersIncluding(p))

	// 3) 新上游模型 → Auto 预览批次（不在声明里也不在池里；每 provider 至多一个待审批次）
	providerHas := make(map[string]bool, len(offeringStates))
	for modelID := range offeringStates {
		providerHas[modelID] = true
	}
	fresh := modelcatalog.FilterNew(entries, declared, providerHas)
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

	delistedCount, delistedModels := providerDelistedSnapshot(p.ID, p.ModelIDs)
	st = providerRecheckState{
		LastCheckedMs:  time.Now().UnixMilli(),
		DelistedMarked: delistedCount,
		DelistedModels: delistedModels,
		MissCounting:   presence.MissingCounting,
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
	provRecheckMu.Lock()
	cp := cloneProviderRecheckState(st)
	previous := providerRecheckStat[providerID]
	if prev, ok := providerRecheckStat[providerID]; ok {
		cp.Offerings = cloneOfferingStates(prev.Offerings)
		cp.Runtime = nil
		if prev.Runtime != nil {
			runtime := prev.Runtime.Clone()
			cp.Runtime = &runtime
		}
		if st.LastCheckedMs == 0 {
			cp.LastCheckedMs = prev.LastCheckedMs // 失败轮保留上次成功状态
			cp.DelistedMarked = prev.DelistedMarked
			if prev.DelistedModels != nil {
				cp.DelistedModels = append([]string{}, prev.DelistedModels...)
			}
			cp.MissCounting = prev.MissCounting
			cp.NewQueued = prev.NewQueued
			cp.BatchID = prev.BatchID
		}
	}
	providerRecheckStat[providerID] = &cp
	if err := saveProviderRecheckStateLocked(); err != nil {
		if previous == nil {
			delete(providerRecheckStat, providerID)
		} else {
			providerRecheckStat[providerID] = previous
		}
	}
	provRecheckMu.Unlock()
}

// clearProviderRecheck provider 删除时清掉其检测状态与待审 Auto 批次。
func clearProviderRecheck(providerID string) {
	provRecheckMu.Lock()
	previous, previousMiss := providerRecheckStat[providerID], provRecheckMiss[providerID]
	delete(providerRecheckStat, providerID)
	delete(provRecheckMiss, providerID)
	if err := saveProviderRecheckStateLocked(); err != nil {
		if previous != nil {
			providerRecheckStat[providerID] = previous
		}
		if previousMiss != nil {
			provRecheckMiss[providerID] = previousMiss
		}
		provRecheckMu.Unlock()
		return
	}
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
	return runAllRechecks(context.Background())
}

func runAllRechecks(ctx context.Context) map[string]providerRecheckState {
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
		if ctx.Err() != nil {
			break
		}
		if !p.Enabled {
			continue
		}
		out[p.ID] = recheckProviderModels(p)
		if !waitForRecheck(ctx, providerRecheckGap) {
			break
		}
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
		out[id] = cloneProviderRecheckState(*st)
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

func waitForRecheck(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// StartModelRecheck 启动后台检测循环：开机延迟一轮（避开首启模型同步抢带宽），
// 之后 6h + 随机抖动周期执行。取消 ctx 会停止等待，并在当前检测轮完成后退出。
func StartModelRecheck(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !waitForRecheck(ctx, providerRecheckBootDelay+time.Duration(rand.Intn(90))*time.Second) {
			return
		}
		runAllRechecks(ctx)
		for {
			interval := providerRecheckInterval + time.Duration(rand.Intn(45))*time.Minute
			if !waitForRecheck(ctx, interval) {
				return
			}
			runAllRechecks(ctx)
		}
	}()
	return done
}
