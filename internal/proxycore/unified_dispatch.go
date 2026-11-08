package proxycore

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"routenest/internal/combos"
	"routenest/internal/compaction"
	"routenest/internal/config"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/providers"
	"routenest/internal/routing"
	"routenest/internal/zen"
)

type unifiedDispatchCall struct {
	runtime     *dispatch.Runtime
	request     dispatch.Request
	mode        config.DispatchMode
	execution   *dispatch.Execution
	result      dispatch.Result
	diagnostics routing.PlanDiagnostics
	plan        routing.CandidatePlan
	params      map[string]any
	reqLog      *domain.RequestLog
}

func legacyIngressFreeOnlyRejection(model string) string {
	if liveConfig().DispatchMode == config.DispatchModeEnforce {
		return ""
	}
	return routing.FreeOnlyRejection(model)
}

func prepareUnifiedParams(params map[string]any, model, sessionID, protocol string, mode config.DispatchMode) (domain.ModelCombo, bool, error) {
	if combo, ok := combos.FindCombo(model); ok {
		if !combo.Enabled {
			return domain.ModelCombo{}, false, fmt.Errorf("model combo %q is disabled", model)
		}
		if out := compaction.MaybeCompact(params, combos.CompactionModel(combo), sessionID); out.Changed {
			log.Printf("  %s combo %q %s", protocol, combo.ID, out.Note)
		}
		return combo, true, nil
	}
	_, pin, err := routing.ResolveModelReference(model)
	if err != nil {
		return domain.ModelCombo{}, false, err
	}
	if pin.ProviderID != "" || pin.ProviderName != "" {
		compaction.CompactForRequest(params, compaction.ModelForCompaction(model), sessionID, protocol)
		return domain.ModelCombo{}, false, nil
	}
	if mode != config.DispatchModeEnforce && zen.RouteModel(model) == "reject" {
		return domain.ModelCombo{}, false, fmt.Errorf("model %q is a paid opencode model; only free models are proxied", model)
	}
	if zenModel, ok := zen.ResolveInfo(model); ok && zen.IsFreeModel(zenModel) {
		if out := compaction.MaybeCompact(params, zenModel, sessionID); out.Changed {
			log.Printf("  %s %s", protocol, out.Note)
		}
	} else {
		compaction.CompactForRequest(params, compaction.ModelForCompaction(model), sessionID, protocol)
	}
	return domain.ModelCombo{}, false, nil
}

func unifiedPlan(cfg *config.Proxy, model string, params map[string]any, combo domain.ModelCombo, isCombo bool) (routing.CandidatePlan, routing.PlanDiagnostics, error) {
	options := routing.SnapshotOptions{
		ModelID: model, OnlyFree: cfg.OnlyFree, Strategy: cfg.Strategy,
		AffinityKey: providers.SessionKeyFromParams(params),
	}
	if isCombo {
		return routing.SnapshotAndPlanCombo(options, combo)
	}
	_, pin, err := routing.ResolveModelReference(model)
	if err != nil {
		return routing.CandidatePlan{}, routing.PlanDiagnostics{}, err
	}
	if pin.ProviderID != "" || pin.ProviderName != "" {
		return routing.SnapshotAndPlan(options, nil)
	}
	return routing.SnapshotAndPlan(options, routing.ModelFallbackChain(model))
}

func traceToAttemptLog(trace []dispatch.TraceRecord) []domain.AttemptLog {
	out := make([]domain.AttemptLog, 0, len(trace))
	for _, record := range trace {
		outcome := "failed"
		switch record.Outcome {
		case "selected", "success":
			outcome = "success"
		case "skipped":
			outcome = "skipped"
		}
		out = append(out, domain.AttemptLog{
			Model: record.ModelID, UpstreamModelID: record.UpstreamModelID,
			Order: record.Order, Group: record.Group, Status: record.StatusCode,
			Outcome: outcome, DispatchOutcome: record.Outcome,
			Reason: record.Reason, DurationMs: record.DurationMS,
			Provider: string(record.ProviderID), ProviderName: record.ProviderName,
			CredentialID: string(record.CredentialID), EgressID: record.EgressID,
		})
	}
	return out
}

