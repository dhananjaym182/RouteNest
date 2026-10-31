package combos

import (
	"fmt"
	"routenest/internal/domain"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"sort"
	"strings"
)

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
