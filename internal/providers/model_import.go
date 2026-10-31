package providers

// ============================================================================
// §9.3 受控模型导入：拉取上游 /models → 有序信号分类每个模型的 free/paid →
// 生成预览批次（preview）→ 管理员勾选批准（apply）后才写入池与 provider。
// 核心护栏：未匹配任何免费信号的模型一律归为 paid，绝不猜免费；已有模型
// 的层级变化只以 diff 呈现，remote/zen/manual 归属的条目永远 protected。
// ============================================================================

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/modelcatalog"
	"routenest/internal/models"
)

// Tier signals (stored in Model.TierSignal; admin UI shows why a model is free
// and supports manual override to "manual"). Ordered strongest-first.
const (
	tierSignalPricingZero  = modelcatalog.TierSignalPricingZero
	tierSignalFlagFree     = modelcatalog.TierSignalFlagFree
	tierSignalSuffixFree   = modelcatalog.TierSignalSuffixFree
	tierSignalNameToken    = modelcatalog.TierSignalNameToken
	tierSignalDisplayName  = modelcatalog.TierSignalDisplayName
	tierSignalAllowlist    = modelcatalog.TierSignalAllowlist
	tierSignalProviderFree = modelcatalog.TierSignalProviderFree
	tierSignalPricingPaid  = modelcatalog.TierSignalPricingPaid
	tierSignalPaidDefault  = modelcatalog.TierSignalPaidDefault
)

// upstreamModelEntry is one parsed /models item (OpenAI list + OpenRouter-style pricing).
type upstreamModelEntry = modelcatalog.Entry

func freeWord(s string) bool { return modelcatalog.FreeWord(s) }

// classifyUpstreamModel checks signals in order and returns on the first hit.
// Explicit free markers rank above explicit non-zero pricing.
func classifyUpstreamModel(e upstreamModelEntry, providerFree bool) (tier, signal string) {
	return modelcatalog.Classify(e, providerFree, freeModelIDAllowlist())
}

// ---------- 预览批次 ----------

type importItem = modelcatalog.ImportItem

type importBatch = modelcatalog.ImportBatch

var (
	importsMu      sync.Mutex
	pendingImports = map[string]*importBatch{}
)

func newImportBatchID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// buildImportBatch 对条目分类并对比当前池，产出仅内存的待批准批次。
func buildImportBatch(p CustomProvider, entries []upstreamModelEntry) *importBatch {
	batch := &importBatch{ID: newImportBatchID(), ProviderID: p.ID, ProviderName: p.Name, CreatedAt: time.Now()}
	existing := make(map[string]modelcatalog.Existing)
	models.Read(func(list []domain.Model) {
		for _, m := range list {
			existing[m.ID] = modelcatalog.Existing{Source: m.Source, TierSignal: m.TierSignal, Cost: m.Cost}
		}
	})
	batch.Items, batch.Stats = modelcatalog.BuildImportItems(entries, p.Free, existing, freeModelIDAllowlist())
	importsMu.Lock()
	for id, old := range pendingImports {
		if len(pendingImports) >= 20 && time.Since(old.CreatedAt) > time.Hour {
			delete(pendingImports, id)
		}
	}
	pendingImports[batch.ID] = batch
	importsMu.Unlock()
	return batch
}

// ---------- admin 端点 ----------

