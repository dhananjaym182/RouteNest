package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/platform"
	poolstore "routenest/internal/store/accounts"
)

var (
	// Pool is the in-memory account pool. It is exported for the packages that
	// still share the pool lock; new model access goes through internal/models.
	Pool     *AccountPool
	PoolMu   sync.Mutex
	PoolPath string

	poolLoadErr     error
	poolLoadErrPath string
)

type PoolStorageState string

const (
	PoolStorageFirstRun   PoolStorageState = "first_run"
	PoolStorageReady      PoolStorageState = "ready"
	PoolStorageMissing    PoolStorageState = "missing_after_initialization"
	PoolStorageCorrupt    PoolStorageState = "corrupt"
	PoolStorageUnreadable PoolStorageState = "unreadable"
)

var ErrPoolStorageUnavailable = errors.New("account storage unavailable")
var errInvalidPoolMarker = errors.New("invalid account storage marker")

const poolMarkerVersion = 1

const InitializeEmptyAccountStoreEnv = "ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE"

type poolMarker struct {
	Version int `json:"version"`
}

// poolPathMu guards first-use resolution of PoolPath. Code that needs both
// locks takes PoolMu first, then poolPathMu, matching LoadPool and persistence.
var poolPathMu sync.Mutex

// ensurePoolPath resolves the accounts file path on first use, so importing this
// package performs no data-directory I/O. Tests that assign PoolPath keep it.
func ensurePoolPath() {
	poolPathMu.Lock()
	defer poolPathMu.Unlock()
	if PoolPath == "" {
		PoolPath = platform.ResolveDataPath(".routenest-accounts.json", nil)
	}
}

func LoadPool() *AccountPool {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	ensurePoolPath()

	if Pool != nil {
		return Pool
	}

	p, _, err := loadPoolFromDisk(PoolPath)
	if err != nil {
		poolLoadErr = err
		poolLoadErrPath = PoolPath
		Pool = poolstore.Empty()
		log.Printf("Client API-key storage unavailable at %s: %v", PoolPath, err)
		return Pool
	}
	poolLoadErr = nil
	poolLoadErrPath = ""
	Pool = p
	return Pool
}

func PoolStorageError() error {
	LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return PoolStorageErrorLocked()
}

func PoolStorageErrorLocked() error {
	if poolLoadErrPath != PoolPath {
		return nil
	}
	return poolLoadErr
}

func LoadPoolFromDisk(path string) (*AccountPool, PoolStorageState, error) {
	return loadPoolFromDisk(path)
}

func loadPoolFromDisk(path string) (*AccountPool, PoolStorageState, error) {
	if strings.TrimSpace(path) == "" {
		return nil, PoolStorageUnreadable, fmt.Errorf("%w: account storage path is empty", ErrPoolStorageUnavailable)
	}
	markerExists, markerErr := loadPoolMarker(path)
	if markerErr != nil {
		state := PoolStorageUnreadable
		if jsonError(markerErr) || errors.Is(markerErr, errInvalidPoolMarker) {
			state = PoolStorageCorrupt
		}
		return nil, state, fmt.Errorf("%w: inspect initialization marker for %s: %v", ErrPoolStorageUnavailable, path, markerErr)
	}
	_, statErr := os.Stat(path)
	missing := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !missing {
		return nil, PoolStorageUnreadable, fmt.Errorf("%w: read %s: %v", ErrPoolStorageUnavailable, path, statErr)
	}
	if missing {
		if markerExists {
			return nil, PoolStorageMissing, fmt.Errorf("%w: initialized account storage %s is missing; restore it from backup", ErrPoolStorageUnavailable, path)
		}
		backup, err := corruptPoolBackupExists(path)
		if err != nil {
			return nil, PoolStorageUnreadable, fmt.Errorf("%w: inspect backups for %s: %v", ErrPoolStorageUnavailable, path, err)
		}
		if backup {
			return nil, PoolStorageCorrupt, fmt.Errorf("%w: %s is missing after a corrupt-file backup; restore or repair it", ErrPoolStorageUnavailable, path)
		}
		established, err := platform.EstablishedDataEvidence(path)
		if err != nil {
			return nil, PoolStorageUnreadable, fmt.Errorf("%w: inspect installation evidence for %s: %v", ErrPoolStorageUnavailable, path, err)
		}
		if established {
			return nil, PoolStorageMissing, fmt.Errorf("%w: account storage and its initialization witness are missing from an established data directory; restore a valid account-pool file and restart RouteNest", ErrPoolStorageUnavailable)
		}
		if os.Getenv(InitializeEmptyAccountStoreEnv) != "true" {
			return nil, PoolStorageMissing, fmt.Errorf("%w: account storage is uninitialized; for a confirmed fresh installation set %s=true for one startup, otherwise restore the account-pool file", ErrPoolStorageUnavailable, InitializeEmptyAccountStoreEnv)
		}
		p := poolstore.Empty()
		if err := poolstore.SaveDurable(path, p); err != nil {
			return nil, PoolStorageUnreadable, fmt.Errorf("%w: initialize %s: %v", ErrPoolStorageUnavailable, path, err)
		}
		if err := savePoolMarker(path); err != nil {
			return nil, PoolStorageUnreadable, fmt.Errorf("%w: initialize marker for %s: %v", ErrPoolStorageUnavailable, path, err)
		}
		return p, PoolStorageFirstRun, nil
	}
	p, err := poolstore.Load(path)
	if err != nil {
		if jsonError(err) {
			platform.BackupCorruptFile(path, err)
			return nil, PoolStorageCorrupt, fmt.Errorf("%w: %s is corrupt and was preserved as a .corrupt-* backup: %v", ErrPoolStorageUnavailable, path, err)
		}
		return nil, PoolStorageUnreadable, fmt.Errorf("%w: read %s: %v", ErrPoolStorageUnavailable, path, err)
	}
	if !markerExists {
		if err := savePoolMarker(path); err != nil {
			return nil, PoolStorageUnreadable, fmt.Errorf("%w: initialize marker for %s: %v", ErrPoolStorageUnavailable, path, err)
		}
	}
	return p, PoolStorageReady, nil
}

