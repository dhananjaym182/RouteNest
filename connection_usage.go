package main

// Connection-scoped usage attribution choke point. It stays in package main
// because the account path calls routing.RecordTokenUsage (which needs the Cline
// account pool and model catalog); the connection store itself lives in
// internal/connections.

import (
	"cline-go-proxy/internal/routing"
	"net/http"

	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/proxycore"
)

// recordUsage is the single attribution choke point for every finalize site.
// Cline-path responses carry an account; provider-path responses carry a
// tagged connection (acc == nil); zen traffic has neither and stays log-only,
// exactly as before wave-4.
func recordUsage(acc *Account, upstream *http.Response, reqLog *RequestLog, model string, usage proxycore.TokenUsage) {
	if acc != nil {
		routing.RecordTokenUsage(acc, model, usage)
		return
	}
	conn := connections.ResponseConnection(upstream)
	if conn == nil {
		return
	}
	if reqLog != nil {
		reqLog.ConnectionID = conn.ID
		reqLog.ConnectionEmail = conn.Email
	}
	if usage.Valid {
		connections.RecordConnectionUsage(conn.ID, model, usage)
	}
}
