package qoder

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ============ Request payload ============

type chatParameters struct {
	MaxTokens int `json:"max_tokens"`
}

type modelConfigRef struct {
	Key         string `json:"key"`
	IsReasoning bool   `json:"is_reasoning"`
}

type chatContextExtra struct {
	Context         []any          `json:"context"`
	ModelConfig     modelConfigRef `json:"modelConfig"`
	OriginalContent string         `json:"originalContent"`
}

type chatContext struct {
	ChatPrompt string           `json:"chatPrompt"`
	ImageURLs  any              `json:"imageUrls"`
	Extra      chatContextExtra `json:"extra"`
	Features   []any            `json:"features"`
	Text       string           `json:"text"`
}

type businessBlock struct {
	Product string `json:"product"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	BeginAt int64  `json:"begin_at"`
}

// chatPayload mirrors buildQoderRequestBody in the reference executor. The
// upstream only streams, so `stream` is always true even for non-stream
// callers (they get the stream collapsed locally).
type chatPayload struct {
	RequestID      string          `json:"request_id"`
	RequestSetID   string          `json:"request_set_id"`
	ChatRecordID   string          `json:"chat_record_id"`
	SessionID      string          `json:"session_id"`
	Stream         bool            `json:"stream"`
	ChatTask       string          `json:"chat_task"`
	IsReply        bool            `json:"is_reply"`
	IsRetry        bool            `json:"is_retry"`
	Source         int             `json:"source"`
	Version        string          `json:"version"`
	SessionType    string          `json:"session_type"`
	AgentID        string          `json:"agent_id"`
	TaskID         string          `json:"task_id"`
	CodeLanguage   string          `json:"code_language"`
	ChatPrompt     string          `json:"chat_prompt"`
	ImageURLs      any             `json:"image_urls"`
	AliyunUserType string          `json:"aliyun_user_type"`
	System         string          `json:"system"`
	Messages       []any           `json:"messages"`
	Tools          []any           `json:"tools"`
	Parameters     chatParameters  `json:"parameters"`
	ChatContext    chatContext     `json:"chat_context"`
	ModelConfig    json.RawMessage `json:"model_config"`
	Business       businessBlock   `json:"business"`
}

func buildChatPayload(creds Creds, params map[string]any, modelKey string, entry catalogEntry) (*chatPayload, error) {
	messages, systemText := normalizeMessages(params["messages"])
	tools := anySlice(params["tools"])
	if tools == nil {
		tools = []any{}
	}

	maxTokens := 32768
	if entry.maxOutputTokens > 0 {
		maxTokens = entry.maxOutputTokens
	}
	if n, ok := positiveInt(params["max_tokens"]); ok && n < maxTokens {
		maxTokens = n
	}
	if n, ok := positiveInt(params["max_completion_tokens"]); ok && n < maxTokens {
		maxTokens = n
	}

	lastUser := lastUserText(messages)
	recordID := stableChatRecordID(modelKey, messages, tools, maxTokens)
	requestID, err := newUUID()
	if err != nil {
		return nil, err
	}
	businessID, err := newUUID()
	if err != nil {
		return nil, err
	}

	return &chatPayload{
		RequestID:      requestID,
		RequestSetID:   recordID,
		ChatRecordID:   recordID,
		SessionID:      stableHash("qoder-session", creds.UserID, modelKey),
		Stream:         true,
		ChatTask:       "FREE_INPUT",
		IsReply:        true,
		IsRetry:        false,
		Source:         1,
		Version:        "3",
		SessionType:    "qodercli",
		AgentID:        "agent_common",
		TaskID:         "common",
		CodeLanguage:   "",
		ChatPrompt:     "",
		ImageURLs:      nil,
		AliyunUserType: "",
		System:         systemText,
		Messages:       messages,
		Tools:          tools,
		Parameters:     chatParameters{MaxTokens: maxTokens},
		ChatContext: chatContext{
			ChatPrompt: "",
			ImageURLs:  nil,
			Extra: chatContextExtra{
				Context:         []any{},
				ModelConfig:     modelConfigRef{Key: modelKey, IsReasoning: entry.isReasoning},
				OriginalContent: lastUser,
			},
			Features: []any{},
			Text:     lastUser,
		},
		ModelConfig: entry.raw,
		Business: businessBlock{
			Product: "cli",
			Version: "1.0.0",
			Type:    "agent",
			Stage:   "start",
			ID:      businessID,
			Name:    truncate(lastUser, 30),
			BeginAt: time.Now().UnixMilli(),
		},
	}, nil
}

// anySlice coerces the shapes a decoded JSON array can take into []any.
func anySlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []map[string]any:
		out := make([]any, len(s))
		for i, m := range s {
			out[i] = m
		}
		return out
	}
	return nil
}

func positiveInt(v any) (int, bool) {
	n, ok := numberToInt(v)
	return n, ok && n > 0
}

// stableHash mirrors the reference stableHash: sha256(prefix + "\0" + part...)
// truncated to 16 hex chars.
func stableHash(prefix string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(prefix))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// stableChatRecordID groups related requests under one chat record. It hashes
// the same inputs as the reference; map key ordering differs from JS object
// insertion order, but the value only needs to be stable within this client.
func stableChatRecordID(model string, messages, tools []any, maxTokens int) string {
	h := sha256.New()
	h.Write([]byte("qoder-record\x00"))
	h.Write([]byte(model))
	for _, item := range messages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "" {
			h.Write([]byte{0})
			h.Write([]byte(role))
		}
		switch c := msg["content"].(type) {
		case string:
			if c != "" {
				h.Write([]byte{0})
				h.Write([]byte(c))
			}
		case []any:
			// Include image refs so the same prompt with a different image
			// gets a distinct chat_record_id.
			h.Write([]byte{0})
			if b, err := marshalNoEscape(c); err == nil {
				h.Write(b)
			}
		}
	}
	if tools != nil {
		h.Write([]byte{0})
		if b, err := marshalNoEscape(tools); err == nil {
			h.Write(b)
		}
	}
	h.Write([]byte(fmt.Sprintf("\x00mt=%d", maxTokens)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// randHex returns n random bytes as hex, or "" if the entropy source fails.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