func loadPoolMarker(path string) (bool, error) {
	markerPath := path + ".initialized"
	info, err := os.Lstat(markerPath)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return true, errInvalidPoolMarker
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var marker poolMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return true, err
	}
	if marker.Version != poolMarkerVersion {
		return true, errInvalidPoolMarker
	}
	return true, nil
}

func savePoolMarker(path string) error {
	data, err := json.Marshal(poolMarker{Version: poolMarkerVersion})
	if err != nil {
		return err
	}
	return platform.WriteFileAtomicDurable(path+".initialized", data, 0600)
}

func corruptPoolBackupExists(path string) (bool, error) {
	matches, err := filepath.Glob(path + ".corrupt-*")
	return len(matches) > 0, err
}

func jsonError(err error) bool {
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	return errors.As(err, &syntaxError) || errors.As(err, &typeError) || errors.Is(err, poolstore.ErrInvalidDocument)
}

func SavePool() {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	SavePoolLocked()
}

// SavePoolLocked persists the live pool while the caller holds PoolMu.
func SavePoolLocked() {
	if err := SavePoolStrictLocked(); err != nil {
		log.Printf("Failed to save accounts to %s: %v", PoolPath, err)
	}
}

// SavePoolStrict persists the current pool and returns any durability error to
// callers that must fail closed. Most legacy call sites use SavePool's
// best-effort logging, while security migrations must not acknowledge success
// unless the old credential authority was durably replaced.
func SavePoolStrict() error {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return SavePoolStrictLocked()
}

// SavePoolStrictLocked is the fail-closed persistence primitive for mutations
// already protected by PoolMu. Callers must hold PoolMu for the entire mutation
// and save so JSON serialization never observes a partially updated pool.
func SavePoolStrictLocked() error {
	ensurePoolPath()
	if poolLoadErr != nil && poolLoadErrPath == PoolPath {
		return poolLoadErr
	}
	return poolstore.Save(PoolPath, Pool)
}

func DeleteAllAccountsStrict() error {
	return deleteAllAccountsStrict(poolstore.SaveDurable)
}

func deleteAllAccountsStrict(save func(string, *AccountPool) error) error {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	ensurePoolPath()
	p, _, err := loadPoolFromDisk(PoolPath)
	if err != nil {
		poolLoadErr = err
		poolLoadErrPath = PoolPath
		return err
	}
	next := clonePool(p)
	next.Accounts = []*Account{}
	next.CurrentIdx = 0
	if err := save(PoolPath, next); err != nil {
		if platform.AtomicWriteMayHaveReplaced(err) {
			persisted, reloadErr := poolstore.Load(PoolPath)
			if reloadErr != nil {
				poolLoadErr = errors.Join(err, reloadErr)
				poolLoadErrPath = PoolPath
				Pool = poolstore.Empty()
				return poolLoadErr
			}
			publishPoolLocked(persisted)
		}
		return err
	}
	publishPoolLocked(next)
	poolLoadErr = nil
	poolLoadErrPath = ""
	return nil
}

