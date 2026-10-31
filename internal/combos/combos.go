package combos

// 模型组合（Named Model Combos）+ 有序回退 —— Phase 1。
//
// 设计目标：管理员可配置若干具名组合（如 premium-coding / fast-free），每个组合
// 是一串**有序**的现有模型条目；客户端把组合 ID 当作普通模型名请求，路由器按配置
// 顺序逐个尝试，遇到可重试的上游失败就换下一个条目，直到成功或耗尽。
//
// 关键约束（实现时严格遵守）：
//   - 组合内的顺序是唯一权威。**不**按用量/延迟/质量/随机/账号轮询重排健康条目；
//     运行时可用性只会让某个条目被「跳过」，不会改变配置顺序。
//   - 每个条目的派发复用既有路径：zen 免费模型走 zen.Call；自定义 provider 走
//     providers.CallAPI（含冗余组、粘性会话）；其余走 Cline 账号池（含账号轮询、
//     模型级冷却、402/429 处理）。**不**对每个条目递归调用全局 ModelChain / free 链。
//   - 默认情况下组合只走自己的序列；只有管理员显式开启 emergencyFallback 才允许
//     落到全局 free 链（应急），且该行为在 UI 与报告中明确标注。
//   - 复用既有持久化（.routenest-config.json + 原子写 + 损坏备份），不引入新存储。
//
// 数据模型预留了 Phase 2 的扩展位（Category 等）；本阶段不实现任何自动质量评分，
// 也**不**从模型名推断质量。

import (
	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/compaction"
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/i18n"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/routing"
	"cline-go-proxy/internal/zen"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Root-owned live state seams, wired once from root composition. Defaults keep
// the package self-contained for unit tests.
var (
	ConfigMu     = &defaultConfigMu
	Config       = func() *config.Proxy { return defaultConfig }
	GetConfig    = func() *config.Proxy { return defaultConfig }
	EnsureConfig = func() *config.Proxy { return defaultConfig }
	SaveConfig   = func() {}

	IsAccountUnavailable    = func(error) bool { return false }
	NewUnavailableError     = func(message string) error { return &unavailableError{message: message} }
	CallClineAPIWithAccount = func(context.Context, *domain.Account, map[string]any, bool) (*http.Response, *domain.Account, error) {
		return nil, nil, fmt.Errorf("cline API not wired")
	}
	IsDeterministicRequestError = func(error) bool { return false }
	SetModelCooldown            = func(*domain.Account, string, time.Time) {}
	TruncateEmail               = func(email string) string { return email }
	CallFreeClineAPI            = func(map[string]any, bool) (*http.Response, *domain.Account, error) {
		return nil, nil, fmt.Errorf("free chain not wired")
	}
	WithModel = func(params map[string]any, model string) map[string]any {
		cp := make(map[string]any, len(params)+1)
		for k, v := range params {
			cp[k] = v
		}
		cp["model"] = model
		return cp
	}
)

var (
	defaultConfigMu sync.Mutex
	defaultConfig   = config.DefaultProxy()
)

type unavailableError struct{ message string }

func (e *unavailableError) Error() string { return e.message }

const (
	servedByParam     = "__served_by"
	providerNameParam = "__served_by_name"
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
		if !en.Disabled && !routing.ModelDisabled(m) {
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

// ============================================================================
// 组合运行：有序回退
// ============================================================================

// Run 是一次组合派发的完整结果。
type Run struct {
	Resp     *http.Response
	Acc      *domain.Account
	Upstream string
	Model    string // 实际服务的模型
	Trace    []domain.AttemptLog
	Err      error
}

// Retryable 判断某次条目尝试的失败是否可重试（应继续尝试下一个条目）。
//
// 可重试：限流(429)/配额(402)/网关重定向(421)/瞬态 5xx/超时/传输错误/模型不存在(404)。
// 不可重试：客户端/配置类错误 400/401/403 —— 重试下一个模型没有意义，直接失败。
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if IsAccountUnavailable(err) {
		return true // 账号不可用：可能换号/换条目即可恢复
	}
	var apiErr *egress.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 400, 401, 403:
			return false
		case 402, 408, 409, 421, 425, 429:
			return true
		case 404:
			return true
		default:
			return apiErr.StatusCode >= 500
		}
	}
	// 其余（zen 网络/上游错误、未知错误）按瞬态处理，交给下一个条目。
	return true
}

