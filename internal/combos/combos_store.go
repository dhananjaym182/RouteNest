package combos

import (
	"errors"
	"fmt"
	"routenest/internal/domain"
	"strings"
	"time"
)

// ============================================================================
// 持久化（复用 .routenest-config.json / proxyConfig / 原子写）
// ============================================================================

// cloneCombos 深拷贝，避免调用方在锁外持有内部切片引用。
func cloneCombos(in []domain.ModelCombo) []domain.ModelCombo {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.ModelCombo, len(in))
	for i, c := range in {
		out[i] = c
		if len(c.Entries) > 0 {
			ents := make([]domain.ComboEntry, len(c.Entries))
			copy(ents, c.Entries)
			out[i].Entries = ents
		}
	}
	return out
}

// ListCombos 返回全部组合（含停用）的快照副本。
func ListCombos() []domain.ModelCombo {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	if Config() == nil {
		return nil
	}
	return cloneCombos(Config().Combos)
}

// FindCombo 按 ID 查找组合（含停用）。ID 精确匹配（去空白）。
func FindCombo(id string) (domain.ModelCombo, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return domain.ModelCombo{}, false
	}
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	if Config() == nil {
		return domain.ModelCombo{}, false
	}
	for _, c := range Config().Combos {
		if c.ID == id {
			cp := c
			if len(c.Entries) > 0 {
				ents := make([]domain.ComboEntry, len(c.Entries))
				copy(ents, c.Entries)
				cp.Entries = ents
			}
			return cp, true
		}
	}
	return domain.ModelCombo{}, false
}

// FindEnabledCombo 只在组合存在且启用时返回（路由用）。
func FindEnabledCombo(id string) (domain.ModelCombo, bool) {
	c, ok := FindCombo(id)
	if !ok || !c.Enabled {
		return domain.ModelCombo{}, false
	}
	return c, true
}

// comboEnabledIDs 返回启用中组合 ID 集合（供 /v1/models 暴露）。
func EnabledList() []domain.ModelCombo {
	all := ListCombos()
	out := make([]domain.ModelCombo, 0, len(all))
	for _, c := range all {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

// ============================================================================
// CRUD
// ============================================================================

// UpsertCombo 新建或更新组合（ID 为主键）并持久化。
func UpsertCombo(c domain.ModelCombo) (domain.ModelCombo, error) {
	c = normalizeCombo(c)

	// 校验在**不持有** proxyConfigMu 的情况下进行：ValidateCombo 会读取模型目录、
	// provider 列表并做 zen 路由判定（涉及 accounts.PoolMu / providersMu / zen.ConfigMu）。
	// 在配置锁内做这些外部读取会扩大锁范围并带来锁序风险，故先快照再校验。
	existing := ListCombos()
	others := make([]domain.ModelCombo, 0, len(existing))
	for _, e := range existing {
		if e.ID == c.ID {
			continue
		}
		others = append(others, e)
	}
	if err := ValidateCombo(c, others); err != nil {
		return domain.ModelCombo{}, err
	}

	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	if Config() == nil {
		EnsureConfig()
	}
	idx := -1
	for i, e := range Config().Combos {
		if e.ID == c.ID {
			idx = i
			continue
		}
		// 锁内仅做无外部调用的复核：防止校验与写入之间并发新增导致的名称冲突。
		if strings.EqualFold(strings.TrimSpace(e.Name), c.Name) {
			return domain.ModelCombo{}, fmt.Errorf("combo name %q is already in use", c.Name)
		}
	}
	now := time.Now()
	if idx >= 0 {
		c.CreatedAt = Config().Combos[idx].CreatedAt
	} else if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if idx >= 0 {
		Config().Combos[idx] = c
	} else {
		Config().Combos = append(Config().Combos, c)
	}
	SaveConfig()
	return c, nil
}

// DeleteCombo 删除组合。
func DeleteCombo(id string) bool {
	id = strings.TrimSpace(id)
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	if Config() == nil {
		return false
	}
	for i, c := range Config().Combos {
		if c.ID == id {
			Config().Combos = append(Config().Combos[:i], Config().Combos[i+1:]...)
			SaveConfig()
			return true
		}
	}
	return false
}

// SetComboEnabled 启停组合。
func SetComboEnabled(id string, enabled bool) (domain.ModelCombo, error) {
	id = strings.TrimSpace(id)
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	if Config() == nil {
		return domain.ModelCombo{}, errors.New("combo not found")
	}
	for i := range Config().Combos {
		if Config().Combos[i].ID == id {
			if enabled {
				enabledCount := 0
				for _, en := range Config().Combos[i].Entries {
					if !en.Disabled {
						enabledCount++
					}
				}
				if enabledCount == 0 {
					return domain.ModelCombo{}, errors.New("cannot enable a combo with no enabled entries")
				}
			}
			Config().Combos[i].Enabled = enabled
			Config().Combos[i].UpdatedAt = time.Now()
			SaveConfig()
			cp := Config().Combos[i]
			if len(cp.Entries) > 0 {
				ents := make([]domain.ComboEntry, len(cp.Entries))
				copy(ents, cp.Entries)
				cp.Entries = ents
			}
			return cp, nil
		}
	}
	return domain.ModelCombo{}, errors.New("combo not found")
}

// CloneCombo 复制一个组合，生成唯一 ID/名称。
func CloneCombo(id string) (domain.ModelCombo, error) {
	src, ok := FindCombo(id)
	if !ok {
		return domain.ModelCombo{}, errors.New("combo not found")
	}
	all := ListCombos()
	takenID := map[string]bool{}
	takenName := map[string]bool{}
	for _, c := range all {
		takenID[c.ID] = true
		takenName[strings.ToLower(c.Name)] = true
	}
	base := src.ID
	// 预留后缀空间，避免 64 字符 ID 克隆后超长被 ValidateCombo 拒绝。
	if max := IDMaxLen - len("-copy-99"); len(base) > max {
		base = base[:max]
	}
	newID := base + "-copy"
	for i := 2; takenID[newID]; i++ {
		newID = fmt.Sprintf("%s-copy-%d", base, i)
	}
	newName := src.Name + " (copy)"
	for i := 2; takenName[strings.ToLower(newName)]; i++ {
		newName = fmt.Sprintf("%s (copy %d)", src.Name, i)
	}
	cp := src
	cp.ID = newID
	cp.Name = newName
	cp.CreatedAt = time.Time{}
	cp.UpdatedAt = time.Time{}
	return UpsertCombo(cp)
}