func publishPoolLocked(next *AccountPool) {
	if Pool == nil {
		Pool = next
		return
	}
	*Pool = *next
}

func clonePool(pool *AccountPool) *AccountPool {
	if pool == nil {
		return poolstore.Empty()
	}
	next := *pool
	next.Accounts = append([]*Account(nil), pool.Accounts...)
	next.Keys = append([]string(nil), pool.Keys...)
	next.Models = append([]Model(nil), pool.Models...)
	next.RemovedModelOfferings = append([]domain.ModelRemovalRef(nil), pool.RemovedModelOfferings...)
	return &next
}

// AddAccount 添加账号。同一邮箱（不区分大小写，来自 Cline userInfo，即同一
// Cline 账号）已存在时**原地更新凭据**：重新授权/重复导入不该产生重复条目 ——
// 保留原 AccountID、创建时间与用量统计，只更新令牌并置回 active。
// 已存在的同邮箱重复条目（历史遗留）一并合并进首个条目后移除（用量计数求和）。
func AddAccount(acc *Account) {
	p := LoadPool()
	PoolMu.Lock()
	newEmail := strings.TrimSpace(acc.Email)
	var kept *Account
	var dups []*Account
	for _, a := range p.Accounts {
		if newEmail != "" && strings.EqualFold(strings.TrimSpace(a.Email), newEmail) {
			if kept == nil {
				kept = a
			} else {
				dups = append(dups, a)
			}
		}
	}
	if kept != nil {
		kept.RefreshToken = acc.RefreshToken
		kept.AccessToken = acc.AccessToken
		kept.ExpiresAt = acc.ExpiresAt
		kept.Status = "active"
		if len(dups) > 0 {
			for _, d := range dups {
				kept.UsageCount += d.UsageCount
				kept.PromptTokens += d.PromptTokens
				kept.CompletionTokens += d.CompletionTokens
				kept.TotalTokens += d.TotalTokens
				kept.CachedTokens += d.CachedTokens
			}
			out := p.Accounts[:0]
			for _, a := range p.Accounts {
				isDup := false
				for _, d := range dups {
					if a == d {
						isDup = true
						break
					}
				}
				if !isDup {
					out = append(out, a)
				}
			}
			p.Accounts = out
			log.Printf("Account %s: merged %d duplicate entr(y/ies), stats preserved", acc.Email, len(dups))
		}
		log.Printf("Account re-authorized in place: %s (AccountID/stats preserved)", acc.Email)
	} else {
		p.Accounts = append(p.Accounts, acc)
	}
	SavePoolLocked()
	PoolMu.Unlock()
}

// FindAccountByRefreshToken 按 refreshToken 查找已有账号（不存在返回 nil）。
// 用于导入时的去重：同一个 refreshToken 只应存在一个账号。
func FindAccountByRefreshToken(refreshToken string) *Account {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil
	}
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	for _, a := range p.Accounts {
		if strings.TrimSpace(a.RefreshToken) == refreshToken {
			return a
		}
	}
	return nil
}

// AccountExists 判断该 refreshToken 是否已在账号池中。
func AccountExists(refreshToken string) bool {
	return FindAccountByRefreshToken(refreshToken) != nil
}

// IsDuplicateImportToken 判断待导入的 refreshToken 是否应跳过（导入去重）：
// 账号池中已存在同一 refreshToken，或本批次内已处理过（seen）。
// seen 由调用方维护、在此更新，用于同一批次内的去重。
func IsDuplicateImportToken(refreshToken string, seen map[string]bool) bool {
	if seen[refreshToken] || AccountExists(refreshToken) {
		return true
	}
	seen[refreshToken] = true
	return false
}

func RemoveAccount(accountID string) bool {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	for i, a := range p.Accounts {
		if a.AccountID == accountID {
			p.Accounts = append(p.Accounts[:i], p.Accounts[i+1:]...)
			SavePoolLocked()
			return true
		}
	}
	return false
}

func GetAccountByID(accountID string) *Account {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	for _, a := range p.Accounts {
		if a.AccountID == accountID {
			return a
		}
	}
	return nil
}

