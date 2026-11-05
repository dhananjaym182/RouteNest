package combos

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"routenest/internal/domain"
	"routenest/internal/i18n"
	"routenest/internal/providers"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
)

// EntryKey 组合条目去重键：同一模型但不同上游固定（Source/Provider）不算重复，
// 允许把同一模型分别钉到不同的上游。
func EntryKey(e domain.ComboEntry) string {
	return strings.TrimSpace(e.Model) + "\x00" + strings.TrimSpace(e.Source) + "\x00" + strings.TrimSpace(e.Provider)
}

// comboIDRe 组合 ID 允许的字符集：字母数字开头，后接字母数字/点/下划线/连字符。
// 故意不含 "/" 与 ":"，避免与 "provider/model" 形态的真实模型 ID 混淆。
var comboIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const (
	IDMaxLen        = 64
	comboMaxEntries = 64
)

// knownModelIDs 返回「可被引用的模型 ID」集合：池内全部模型 + 启用中 provider
// 声明的模型 + zen 种子/别名。组合 ID 与条目引用都基于这一集合校验。
func knownModelIDs() map[string]bool {
	known := make(map[string]bool)
	for _, m := range routing.ModelsProvider() {
		if m.ID != "" {
			known[m.ID] = true
		}
	}
	for _, p := range providers.List() {
		if !p.Enabled {
			continue
		}
		for _, id := range p.ModelIDs {
			if id != "" {
				known[id] = true
			}
		}
	}
	for _, sm := range zen.SeedModels {
		if sm.ID != "" {
			known[sm.ID] = true
		}
		for _, a := range sm.Aliases {
			if a != "" {
				known[a] = true
			}
		}
	}
	return known
}

// ============================================================================
// 校验
// ============================================================================

// ValidateCombo 校验组合定义。existing 是除自身以外的现有组合（用于唯一性校验）。
func ValidateCombo(c domain.ModelCombo, existing []domain.ModelCombo) error {
	id := strings.TrimSpace(c.ID)
	if id == "" {
		return errors.New("combo id is required")
	}
	if len(id) > IDMaxLen {
		return fmt.Errorf("combo id is too long (max %d characters)", IDMaxLen)
	}
	if !comboIDRe.MatchString(id) {
		return errors.New("combo id may only contain letters, digits, dot, underscore and hyphen")
	}
	if id == "free" {
		return errors.New(`combo id "free" is a reserved alias`)
	}
	if knownModelIDs()[id] {
		return fmt.Errorf("combo id %q collides with an existing model id", id)
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return errors.New("combo name is required")
	}
	for _, e := range existing {
		if e.ID == id {
			return fmt.Errorf("combo id %q already exists", id)
		}
		if strings.EqualFold(strings.TrimSpace(e.Name), name) {
			return fmt.Errorf("combo name %q is already in use", name)
		}
	}
	if len(c.Entries) == 0 {
		return errors.New("combo needs at least one model entry")
	}
	if len(c.Entries) > comboMaxEntries {
		return fmt.Errorf("combo has too many entries (max %d)", comboMaxEntries)
	}
	known := knownModelIDs()
	seen := make(map[string]bool, len(c.Entries))
	enabledCount := 0
	for _, en := range c.Entries {
		m := strings.TrimSpace(en.Model)
		if m == "" {
			return errors.New("combo entry has an empty model id")
		}
		if m == "free" {
			return errors.New(`the "free" alias cannot be used as a combo entry; add concrete models instead`)
		}
		key := EntryKey(en)
		if seen[key] {
			return fmt.Errorf("duplicate combo entry %q", m)
		}
		seen[key] = true
		if !known[m] {
			return fmt.Errorf("unknown model reference %q", m)
		}
		if zen.RouteModel(m) == "reject" {
			return fmt.Errorf("model %q is a paid opencode model and cannot be used in a combo", m)
		}
		if err := validateComboEntryPin(en, m); err != nil {
			return err
		}
		if !en.Disabled && !routing.IsModelDisabled(m) {
			enabledCount++
		}
	}
	if c.Enabled && enabledCount == 0 {
		return errors.New("an enabled combo needs at least one enabled entry")
	}
	return nil
}

// comboPinError 组合条目上游固定的校验错误：携带 i18n key + 英文兜底文本，
// 便于 handler 本地化，同时保证无上下文调用时（测试）仍可读。
type comboPinError struct {
	key  string
	msg  string
	args []any
}

func (e *comboPinError) Error() string { return e.msg }

// LocalizeComboError 把校验错误本地化：comboPinError 走 tAPI key，其余（既有英文
// 校验信息）原样返回。
func LocalizeComboError(r *http.Request, err error) string {
	if err == nil {
		return ""
	}
	var pe *comboPinError
	if errors.As(err, &pe) && pe.key != "" {
		return i18n.TAPI(r, pe.key, pe.args...)
	}
	return err.Error()
}

// validateComboEntryPin 校验条目的上游固定（Source/Provider）。空 Source 表示自动
// 派发（默认优先级），永远合法；非空时必须指向一个真实可用的目标。
func validateComboEntryPin(en domain.ComboEntry, model string) error {
	switch src := strings.TrimSpace(en.Source); src {
	case "":
		return nil
	case "cline":
		// 必须存在同名、真能由 Cline 账号池服务的池内条目（排除 zen 与
		// provider-only 模型），否则固定到 cline 账号池要么无模型、要么必然 400。
		if !ClinePoolModelExists(model) {
			return &comboPinError{
				key:  "combo_entry_cline_unknown",
				msg:  fmt.Sprintf("model %q is not a Cline account-pool model (non-zen); cannot pin source=cline", model),
				args: []any{model},
			}
		}
		return nil
	case "zen":
		zm, ok := zen.ResolveInfo(model)
		if !ok || !zen.IsFreeModel(zm) {
			return &comboPinError{
				key:  "combo_entry_zen_not_free",
				msg:  fmt.Sprintf("model %q is not an available free zen model; cannot pin source=zen", model),
				args: []any{model},
			}
		}
		return nil
	case "provider":
		name := strings.TrimSpace(en.Provider)
		if name == "" {
			return &comboPinError{
				key: "combo_entry_provider_required",
				msg: "provider name is required when source=provider",
			}
		}
		if !providers.NameServesModel(name, model) {
			return &comboPinError{
				key:  "combo_entry_provider_unknown",
				msg:  fmt.Sprintf("no enabled provider %q serves model %q", name, model),
				args: []any{name, model},
			}
		}
		return nil
	default:
		return &comboPinError{
			key:  "combo_entry_bad_source",
			msg:  fmt.Sprintf("invalid combo entry source %q (allowed: cline, zen, provider)", src),
			args: []any{},
		}
	}
}

// normalizeCombo 规整输入：去空白、丢弃空条目行。
func normalizeCombo(c domain.ModelCombo) domain.ModelCombo {
	c.ID = strings.TrimSpace(c.ID)
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	c.Category = strings.TrimSpace(c.Category)
	ents := make([]domain.ComboEntry, 0, len(c.Entries))
	for _, en := range c.Entries {
		en.Model = strings.TrimSpace(en.Model)
		en.Source = strings.TrimSpace(en.Source)
		en.Provider = strings.TrimSpace(en.Provider)
		if en.Model == "" {
			continue
		}
		ents = append(ents, en)
	}
	c.Entries = ents
	return c
}
