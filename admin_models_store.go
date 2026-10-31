package main

import (
	"cline-go-proxy/internal/routing"
	"cline-go-proxy/internal/zen"
	"net/http"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/i18n"
)

// rootModelStore adapts the account pool's model catalog to adminapi.ModelStore.
// Locking and persistence stay in the root composition layer.
type rootModelStore struct{}

func (rootModelStore) List() []domain.Model {
	return getAllModels()
}

func (rootModelStore) Add(m domain.Model) {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	p.Models = append(p.Models, m)
	accounts.PoolMu.Unlock()
	accounts.SavePool()
}

func (rootModelStore) Delete(id string) adminapi.DeleteModelResult {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	for i, m := range p.Models {
		if m.ID != id {
			continue
		}
		// Custom models may be removed; synchronized models only once delisted.
		if !m.Custom && !m.Delisted {
			accounts.PoolMu.Unlock()
			return adminapi.ModelBuiltin
		}
		p.Models = append(p.Models[:i], p.Models[i+1:]...)
		if p.DefaultModel == id {
			p.DefaultModel = ""
		}
		accounts.PoolMu.Unlock()
		accounts.SavePool()
		return adminapi.ModelDeleted
	}
	accounts.PoolMu.Unlock()
	return adminapi.ModelNotFound
}

func (rootModelStore) UpdateContext(id string, context, output int) bool {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	for i := range p.Models {
		if p.Models[i].ID != id {
			continue
		}
		p.Models[i].Context = context
		p.Models[i].Output = output
		// Zeroing both returns the limits to unknown; otherwise the user locks them
		// against sync overwrites.
		p.Models[i].MetaLocked = !(context == 0 && output == 0)
		accounts.PoolMu.Unlock()
		accounts.SavePool()
		return true
	}
	accounts.PoolMu.Unlock()
	return false
}

func (rootModelStore) UpdateCapabilities(id string, caps adminapi.ModelCapabilities) bool {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	for i := range p.Models {
		if p.Models[i].ID != id {
			continue
		}
		// nil means "leave unchanged" (the handler distinguishes it from an
		// explicit empty list, which clears the field).
		if caps.ThinkingLevels != nil {
			p.Models[i].ThinkingLevels = caps.ThinkingLevels
		}
		if caps.InputModalities != nil {
			p.Models[i].InputModalities = caps.InputModalities
		}
		if caps.OutputModalities != nil {
			p.Models[i].OutputModalities = caps.OutputModalities
		}
		accounts.PoolMu.Unlock()
		accounts.SavePool()
		return true
	}
	accounts.PoolMu.Unlock()
	return false
}

func (rootModelStore) SetDisabled(ids []string, disabled bool) int {
	target := make(map[string]bool, len(ids))
	for _, id := range ids {
		target[id] = true
	}
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	changed := 0
	for i := range p.Models {
		if !target[p.Models[i].ID] {
			continue
		}
		p.Models[i].Disabled = disabled
		changed++
		if disabled && p.DefaultModel == p.Models[i].ID {
			p.DefaultModel = ""
		}
	}
	accounts.PoolMu.Unlock()
	if changed > 0 {
		accounts.SavePool()
	}
	return changed
}

// normalizeAdminModels applies the billing normalization the admin UI expects:
// zen free models always display as free, matching route classification.
func normalizeAdminModels(models []domain.Model) []domain.Model {
	for i := range models {
		if zen.IsSource(models[i]) && zen.IsFreeModel(models[i]) && models[i].Cost != "free" {
			models[i].Cost = "free"
		}
	}
	return models
}

var adminModels = &adminapi.Models{
	Store:     rootModelStore{},
	LastSync:  func() any { return routing.GetModelSyncResult() },
	Normalize: normalizeAdminModels,
	Translate: func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}