// GetAccountByCredentialID resolves the global redacted credential identity to
// its secret-bearing account record for the application dispatch resolver.
func GetAccountByCredentialID(credentialID domain.CredentialID) *Account {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()
	for _, account := range p.Accounts {
		if domain.ClineCredentialID(account.AccountID, account.Email) == credentialID {
			return account
		}
	}
	return nil
}

func accountRefreshToken(acc *Account) string {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return acc.RefreshToken
}

func accountAccessState(acc *Account) (string, int64) {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return acc.AccessToken, acc.ExpiresAt
}

// CurrentAccessToken returns the shared account token under the pool's account-
// state synchronization boundary.
func CurrentAccessToken(acc *Account) string {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return acc.AccessToken
}

// AccountAvailability snapshots the mutable account-wide routing state.
func AccountAvailability(acc *Account) (string, time.Time) {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return acc.Status, acc.CooldownUntil
}

func persistAccountUpdate(update func()) {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	update()
	SavePoolLocked()
}

// SetAccountCooldown changes account-wide availability and persists the state
// before releasing the account-state lock.
func SetAccountCooldown(acc *Account, until time.Time) {
	persistAccountUpdate(func() {
		acc.Status = "cooldown"
		acc.CooldownUntil = until
	})
}

// SetAccountStatus changes and persists an account status under the pool lock.
func SetAccountStatus(acc *Account, status string) {
	persistAccountUpdate(func() { acc.Status = status })
}

// ActivateAccount persists a recovery only when the account is not active.
func ActivateAccount(acc *Account) {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	if acc.Status == "active" {
		return
	}
	acc.Status = "active"
	SavePoolLocked()
}

// RecordAccountUse updates last-use and aggregate request count atomically.
func RecordAccountUse(acc *Account, usedAt time.Time) {
	persistAccountUpdate(func() {
		acc.LastUsed = usedAt
		acc.UsageCount++
	})
}

func RefreshAccountToken(acc *Account) error {
	refreshToken := accountRefreshToken(acc)
	resp, err := refreshClineToken(refreshToken)
	if err != nil {
		if isRefreshRejected(err) {
			// cline 自家 refresh 端点拒绝 ≠ 账号真失效：refresh token 本质是
			// WorkOS token，走 WorkOS authenticate 端点仍可能自愈，不必重新授权。
			if wos := refreshViaWorkOS(refreshToken); wos != nil {
				ApplyWorkOSTokens(acc, wos)
				return nil
			}
			SetAccountStatus(acc, "expired")
			// 双通道都拒绝 = WorkOS 会话已被服务端终结（invalid_grant: Session
			// has already ended / token revoked），刷新救不回，必须重新授权
			return fmt.Errorf("refresh token rejected by both Cline and WorkOS (session ended or revoked) — account requires re-authorization: 管理页「导入账号」→ OAuth 登录")
		}
		SetAccountCooldown(acc, time.Now().Add(5*time.Minute))
		return fmt.Errorf("token refresh failed (transient): %w", err)
	}

	persistAccountUpdate(func() {
		acc.AccessToken = "workos:" + resp.Data.AccessToken
		if resp.Data.RefreshToken != "" {
			acc.RefreshToken = resp.Data.RefreshToken
		}
		acc.ExpiresAt = parseExpiry(resp.Data.ExpiresAt) - 60000
		acc.Status = "active"
	})
	return nil
}

// ApplyWorkOSTokens 用 WorkOS 直接刷新到的令牌续命账号：优先回 cline
// /auth/register 换正式 cline 令牌；注册也失败时直接用 workos 前缀令牌
// （Cline 上游接受），轮换出的新 refresh token 一并落盘。
func ApplyWorkOSTokens(acc *Account, wos *authenticateResp) {
	var accessToken, refreshToken string
	var expiresAt int64
	if cline, err := registerWithCline(wos.AccessToken, wos.RefreshToken); err == nil && cline.Data.AccessToken != "" {
		accessToken = "workos:" + cline.Data.AccessToken
		refreshToken = cline.Data.RefreshToken
		expiresAt = parseExpiry(cline.Data.ExpiresAt) - 60000
	} else {
		accessToken = "workos:" + wos.AccessToken
		refreshToken = wos.RefreshToken
		expiresAt = time.Now().Add(time.Hour).UnixMilli() - 60000
	}
	email := ""
	persistAccountUpdate(func() {
		acc.AccessToken = accessToken
		if refreshToken != "" {
			acc.RefreshToken = refreshToken
		}
		acc.ExpiresAt = expiresAt
		acc.Status = "active"
		email = acc.Email
	})
	log.Printf("  account %s: token refreshed via WorkOS fallback (no re-auth needed)", email)
}