func exclusionLogs(exclusions []routing.CandidateExclusion) []domain.PlannerExclusionLog {
	out := make([]domain.PlannerExclusionLog, 0, len(exclusions))
	for _, exclusion := range exclusions {
		out = append(out, domain.PlannerExclusionLog{
			ProviderID: string(exclusion.ProviderID), Model: exclusion.ModelID,
			Reason: string(exclusion.Reason),
		})
	}
	return out
}

func (call *unifiedDispatchCall) applyPlanMetadata(combo domain.ModelCombo, isCombo bool) {
	if call.reqLog == nil {
		return
	}
	call.reqLog.PlannerExclusions = exclusionLogs(call.diagnostics.Exclusions)
	if call.diagnostics.RequestedModel != "" && call.diagnostics.RequestedModel != call.reqLog.Model {
		call.reqLog.RequestedModel = call.diagnostics.RequestedModel
	}
	if isCombo {
		call.reqLog.RequestedModel = combo.ID
		call.reqLog.ComboID = combo.ID
		call.reqLog.ComboName = combo.Name
	}
}

func (call *unifiedDispatchCall) apply(result dispatch.Result) {
	call.result = result
	serving := result.Serving
	if serving.LogicalModelID == "" && len(result.Trace) > 0 {
		last := result.Trace[len(result.Trace)-1]
		serving = dispatch.CandidateAttempt{
			LogicalModelID: last.ModelID, UpstreamModelID: last.UpstreamModelID,
			ProviderID: last.ProviderID, ProviderName: last.ProviderName,
			CredentialID: last.CredentialID, EgressID: last.EgressID,
		}
	}
	if serving.LogicalModelID != "" {
		call.params["model"] = serving.LogicalModelID
		if call.reqLog != nil {
			call.reqLog.Model = serving.LogicalModelID
		}
	}
	delete(call.params, routing.ServedByParam)
	delete(call.params, routing.ProviderNameParam)
	delete(call.params, providers.ProviderIDParam)
	if serving.ProviderID != "" && serving.ProviderID != "cline" && serving.ProviderID != "opencode" {
		call.params[routing.ServedByParam] = zen.UpstreamProvider
		call.params[routing.ProviderNameParam] = serving.ProviderName
		call.params[providers.ProviderIDParam] = string(serving.ProviderID)
	}
	if call.reqLog == nil {
		return
	}
	if result.RequestID != "" {
		call.reqLog.ID = result.RequestID
	}
	call.reqLog.BudgetAttemptID = result.AttemptID
	stampUpstream(call.reqLog, call.params)
	if serving.ProviderID == "opencode" {
		call.reqLog.Upstream = zen.UpstreamOpenCode
	} else if serving.ProviderID == "cline" {
		call.reqLog.Upstream = zen.UpstreamCline
	}
	if result.Account != nil {
		call.reqLog.AccountID = result.Account.AccountID
		call.reqLog.AccountEmail = result.Account.Email
	}
	if result.ConnectionID != "" {
		call.reqLog.ConnectionID = result.ConnectionID
	}
	call.applyObservability(result)
}

func (call *unifiedDispatchCall) applyObservability(result dispatch.Result) {
	if len(result.Trace) == 0 {
		return
	}
	call.result.Trace = append([]dispatch.TraceRecord(nil), result.Trace...)
	call.result.FallbackCount = result.FallbackCount
	if call.reqLog == nil {
		return
	}
	call.reqLog.Attempts = traceToAttemptLog(result.Trace)
	call.reqLog.FallbackCount = result.FallbackCount
	call.reqLog.FallbackUsed = result.FallbackCount > 0
}

