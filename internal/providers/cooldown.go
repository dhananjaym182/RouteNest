package providers

import (
	"log"
	"time"

	"routenest/internal/dispatch"
	"routenest/internal/platform"
	providerstore "routenest/internal/store/providers"
)

// SetCooldown 记录 provider 模型冷却（限流/5xx/配置错误时调用）。
// 同步落盘：重启后不再把刚冷却的 provider 当健康源重新探一遍 —— 对 429 这类
// 长冷却（幂重置 EP 违 30m 封顶）尤其必要。
func SetCooldown(providerID, model string, until time.Time) {
	Mu.Lock()
	Cooldowns[providerID+":"+model] = until
	saveProviderCooldownsLocked()
	Mu.Unlock()
	stickyClearForProvider(providerID) // 粘贴会话不要粘在冷却中的 provider 上
}

// providerRateLimitedKeys reports explicit quota/rate-limit keywords.
func providerRateLimitedKeys(body string) bool {
	return dispatch.RateLimitedByBody(body)
}

// ============================================================================
// Provider 路由加固：冷却落盘 / 粘贴会话 / 实测成绩排序 / 预算上限
// ============================================================================

// ---- 冷却落盘：provider:model → 恢复时间（unixMicro）--------------------

// loadProviderCooldownsLocked 读取冷却文件（与 provider registry 同锁调用），
// 过期条目直接丢弃。文件缺失/损坏只记日志，不阻塞 provider 初始化。
// 调方必须已持有 Mu。
func loadProviderCooldownsLocked() {
	ensurePaths()
	loaded, err := providerstore.LoadCooldowns(CooldownsPath)
	if err != nil {
		platform.BackupCorruptFile(CooldownsPath, err)
		return
	}
	for k, until := range loaded {
		Cooldowns[k] = until
	}
}

// saveProviderCooldownsLocked 把未过期的冷却条目原子写盘。调方必须已持有
// Mu。写失败只记日志：冷却丢失的最坏后果是重启后多探测一次。
func saveProviderCooldownsLocked() {
	ensurePaths()
	if err := providerstore.SaveCooldowns(CooldownsPath, Cooldowns); err != nil {
		log.Printf("provider cooldowns save failed: %v", err)
	}
}