func PickAccount() *Account {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()
	return pickAccountLocked(p)
}

// PickAccountForModel 按轮询/策略挑选一个「该模型未处于模型级冷却」的账号；
// 所有 active 账号对该模型都冷却时回退到普通 PickAccount（请求会得到模型级 429 提示）。
// 空模型名等同于 PickAccount。
func PickAccountForModel(model string) *Account {
	return PickAccountForModelWithFallback(model, true)
}

func PickAccountForModelStrict(model string) *Account {
	return PickAccountForModelWithFallback(model, false)
}

func PickAccountForModelWithFallback(model string, fallbackToActive bool) *Account {
	if model == "" {
		return PickAccount()
	}

	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	active := make([]*Account, 0)
	for _, a := range p.Accounts {
		if a.Status == "active" {
			active = append(active, a)
		}
	}
	if len(active) == 0 {
		return nil
	}

	// 该模型未冷却的账号列表
	eligible := make([]*Account, 0, len(active))
	for _, a := range active {
		until, cool := a.ModelCooldowns[model]
		if !cool || time.Now().After(until) {
			if cool {
				delete(a.ModelCooldowns, model)
			}
			eligible = append(eligible, a)
		}
	}

	if len(eligible) == 0 {
		if fallbackToActive {
			return pickAccountLocked(p)
		}
		return nil
	}

	cfg := currentConfig()
	var acc *Account
	switch cfg.Strategy {
	case "fill":
		acc = eligible[0]
	case "random":
		n := time.Now().UnixNano() % int64(len(eligible))
		acc = eligible[n]
	default: // round_robin
		if p.CurrentIdx >= len(eligible) {
			p.CurrentIdx = 0
		}
		acc = eligible[p.CurrentIdx]
		p.CurrentIdx = (p.CurrentIdx + 1) % len(eligible)
	}
	SavePoolLocked()
	return acc
}

// PickAccountForModelLeastUsed 在所有「模型未冷却」的 active 账号中，选择该模型
// 历史用量最少的账号（并清掉已过期的冷却记录）。等量时按轮询索引取，保持原有
// 公平性；全部不可用返回 nil。供回退链上的非首选模型使用：流量应摊到较少
// 使用的账号上，而不是每次都砸在第一个可用账号。
func PickAccountForModelLeastUsed(model string) *Account {
	if model == "" {
		return PickAccount()
	}

	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	var best *Account
	var bestCount int64
	for _, a := range p.Accounts {
		if a.Status != "active" {
			continue
		}
		if until, cool := a.ModelCooldowns[model]; cool {
			if time.Now().After(until) {
				delete(a.ModelCooldowns, model)
			} else {
				continue // 该账号此模型冷却中
			}
		}
		var cnt int64
		if st, ok := a.ModelStats[model]; ok {
			cnt = st.UsageCount
		}
		if best == nil || cnt < bestCount {
			best, bestCount = a, cnt
		}
	}
	if best == nil {
		return nil
	}
	SavePoolLocked()
	return best
}

// SortModelsByAvailability 将回退链按「可用性优先」重排：
//  1. 有未冷却账号的模型在前；
//  2. 同组内按该模型的账号总用量升序——把流量摊到用得少的模型上，
//     避免每次都选第一个可用模型、把它的额度打到冷却。
//  3. 稳定排序：可用性与用量相同时保持管理员配置的优先级顺序。
func SortModelsByAvailability(chain []string) []string {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	now := time.Now()
	cands := make([]dispatch.ModelAvailability, 0, len(chain))
	for _, m := range chain {
		avail := false
		minUsage := int64(-1)
		for _, a := range p.Accounts {
			if a.Status != "active" {
				continue
			}
			if until, cool := a.ModelCooldowns[m]; cool {
				if now.After(until) {
					delete(a.ModelCooldowns, m)
				} else {
					continue
				}
			}
			avail = true
			var cnt int64
			if st, ok := a.ModelStats[m]; ok {
				cnt = st.UsageCount
			}
			if minUsage < 0 || cnt < minUsage {
				minUsage = cnt
			}
		}
		cands = append(cands, dispatch.ModelAvailability{Model: m, Available: avail, MinUsage: minUsage})
	}
	out := dispatch.OrderByAvailability(cands)
	SavePoolLocked()
	return out
}