func shadowCompare(diagnostics routing.PlanDiagnostics, result dispatch.Result) {
	planned := ""
	if len(diagnostics.Candidates) > 0 {
		planned = diagnostics.Candidates[0]
	}
	served := fmt.Sprintf("%s:%s:%s", result.Serving.ProviderID, result.Serving.LogicalModelID, result.Serving.UpstreamModelID)
	if planned != served {
		log.Printf("  dispatch shadow mismatch: planned=%q served=%q exclusions=%d", planned, served, len(diagnostics.Exclusions))
	}
}

func startUnifiedDispatch(ctx context.Context, params map[string]any, stream bool, protocol, sessionID string, reqLog *domain.RequestLog) (*unifiedDispatchCall, error) {
	model, _ := params["model"].(string)
	cfg := liveConfig()
	combo, isCombo, err := prepareUnifiedParams(params, model, sessionID, protocol, cfg.DispatchMode)
	if err != nil {
		return nil, err
	}
	if reqLog != nil {
		reqLog.ToolNames = clampParamsToolNames(params)
	}
	mode := cfg.DispatchMode
	var (
		plan        routing.CandidatePlan
		diagnostics routing.PlanDiagnostics
	)
	if mode != config.DispatchModeLegacy {
		plan, diagnostics, err = unifiedPlan(cfg, model, params, combo, isCombo)
		if err != nil {
			if mode == config.DispatchModeEnforce {
				return nil, err
			}
			log.Printf("  dispatch shadow plan unavailable: %v", err)
			plan, diagnostics = routing.CandidatePlan{}, routing.PlanDiagnostics{RequestedModel: model}
		}
	}
	runtime := dispatch.DefaultRuntime()
	requestID := ""
	if reqLog != nil {
		requestID = reqLog.ID
	}
	if requestID == "" {
		generated, idErr := dispatch.GenerateID()
		if idErr != nil {
			return nil, idErr
		}
		requestID = "req_" + generated
		if reqLog != nil {
			reqLog.ID = requestID
		}
	}
	request := dispatch.Request{
		RequestID: requestID, RequestedModel: model, Params: params, Stream: stream,
		SessionID: sessionID,
	}
	call := &unifiedDispatchCall{
		runtime: runtime, request: request, mode: mode,
		diagnostics: diagnostics, plan: plan, params: params, reqLog: reqLog,
	}
	call.applyPlanMetadata(combo, isCombo)
	for truncateAttempt := 0; ; truncateAttempt++ {
		switch call.mode {
		case config.DispatchModeEnforce:
			attempts := routing.DispatchAttempts(plan)
			if len(attempts) == 0 {
				return nil, &routing.FreeModelUnavailableError{Message: fmt.Sprintf("model %q has no eligible provider candidates", model)}
			}
			call.execution, call.result, err = runtime.Executor.Execute(ctx, request, attempts)
		default:
			call.result, err = runtime.Legacy.ExecuteLegacy(ctx, request)
			if call.mode == config.DispatchModeShadow && err == nil {
				shadowCompare(diagnostics, call.result)
			}
		}
		if err == nil {
			call.apply(call.result)
			return call, nil
		}
		if truncateAttempt > 0 || !routing.IsContextLengthError(err) {
			call.apply(call.result)
			return nil, err
		}
		out := compaction.FallbackTruncate(params, compaction.ModelForCompaction(model))
		if !out.Changed {
			return nil, err
		}
		log.Printf("  %s %s", protocol, out.Note)
	}
}

func (call *unifiedDispatchCall) Retry(ctx context.Context, reason string) (*http.Response, *domain.Account, error) {
	var (
		result dispatch.Result
		err    error
	)
	if call.mode == config.DispatchModeEnforce && call.execution != nil {
		result, err = call.execution.Next(ctx, reason)
	} else {
		result, err = call.runtime.Legacy.ExecuteLegacy(ctx, call.request)
	}
	if err != nil {
		call.applyObservability(result)
		return nil, nil, err
	}
	call.apply(result)
	return result.Response, result.Account, nil
}

func (call *unifiedDispatchCall) MarkCommitted() {
	if call != nil && call.execution != nil {
		call.execution.MarkCommitted()
	}
}