// AttemptStatus 从错误里尽量提取上游 HTTP 状态码（供尝试轨迹展示）。
func AttemptStatus(err error) int {
	var apiErr *egress.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// RouteKind 判定组合条目的派发路径：zen / reject（付费 zen）/ cline，以及
// zen-degraded（zen 处于故障转移窗口，条目运行时不可用应跳过）。
//
// 与 zen.RouteModel 的唯一区别：zen.RouteModel 在 zen 故障转移窗口会把 zen 免费模型降级
// 为 "cline"，而组合**不能**沿用该降级 —— 组合没有全局 free 链可改写，若把 zen
// 模型 ID 原样发给 Cline 会得到 400 "invalid model format"，进而被误判为永久失败
// 并中止整条组合。故这里显式区分「同名 cline 冲突」（走 cline，与 zen.RouteModel 一致）
// 与「zen 降级」（走 zen-degraded → 跳过条目）。
func RouteKind(model string) string {
	switch zen.RouteModel(model) {
	case "reject":
		return "reject"
	case "zen":
		return "zen"
	}
	// zen.RouteModel == "cline"
	zm, ok := zen.ResolveInfo(model)
	if !ok || !zen.IsFreeModel(zm) {
		return "cline" // 普通 cline 模型
	}
	if ClineModelWithIDExists(model) {
		return "cline" // 同名 cline 冲突：cline 优先（与 zen.RouteModel 一致）
	}
	return "zen-degraded"
}

// ClineModelWithIDExists 判断池内是否存在与给定模型同名的非 zen 模型
// （自定义冲突场景，zen.RouteModel 会把同名 zen 模型让给 cline）。
func ClineModelWithIDExists(model string) bool {
	id := strings.TrimPrefix(strings.TrimSpace(model), "opencode/")
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	for _, pm := range p.Models {
		if !zen.IsSource(pm) && pm.ID == id {
			return true
		}
	}
	return false
}

// ClinePoolModelExists 判断模型是否真能由 Cline 账号池服务：排除 zen 与自定义
// provider 来源。source=cline 固定必须用它（严格）——provider 同步进池的条目
// 走 Cline 会得到 400 "invalid model format"，进而被组合误判为永久失败。
func ClinePoolModelExists(model string) bool {
	id := strings.TrimPrefix(strings.TrimSpace(model), "opencode/")
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	for _, pm := range p.Models {
		if !zen.IsSource(pm) && pm.Source != "provider" && pm.ID == id {
			return true
		}
	}
	return false
}

// EntryRouteKind 在 RouteKind 之上叠加条目的上游固定（Source/Provider）。
//
//   - Source == "" → RouteKind(Model)（默认自动派发，行为不变）。
//   - "cline"      → 模型真能由 Cline 账号池服务时 "cline"（跳过自定义 provider），
//     否则 "unavailable"（跳过，避免 provider-only 模型被发给 Cline 触发 400 中止）。
//   - "provider"   → "cline"（provider 名称在 cline 分支里强制指定）。
//   - "zen"        → 显式固定压过 AUTO 的同名 cline 冲突降级：可解析且为免费 zen
//     模型时返回 "zen"，zen→cline 故障转移窗口内返回 "zen-degraded"，否则
//     "unavailable"（跳过）。
//   - 其他（未知，通常已被 ValidateCombo 拦截）→ 回退到自动派发，避免运行时误伤。
func EntryRouteKind(e domain.ComboEntry) string {
	base := RouteKind(e.Model)
	switch strings.TrimSpace(e.Source) {
	case "":
		return base
	case "cline":
		// 严格判定：模型必须真能由 Cline 账号池服务。provider-only 模型（含同步
		// 进池的 source=provider 行）走 Cline 会 400，故按运行时不可用跳过该条目，
		// 而不是把 provider 的模型 ID 原样发给 Cline 触发整条组合中止。
		if !ClinePoolModelExists(e.Model) {
			return "unavailable"
		}
		return "cline"
	case "provider":
		return "cline"
	case "zen":
		// 显式 zen 固定压过 AUTO 的「同名 cline 冲突」降级：这正是用 zen 固定来
		// 消歧的用途。只按 zen 自身状态判定，与 validateComboEntryPin 保持一致。
		zm, ok := zen.ResolveInfo(e.Model)
		if !ok || !zen.IsFreeModel(zm) {
			return "unavailable"
		}
		if cfg := zen.GetConfig(); cfg.Failover && zen.FailedNow() {
			return "zen-degraded"
		}
		return "zen"
	default:
		return base
	}
}

// CallSingleModel 通过既有 provider/账号路径调用**单个**模型，不触发全局 ModelChain
// 或 free 链：这是组合回退的安全基础。
//
//   - zen 免费模型 → zen.Call（含匿名免费层处理）
//   - 自定义 provider → providers.CallAPI（冗余组 + 粘性会话）；条目固定到
//     某一 provider 时改走 providers.CallAPINamed（只看该家，不回退账号池）
//   - 其余 → Cline 账号池（模型级冷却、402/429 换号，账号轮询）
//
// 返回：resp / 使用的账号（若有）/ 上游归因 / 实际服务的自定义 provider 显示名
// （upstream=="provider" 时非空，cline/opencode 为空）/ error。
func CallSingleModel(params map[string]any, entry domain.ComboEntry, stream bool, sessionID string) (*http.Response, *domain.Account, string, string, error) {
	model := strings.TrimSpace(entry.Model)
	ep := WithModel(params, model)
	delete(ep, servedByParam)
	delete(ep, providerNameParam)
	delete(ep, providers.ProviderIDParam)

	switch EntryRouteKind(entry) {
	case "reject":
		return nil, nil, zen.UpstreamOpenCode, "", fmt.Errorf("model %q is a paid opencode model; only free models are proxied", model)
	case "zen-degraded":
		// zen 处于故障转移窗口：绝不把 zen 免费模型 ID 原样发给 Cline（会被判
		// 400 "invalid model format"，进而被组合误判为永久失败）。按运行时不可用
		// 跳过该条目 —— 返回可重试错误，组合继续尝试下一个条目。
		return nil, nil, zen.UpstreamOpenCode, "", NewUnavailableError(
			fmt.Sprintf("model %q temporarily unavailable (opencode zen degraded)", model),
		)
	case "unavailable":
		// 条目固定的上游当前无法服务该模型（zen 固定到非免费/非 zen 模型，或
		// cline 固定到 provider-only 模型等）：按运行时不可用跳过，返回可重试错误，
		// 让组合继续尝试下一个条目而不是中止。
		return nil, nil, zen.UpstreamOpenCode, "", NewUnavailableError(
			fmt.Sprintf("model %q is not available on the pinned source", model),
		)
	case "zen":
		if zm, ok := zen.ResolveInfo(model); ok {
			if out := compaction.MaybeCompact(ep, zm, sessionID); out.Changed {
				log.Printf("  combo %s", out.Note)
			}
		}
		resp, err := zen.Call(ep, stream)
		// 上游返回「上下文超限」400：压缩后重试一次
		if err != nil && compaction.IsContextLengthError(err) {
			if zm, ok := zen.ResolveInfo(model); ok {
				log.Printf("  combo: context length exceeded, compacting and retrying")
				if out := compaction.FallbackTruncate(ep, zm); out.Changed {
					log.Printf("  combo %s", out.Note)
					resp, err = zen.Call(ep, stream)
				}
			}
		}
		if err != nil {
			return nil, nil, zen.UpstreamOpenCode, "", err
		}
		return resp, nil, zen.UpstreamOpenCode, "", nil
	}

	// cline 侧：默认先自定义 provider（该模型若接入 provider），失败后走账号池。
	switch strings.TrimSpace(entry.Source) {
	case "provider":
		// 显式固定到某一家 provider：只看它，失败也**不**回退到 Cline 账号池
		// （用户明确指定了唯一上游）。
		resp, perr, attempted, provName := providers.CallAPINamed(ep, stream, entry.Provider)
		if !attempted {
			return nil, nil, zen.UpstreamProvider, "", NewUnavailableError(
				fmt.Sprintf("model %q is not served by provider %q", model, strings.TrimSpace(entry.Provider)),
			)
		}
		if perr != nil {
			// 固定 provider 失败：包装成可重试的「跳过」，避免 400/401/403（如
			// 一把被轮换的 API key）被 Retryable 判为永久错误而中止整条组合。
			return nil, nil, zen.UpstreamProvider, provName, NewUnavailableError(
				fmt.Sprintf("pinned provider %q failed for %q: %s", strings.TrimSpace(entry.Provider), model, platform.Truncate(perr.Error(), 200)),
			)
		}
		return resp, nil, zen.UpstreamProvider, provName, nil
	case "cline":
		// 显式固定到 Cline 账号池：跳过自定义 provider 尝试，直接走账号池。
	default:
		if resp, perr, attempted, provName := providers.CallAPI(ep, stream); attempted {
			if perr == nil {
				return resp, nil, zen.UpstreamProvider, provName, nil
			}
			// provider 已逐个记录失败原因；继续用账号池兜底同一模型。
		}
	}

	tried := map[string]bool{}
	for {
		acc := accounts.PickAccountForModelStrict(model)
		if acc == nil {
			return nil, nil, zen.UpstreamCline, "", NewUnavailableError(fmt.Sprintf("model %q has no available account", model))
		}
		if tried[acc.AccountID] {
			// 防呆：账号轮询下也不应无限重试同一个账号（fill 策略等边界）。
			return nil, acc, zen.UpstreamCline, "", NewUnavailableError(fmt.Sprintf("model %q unavailable on all eligible accounts", model))
		}
		tried[acc.AccountID] = true

		resp, usedAcc, err := CallClineAPIWithAccount(context.Background(), acc, ep, stream)
		if err == nil {
			return resp, usedAcc, zen.UpstreamCline, "", nil
		}
		if IsAccountUnavailable(err) {
			continue // token 失效等：换下一个账号
		}
		// 确定性 payload 400（invalid-request 语义，如工具可选字段 null 被网关硬拒）：
		// 换账号不可能自愈，立即上抛 —— 不在账号池上空转注定失败的上游往返。
		// （上下文超限 400 除外：RunCombo 层有压缩+重试路径。）
		if IsDeterministicRequestError(err) {
			return nil, usedAcc, zen.UpstreamCline, "", err
		}
		if apiErr, ok := err.(*egress.APIError); ok {
			switch apiErr.StatusCode {
			case 402:
				// 余额不足是账号级问题：冷却该账号该模型后换号，仍试同一模型。
				SetModelCooldown(usedAcc, model, time.Now().Add(30*time.Minute))
				log.Printf("  combo: model %q on %s: insufficient credits (402), cooling 30m, next account", model, TruncateEmail(usedAcc.Email))
				continue
			case 429:
				continue // 模型级冷却已记录，换号
			}
		}
		return nil, usedAcc, zen.UpstreamCline, "", err
	}
}

// CompactionModel 取候选条目中最小的有效上下文窗口作为组合压缩预算：
// 组合里最窄的模型决定上限，压缩后的历史对所有条目都安全。未知模型落到
// EffectiveCompactContext 的默认封顶。
func CompactionModel(combo domain.ModelCombo) domain.Model {
	var chosen domain.Model
	best := 0
	for _, e := range combo.Entries {
		m := strings.TrimSpace(e.Model)
		if m == "" || e.Disabled {
			continue
		}
		zm := compaction.ModelForCompaction(m)
		ctx := compaction.EffectiveCompactContext(zm)
		if best == 0 || ctx < best {
			best = ctx
			chosen = zm
		}
	}
	if best == 0 {
		return domain.Model{Context: compaction.DefaultCompactContextCeiling}
	}
	chosen.Context = best
	return chosen
}

// RunCombo 按配置顺序执行组合，返回首个成功的上游响应；失败/跳过的条目记入轨迹。
func RunCombo(params map[string]any, combo domain.ModelCombo, stream bool, sessionID string) Run {
	trace := make([]domain.AttemptLog, 0, len(combo.Entries))
	tried := make(map[string]bool, len(combo.Entries))
	failed := 0
	var lastErr error
	// 组合级主动压缩：以候选模型中最小的有效上下文为预算，一次压缩写回共享
	// params（CallSingleModel 压缩的是 map 副本，条目之间不会传递）。
	if out := compaction.MaybeCompact(params, CompactionModel(combo), sessionID); out.Changed {
		log.Printf("  combo %q %s", combo.ID, out.Note)
	}

	// 上下文超限的「截断重试」整条组合只做一次：截断后的 params 在后续条目间共享。
	truncatedForRun := false

	for i, entry := range combo.Entries {
		m := strings.TrimSpace(entry.Model)
		if m == "" {
			continue
		}
		order := i + 1
		if entry.Disabled {
			trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped", Reason: "entry disabled by admin",
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
			continue
		}
		key := EntryKey(entry)
		if tried[key] {
			trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped", Reason: "duplicate entry",
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
			continue
		}
		if routing.ModelDisabled(m) {
			trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped", Reason: "model disabled by admin",
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
			continue
		}
		if GetConfig().OnlyFree {
			bare, _ := providers.SplitPin(m)
			if !routing.ModelFreeOnlyEligible(bare) {
				trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped",
					Reason: fmt.Sprintf("not eligible under onlyFree (access class: %s)", routing.ModelRouteAccess(bare).Class),
					Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
				continue
			}
		}
		tried[key] = true

		start := time.Now()
		resp, acc, upstream, provName, err := CallSingleModel(params, entry, stream, sessionID)
		elapsed := time.Since(start).Milliseconds()
		// 上下文超限（400 + context-length error）：整条组合还没截断过时截断一次并
		// 重试**当前**条目。模型不是 zen 时用零值 Model 兜底 —— FallbackTruncate /
		// MaybeCompact 的零值默认（200k+封顶）仍会给出合理预算。
		if err != nil && compaction.IsContextLengthError(err) && !truncatedForRun {
			truncatedForRun = true
			log.Printf("  combo %q: entry %d %q context length exceeded, truncating and retrying", combo.ID, order, m)
			if out := compaction.FallbackTruncate(params, compaction.ModelForCompaction(m)); out.Changed {
				log.Printf("  combo %s", out.Note)
				start = time.Now()
				resp, acc, upstream, provName, err = CallSingleModel(params, entry, stream, sessionID)
				elapsed = time.Since(start).Milliseconds()
			}
			// 截断后（或截断未生效）仍报上下文超限：按可重试错误继续下一个条目，
			// 不让 400 经 Retryable 判为永久错误而中止整条组合。
		}
		if err == nil {
			params["model"] = m
			if upstream == zen.UpstreamProvider {
				params[servedByParam] = zen.UpstreamProvider
				params[providerNameParam] = provName
			} else {
				delete(params, servedByParam)
				delete(params, providerNameParam)
				delete(params, providers.ProviderIDParam)
			}
			trace = append(trace, domain.AttemptLog{
				Model: m, Order: order, Upstream: upstream, Outcome: "success", DurationMs: elapsed,
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider),
				ProviderName: provName,
			})
			return Run{Resp: resp, Acc: acc, Upstream: upstream, Model: m, Trace: trace}
		}

		failed++
		lastErr = err
		trace = append(trace, domain.AttemptLog{
			Model: m, Order: order, Upstream: upstream, Status: AttemptStatus(err),
			Outcome: "failed", Reason: platform.Truncate(err.Error(), 200), DurationMs: elapsed,
			Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider),
			ProviderName: provName,
		})
		log.Printf("  combo %q: entry %d %q failed (%v)", combo.ID, order, m, err)

		if compaction.IsContextLengthError(err) {
			// 上下文超限：即便 HTTP 400（Retryable 判为永久）也继续换下一个条目。
			continue
		}
		if !Retryable(err) {
			// 永久性错误（400/401/403 等）：继续换模型没有意义，直接失败。
			return Run{Trace: trace, Err: err}
		}
	}

	// 默认：组合只走自己的序列，不触发全局回退。
	if combo.EmergencyFallback {
		log.Printf("  combo %q: all entries failed, emergency fallback (opt-in) to free chain", combo.ID)
		fp := WithModel(params, "free")
		if resp, acc, err := CallFreeClineAPI(fp, stream); err == nil {
			fm, _ := fp["model"].(string)
			if fm == "" {
				fm = "free"
			}
			params["model"] = fm
			if v, ok := fp[servedByParam].(string); ok {
				params[servedByParam] = v
			} else {
				delete(params, servedByParam)
			}
			if v, ok := fp[providerNameParam].(string); ok {
				params[providerNameParam] = v // free 链经 provider 服务时同步归因名
				params[providers.ProviderIDParam] = fp[providers.ProviderIDParam]
			} else {
				delete(params, providerNameParam)
				delete(params, providers.ProviderIDParam)
			}
			trace = append(trace, domain.AttemptLog{
				Model: fm, Order: -1, Upstream: zen.UpstreamCline, Outcome: "success",
				Reason: "emergency fallback outside combo (opt-in)",
			})
			return Run{Resp: resp, Acc: acc, Upstream: zen.UpstreamCline, Model: fm, Trace: trace}
		} else {
			lastErr = err
			trace = append(trace, domain.AttemptLog{
				Model: "free", Order: -1, Upstream: zen.UpstreamCline, Outcome: "failed",
				Reason: platform.Truncate(err.Error(), 200), Status: AttemptStatus(err),
			})
		}
	}

	if lastErr == nil {
		lastErr = NewUnavailableError(fmt.Sprintf("combo %q has no eligible entries", combo.ID))
	}
	return Run{Trace: trace, Err: lastErr}
}