// pickAccountLocked 在已持有 PoolMu 的前提下执行普通轮询挑选（供 PickAccountForModel 回退用）。
func pickAccountLocked(p *AccountPool) *Account {
	active := make([]*Account, 0)
	for _, a := range p.Accounts {
		if a.Status == "active" {
			active = append(active, a)
		}
	}
	if len(active) == 0 {
		return nil
	}
	cfg := currentConfig()
	var acc *Account
	switch cfg.Strategy {
	case "fill":
		acc = active[0]
	case "random":
		n := time.Now().UnixNano() % int64(len(active))
		acc = active[n]
	default:
		if p.CurrentIdx >= len(active) {
			p.CurrentIdx = 0
		}
		acc = active[p.CurrentIdx]
		p.CurrentIdx = (p.CurrentIdx + 1) % len(active)
	}
	SavePoolLocked()
	return acc
}

func EnsureAccountToken(acc *Account) (string, error) {
	accessToken, expiresAt := accountAccessState(acc)
	if accessToken != "" && time.Now().UnixMilli() < expiresAt {
		return accessToken, nil
	}

	if err := RefreshAccountToken(acc); err != nil {
		return "", err
	}

	return CurrentAccessToken(acc), nil
}

func ListAccounts() []*Account {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()

	result := make([]*Account, len(p.Accounts))
	for i, a := range p.Accounts {
		// Don't expose tokens
		cp := &Account{
			AccountID:        a.AccountID,
			Email:            a.Email,
			Status:           a.Status,
			CooldownUntil:    a.CooldownUntil,
			LastUsed:         a.LastUsed,
			UsageCount:       a.UsageCount,
			PromptTokens:     a.PromptTokens,
			CompletionTokens: a.CompletionTokens,
			TotalTokens:      a.TotalTokens,
			CachedTokens:     a.CachedTokens,
			CreatedAt:        a.CreatedAt,
		}
		// 按模型细分统计（脱敏拷贝）
		if len(a.ModelStats) > 0 {
			cp.ModelStats = make(map[string]*ModelStat, len(a.ModelStats))
			for mid, st := range a.ModelStats {
				sc := *st
				cp.ModelStats[mid] = &sc
			}
		}
		// 模型级冷却（脱敏拷贝）
		if len(a.ModelCooldowns) > 0 {
			cp.ModelCooldowns = make(map[string]time.Time, len(a.ModelCooldowns))
			for mid, until := range a.ModelCooldowns {
				cp.ModelCooldowns[mid] = until
			}
		}
		result[i] = cp
	}
	return result
}

func AddAccountFromDeviceAuth() (*Account, error) {
	fmt.Println()
	fmt.Println("=== Add New Cline Account (OAuth) ===")
	fmt.Println()

	device, err := workosDeviceAuth()
	if err != nil {
		return nil, err
	}

	authURL := device.VerificationURIComplete
	if authURL == "" {
		authURL = device.VerificationURI
	}

	fmt.Println("  1. Open this URL in your browser:")
	fmt.Println("     " + authURL)
	fmt.Println("  2. Enter code: " + device.UserCode)
	fmt.Println("  3. Log in with Google, GitHub, or email")
	fmt.Println()

	_ = openBrowser(authURL)
	fmt.Println("  Waiting for authorization...")

	interval := device.Interval
	if interval < 5 {
		interval = 5
	}
	expiresIn := device.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 300
	}

	workosTok, err := pollWorkosToken(device.DeviceCode, interval, expiresIn)
	if err != nil {
		return nil, err
	}

	fmt.Println("  WorkOS authorized. Registering with Cline...")

	cline, err := registerWithCline(workosTok.AccessToken, workosTok.RefreshToken)
	if err != nil {
		return nil, err
	}

	if cline.Data.RefreshToken == "" {
		return nil, fmt.Errorf("cline registration missing refresh token")
	}

	email := "unknown"
	if cline.Data.UserInfo != nil && cline.Data.UserInfo.Email != "" {
		email = cline.Data.UserInfo.Email
	}

	acc := &Account{
		AccountID:    fmt.Sprintf("acc_%d", time.Now().UnixMilli()),
		Email:        email,
		RefreshToken: cline.Data.RefreshToken,
		AccessToken:  "workos:" + cline.Data.AccessToken,
		ExpiresAt:    parseExpiry(cline.Data.ExpiresAt) - 60000,
		Status:       "active",
		CreatedAt:    time.Now(),
	}

	AddAccount(acc)
	fmt.Printf("  Account added! Email: %s\n", email)
	return acc, nil
}
