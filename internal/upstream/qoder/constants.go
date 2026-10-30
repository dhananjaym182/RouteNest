// Package qoder implements Qoder's COSY-signed inference protocol.
//
// Qoder's chat API is not OpenAI-compatible. Requests are signed with a hybrid
// RSA-1024 + AES-128-CBC + MD5 scheme (COSY), the JSON body is run through a
// WAF-bypass encoder, and every model needs the exact `model_config` block
// published by the live model catalog (a wrong block silently downgrades the
// model upstream). This package hides those details behind an OpenAI-shaped
// surface: Chat returns OpenAI SSE / chat.completion bodies, Models returns the
// live model keys.
package qoder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// COSY client fingerprint constants. These are not arbitrary: the upstream
// signature check compares the Cosy-* header values against the ones used at
// signing time, so they must stay in sync with the qodercli client.
const (
	ideVersion   = "1.0.0"
	clientType   = "5"
	dataPolicy   = "disagree"
	loginVersion = "v2"
	machineOS    = "x86_64_windows"
	machineType  = "5"
)

// Inference endpoints (all under /algo, all COSY-signed).
const (
	modelListPath = "/algo/api/v2/model/list"
	// The chat endpoint only streams upstream; `Encode=1` tells the server the
	// body went through the WAF-bypass encoder.
	chatPath = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
)

// qoderRSAPublicKey is the COSY wrapping key extracted from Qoder IDE v0.9.
// It matches the CLIProxyAPIPlus qoder-provider branch and live qodercli
// traffic. RSA-1024, PKCS#1 v1.5 padding.
const qoderRSAPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

// ModelListURL returns the live model catalog endpoint for baseURL. A user
// who pasted the full chat endpoint gets the same signed URLs as the bare
// host (mirrors normalizePresetBaseURL / the protocol lookup contract).
func ModelListURL(baseURL string) string {
	return sanitizeQoderBase(baseURL) + modelListPath
}

// ChatURL returns the streaming chat endpoint for baseURL.
func ChatURL(baseURL string) string {
	return sanitizeQoderBase(baseURL) + chatPath
}

// sanitizeQoderBase accepts either the bare inference host or the full chat
// endpoint URL and normalizes to the bare host.
func sanitizeQoderBase(baseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(u, "/chat/completions")
}

// marshalNoEscape encodes v like JSON.stringify: no HTML escaping of <, > and
// &, and no trailing newline. Go's json.Encoder appends "\n", so trim it.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// numberToInt coerces a JSON-decoded number (or numeric string) to int.
func numberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i, true
		}
	}
	return 0, false
}

// truncate caps s at n runes, appending "..." when it was cut. The reference
// uses JS slice (UTF-16 units); runes are used here so multi-byte text is not
// split in half.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