// ApplyLog 把组合运行轨迹写入请求日志（保持 Model 语义为实际服务模型）。
func ApplyLog(reqLog *domain.RequestLog, combo domain.ModelCombo, trace []domain.AttemptLog, servedModel string) {
	reqLog.RequestedModel = combo.ID
	reqLog.ComboID = combo.ID
	reqLog.ComboName = combo.Name
	reqLog.Attempts = trace
	fallback := 0
	for _, a := range trace {
		if a.Outcome == "failed" || a.Outcome == "skipped" {
			fallback++
		}
	}
	reqLog.FallbackCount = fallback
	reqLog.FallbackUsed = fallback > 0
	if servedModel != "" {
		reqLog.Model = servedModel
	}
}

// ApplyTrace 把组合运行结果写入请求日志。
func ApplyTrace(reqLog *domain.RequestLog, combo domain.ModelCombo, run Run) {
	ApplyLog(reqLog, combo, run.Trace, run.Model)
}

// TraceSummary 生成简洁的失败说明（用于最终错误信息）。
func TraceSummary(combo domain.ModelCombo, run Run) string {
	var attempted []string
	for _, a := range run.Trace {
		if a.Outcome == "success" {
			continue
		}
		label := a.Model
		if a.ProviderName != "" {
			label = a.ProviderName + "/" + a.Model
		}
		attempted = append(attempted, fmt.Sprintf("%s(%s)", label, a.Outcome))
	}
	sort.Strings(attempted)
	if len(attempted) == 0 {
		return fmt.Sprintf("model %q unavailable", combo.ID)
	}
	return fmt.Sprintf("model %q unavailable: %s", combo.ID, strings.Join(attempted, ", "))
}

// ============================================================================
// 管理 API
// ============================================================================

// Catalog 返回组合条目可引用的模型目录（供管理页选择器使用）。
func Catalog() []map[string]any {
	models := routing.ModelsProvider()
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		cost := m.Cost
		if zen.IsSource(m) && zen.IsFreeModel(m) {
			cost = "free" // 与路由判定保持一致
		}
		out = append(out, map[string]any{
			"id":       m.ID,
			"provider": m.Provider,
			"source":   m.Source,
			"cost":     cost,
			"disabled": m.Disabled,
			"delisted": m.Delisted,
		})
	}
	return out
}
