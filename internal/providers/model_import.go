package providers

// ============================================================================
// §9.3 受控模型导入：拉取上游 /models → 有序信号分类每个模型的 free/paid →
// 生成预览批次（preview）→ 管理员勾选批准（apply）后才写入池与 provider。
// 核心护栏：未匹配任何免费信号的模型一律归为 paid，绝不猜免费；已有模型
// 的层级变化只以 diff 呈现，remote/zen/manual 归属的条目永远 protected。
// ============================================================================

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
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

// ---------- admin operations ----------
// HTTP parsing and envelopes live in internal/admin; these functions hold the
// rules and state changes.

// ErrImportBatchNotFound: the batch was applied, discarded or expired.
var ErrImportBatchNotFound = errors.New("batch not found (already applied or expired)")

// ErrImportProviderGone: the provider that produced the batch was deleted.
var ErrImportProviderGone = errors.New("provider no longer exists")

// ErrModelNotFound: no model with the requested ID exists in the pool.
var ErrModelNotFound = errors.New("model not found")

// ErrInvalidModelCost: cost must be "free", "pass", or "" (automatic).
var ErrInvalidModelCost = errors.New("id and cost(free|pass|empty) required")

// DiscoveryError reports that upstream model discovery failed. The console shows
// the message inside a successful response.
type DiscoveryError struct{ Message string }

func (e *DiscoveryError) Error() string { return e.Message }

// ImportSource names the provider to discover from: a saved provider by ID, or
// an unsaved draft identified by its base URL.
type ImportSource struct {
	ID      string
	Name    string
	BaseURL string
	APIKey  string
	Headers map[string]string
	Free    bool
}

// PreviewImport discovers a provider's models and stores a preview batch for
// review. A draft batch has no provider ID and cannot be applied.
func PreviewImport(src ImportSource) (*importBatch, error) {
	Load()
	Mu.Lock()
	var prov *CustomProvider
	if src.ID != "" {
		for _, p := range CustomProviders {
			if p.ID == src.ID {
				cp := *cloneProvider(p)
				prov = &cp
				break
			}
		}
	} else if strings.TrimSpace(src.BaseURL) != "" {
		prov = &CustomProvider{
			Name:    src.Name,
			BaseURL: strings.TrimRight(strings.TrimSpace(src.BaseURL), "/"),
			APIKey:  src.APIKey,
			Headers: src.Headers,
			Free:    src.Free,
		}
	}
	Mu.Unlock()
	if prov == nil {
		return nil, ErrProviderNotFound
	}
	// Protocol-aware discovery: a qoder provider catalog is COSY-signed and
	// lives at /algo/api/v2/model/list, not {baseURL}/models.
	entries, err := DiscoverModels(*prov, DiscoveryOverrides{})
	if err != nil {
		return nil, &DiscoveryError{Message: err.Error()}
	}
	return buildImportBatch(*prov, entries), nil
}

// ImportApplyResult counts the models changed by ApplyImport.
type ImportApplyResult struct {
	Added   int
	Changed int
}

// ApplyImport writes the approved add and tier_change items of a pending batch
// into the pool and the provider's model list. The batch is consumed on success.
func ApplyImport(batchID string, approve []string) (ImportApplyResult, error) {
	importsMu.Lock()
	batch := pendingImports[batchID]
	importsMu.Unlock()
	if batch == nil {
		return ImportApplyResult{}, ErrImportBatchNotFound
	}
	approved := make(map[string]bool, len(approve))
	for _, id := range approve {
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
		return ImportApplyResult{}, ErrImportProviderGone
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
		return ImportApplyResult{}, nil
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
	return ImportApplyResult{Added: added, Changed: changed}, nil
}

// ImportSummary describes a pending batch without its items.
type ImportSummary struct {
	ID           string
	ProviderID   string
	ProviderName string
	CreatedAt    time.Time
	Items        int
	Auto         bool
}

// PendingImports lists the batches awaiting review, newest first.
func PendingImports() []ImportSummary {
	importsMu.Lock()
	out := make([]ImportSummary, 0, len(pendingImports))
	for _, b := range pendingImports {
		out = append(out, ImportSummary{ID: b.ID, ProviderID: b.ProviderID, ProviderName: b.ProviderName, CreatedAt: b.CreatedAt, Items: len(b.Items), Auto: b.Auto})
	}
	importsMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// PendingImport returns the full pending batch with the given ID.
func PendingImport(id string) (*importBatch, error) {
	importsMu.Lock()
	batch := pendingImports[id]
	importsMu.Unlock()
	if batch == nil {
		return nil, ErrImportBatchNotFound
	}
	return batch, nil
}

// DiscardImport drops a pending batch without applying it.
func DiscardImport(id string) {
	importsMu.Lock()
	delete(pendingImports, id)
	importsMu.Unlock()
}

// SetModelCost sets a model's manual tier. An empty cost restores automatic
// classification; the tier stays as it is until the next import or sync.
func SetModelCost(id, cost string) error {
	if id == "" || (cost != "free" && cost != "pass" && cost != "") {
		return ErrInvalidModelCost
	}
	if !models.SetManualCost(id, cost, time.Now().Unix()) {
		return ErrModelNotFound
	}
	return nil
}
