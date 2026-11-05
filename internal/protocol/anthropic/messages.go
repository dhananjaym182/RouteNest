package anthropic

import "encoding/json"

// Anthropic Messages request body and its conversion to OpenAI chat form.

type MessagesMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type MessagesRequest struct {
	Model             string            `json:"model"`
	MaxTokens         int               `json:"max_tokens"`
	Messages          []MessagesMessage `json:"messages"`
	System            json.RawMessage   `json:"system,omitempty"`
	Stream            bool              `json:"stream,omitempty"`
	Temperature       float64           `json:"temperature,omitempty"`
	TopP              float64           `json:"top_p,omitempty"`
	TopK              int               `json:"top_k,omitempty"`
	Stop              json.RawMessage   `json:"stop_sequences,omitempty"`
	Thinking          json.RawMessage   `json:"thinking,omitempty"`
	Tools             json.RawMessage   `json:"tools,omitempty"`
	ToolChoice        json.RawMessage   `json:"tool_choice,omitempty"`
	Metadata          json.RawMessage   `json:"metadata,omitempty"`
	ContextManagement json.RawMessage   `json:"context_management,omitempty"`
	Extra             map[string]any    `json:"-"`
}

// MessagesToOpenAI converts an Anthropic Messages request to OpenAI form.
func MessagesToOpenAI(req MessagesRequest, opts Options) map[string]any {
	msgs := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, Message{Role: m.Role, Content: m.Content})
	}
	return ToOpenAI(Request{
		Model: req.Model, MaxTokens: req.MaxTokens, Stream: req.Stream,
		Temperature: req.Temperature, TopP: req.TopP, System: req.System,
		Messages: msgs, Tools: req.Tools, ToolChoice: req.ToolChoice, Thinking: req.Thinking,
	}, opts)
}