// POST /admin/api/models/import/preview  body: {id} 或草稿 {baseURL,apiKey,headers,free,name}
// 拉取 + 分类 + 生成预览批次。草稿（未保存 Provider）走后者，批次 providerId 为空、
// 仅可预览：apply 端点找不到 provider 会拒绝，前端改为「所选并入并保存」。
// 上游失败按既有约定：HTTP 200 包 {error}。
func HandleImportPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	var req struct {
		ID      string            `json:"id"`
		BaseURL string            `json:"baseURL"`
		APIKey  string            `json:"apiKey"`
		Headers map[string]string `json:"headers"`
		Free    bool              `json:"free"`
		Name    string            `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	Load()
	Mu.Lock()
	var prov *CustomProvider
	if req.ID != "" {
		for _, p := range CustomProviders {
			if p.ID == req.ID {
				cp := *cloneProvider(p)
				prov = &cp
				break
			}
		}
	} else if strings.TrimSpace(req.BaseURL) != "" {
		prov = &CustomProvider{
			Name:    req.Name,
			BaseURL: strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
			APIKey:  req.APIKey,
			Headers: req.Headers,
			Free:    req.Free,
		}
	}
	Mu.Unlock()
	if prov == nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider not found"})
		return
	}
	// Protocol-aware discovery: a qoder provider catalog is COSY-signed and
	// lives at /algo/api/v2/model/list, not {baseURL}/models.
	entries, err := DiscoverModels(*prov, DiscoveryOverrides{})
	if err != nil {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"error": err.Error()}})
		return
	}
	batch := buildImportBatch(*prov, entries)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"batch": batch}})
}

// POST /admin/api/models/import/apply  body: {batchId, approve:[modelId…]}
// 仅批准列表内的 add / tier_change 落盘；写入 provider.ModelIDs 使其在
// SyncModels 清理逻辑中存续。
func HandleImportApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	var req struct {
		BatchID string   `json:"batchId"`
		Approve []string `json:"approve"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	importsMu.Lock()
	batch := pendingImports[req.BatchID]
	importsMu.Unlock()
	if batch == nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "batch not found (already applied or expired)"})
		return
	}
	approved := make(map[string]bool, len(req.Approve))
	for _, id := range req.Approve {
		approved[id] = true
	}
	byID := make(map[string]importItem, len(batch.Items))
	for _, it := range batch.Items {
		byID[it.ModelID] = it
	}
	prov := func() *CustomProvider {
		Load()
		Mu.Lock()
		defer Mu.Unlock()
		for _, p := range CustomProviders {
			if p.ID == batch.ProviderID {
				cp := *cloneProvider(p)
				return &cp
			}
		}
		return nil
	}()
	if prov == nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider no longer exists"})
		return
	}
	// Decisions are sorted by id so appended records keep a stable order.
	var decisions []models.Decision
	for id := range approved {
		it, ok := byID[id]
		if !ok || (it.Action != "add" && it.Action != "tier_change") {
			continue
		}
		decisions = append(decisions, models.Decision{ID: id, Cost: it.Tier, Signal: it.Signal})
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ID < decisions[j].ID })
	addedIDs, changed := models.ApplyDecisions(decisions, prov.Name, time.Now().Unix())
	added := len(addedIDs)
	// Newly added models join the provider's ModelIDs, which SyncModels uses as
	// the authority for its cleanup loop.
	for _, id := range addedIDs {
		found := false
		for _, mid := range prov.ModelIDs {
			if mid == id {
				found = true
				break
			}
		}
		if !found {
			prov.ModelIDs = append(prov.ModelIDs, id)
		}
	}
	if added+changed == 0 {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"added": 0, "changed": 0}})
		return
	}
	Mu.Lock()
	for i, p := range CustomProviders {
		if p.ID == prov.ID {
			CustomProviders[i].ModelIDs = append([]string(nil), prov.ModelIDs...)
			break
		}
	}
	SaveLocked()
	Mu.Unlock()
	accounts.SavePool()
	importsMu.Lock()
	delete(pendingImports, batch.ID)
	importsMu.Unlock()
	SyncModels()
	log.Printf("model import applied: batch %s provider %s added=%d tierChanged=%d", batch.ID, prov.Name, added, changed)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"added": added, "changed": changed}})
}

// GET /admin/api/models/import/pending — 当前待批准批次（摘要）。
func HandleImportPending(w http.ResponseWriter, r *http.Request) {
	importsMu.Lock()
	out := make([]map[string]any, 0, len(pendingImports))
	for _, b := range pendingImports {
		out = append(out, map[string]any{
			"id": b.ID, "providerId": b.ProviderID, "providerName": b.ProviderName,
			"createdAt": b.CreatedAt, "items": len(b.Items), "auto": b.Auto,
		})
	}
	importsMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i]["createdAt"].(time.Time).After(out[j]["createdAt"].(time.Time))
	})
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"batches": out}})
}

// GET /admin/api/models/import/batch?id=x — 读回待批准批次全文（横幅点击打开 Auto 批次用）。
func HandleImportBatch(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	importsMu.Lock()
	batch := pendingImports[id]
	importsMu.Unlock()
	if batch == nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "batch not found (already applied or expired)"})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"batch": batch}})
}

// POST /admin/api/models/import/discard  body: {batchId}
func HandleImportDiscard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BatchID string `json:"batchId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	importsMu.Lock()
	delete(pendingImports, req.BatchID)
	importsMu.Unlock()
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// POST /admin/api/models/cost  body: {id, cost:"free"|"pass"|""}
// 手动定级（TierSignal="manual"）；cost 传空字符串=恢复自动（清除 manual 标记，
// 层级保留现值，等下一次导入/同步再判定）。§9.3：manual 条目在导入批次中 protected。
func HandleModelCost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		Cost string `json:"cost"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	if req.ID == "" || (req.Cost != "free" && req.Cost != "pass" && req.Cost != "") {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "id and cost(free|pass|empty) required"})
		return
	}
	if !models.SetManualCost(req.ID, req.Cost, time.Now().Unix()) {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "model not found"})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}
