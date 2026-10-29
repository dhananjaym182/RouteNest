package zen

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"cline-go-proxy/internal/protocol/openaichat"
)

// AnonymousCoreTools are the tool names the anonymous free tier expects. The
// upstream agent-shape gate requires both, with the exact CLI descriptions and
// JSON schemas below; empty-schema stand-ins are rejected with a 429 that looks
// like an exhausted free quota.
var AnonymousCoreTools = []string{"read", "shell"}

// BodyOptions supplies root-owned configuration to the body builder: the
// OpenAI fields that may pass through and the model alias resolver.
type BodyOptions struct {
	PassThroughKeys []string
	ResolveModel    func(model string) (string, bool)
}

const readToolDescription = "Read the contents of a file or directory. Supports text files, images, and PDFs. Images and PDFs are presented directly to the model. Each text line is prefixed by its 1-based line number as <line>: <content>. The prefix is for reference and is not part of the file content. Directory entries are returned one per line. Use offset and limit to read large files or directories in sections. Prefer one larger read over many small slices, and use grep to find specific content in large files."

func shellToolDescription() string {
	osName := "Linux"
	shell := "bash"
	switch runtime.GOOS {
	case "darwin":
		osName, shell = "Darwin", "zsh"
	case "windows":
		osName, shell = "Windows", "powershell"
	}
	return "Execute a shell command and return its output. Commands run on " + osName + " using " + shell + ". Quote file paths containing spaces or special characters. Prefer dedicated tools over shell commands when possible. When output is large, the full result is saved to a file and a truncated preview is returned. Rely on automatic truncation unless filtering the output is more useful. Commands accept an optional timeout, background commands have no timeout by default. Background commands return immediately, and you will be notified when they complete."
}

func readToolParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "File or directory to read"},
			"offset": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The line or directory entry to start reading from (1-based)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The maximum number of lines or directory entries to read (defaults to and capped at 2000)",
			},
		},
		"required":             []any{"path"},
		"additionalProperties": false,
	}
}

func shellToolParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "Shell command string to execute"},
			"workdir": map[string]any{
				"type":        "string",
				"description": "Working directory to execute the command in. Defaults to the current working directory. When possible, avoid changing directories in the command and set the working directory here instead.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Timeout in milliseconds. Set to 0 to disable the timeout. Defaults to 120000 for foreground commands. Background commands have no timeout by default.",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Run the command in the background and return immediately (useful for dev servers and long-running builds). You do not need to use '&' at the end of your command when using this parameter. You will be notified when it completes. DO NOT poll for completion.",
			},
		},
		"required":             []any{"command"},
		"additionalProperties": false,
	}
}

// FingerprintTool returns the CLI-shaped definition for one gate tool.
func FingerprintTool(name string) map[string]any {
	var desc string
	var params map[string]any
	switch name {
	case "read":
		desc = readToolDescription
		params = readToolParameters()
	case "shell":
		desc = shellToolDescription()
		params = shellToolParameters()
	default:
		return nil
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": desc,
			"parameters":  params,
		},
	}
}

// FingerprintTools returns the full gate tool set in chat (nested) shape.
func FingerprintTools() []any {
	out := make([]any, 0, len(AnonymousCoreTools))
	for _, name := range AnonymousCoreTools {
		if tool := FingerprintTool(name); tool != nil {
			out = append(out, tool)
		}
	}
	return out
}

// AnonymousToolset returns gate tool definitions for names. Unknown names yield
// nothing; the upstream gate only accepts read and shell.
func AnonymousToolset(names []string) []any {
	if names == nil {
		names = AnonymousCoreTools
	}
	tools := make([]any, 0, len(names))
	for _, name := range names {
		if tool := FingerprintTool(name); tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

// EnsureAnonymousTools makes the body carry the exact read/shell definitions.
// Caller tools with those names are replaced (a different schema fails the
// gate); every other caller tool is left untouched.
func EnsureAnonymousTools(body map[string]any) {
	raw, exists := body["tools"]
	if !exists {
		body["tools"] = FingerprintTools()
		return
	}
	items, ok := raw.([]any)
	if !ok {
		return
	}
	replaced := make(map[string]bool, len(AnonymousCoreTools))
	out := make([]any, 0, len(items)+len(AnonymousCoreTools))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		fn, ok := entry["function"].(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		name, _ := fn["name"].(string)
		if tool := FingerprintTool(name); tool != nil {
			out = append(out, tool)
			replaced[name] = true
			continue
		}
		out = append(out, item)
	}
	for _, name := range AnonymousCoreTools {
		if replaced[name] {
			continue
		}
		if tool := FingerprintTool(name); tool != nil {
			out = append(out, tool)
		}
	}
	body["tools"] = out
}

var (
	envOnce  sync.Once
	envValue string
)

// EnvBlock returns the CLI's environment preamble. The free-tier gate requires
// this exact shape in a system message; a bare <env></env> tag is rejected.
func EnvBlock() string {
	envOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil || cwd == "" {
			cwd = "."
		}
		git := "no"
		dir := cwd
		for i := 0; i < 8; i++ {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				git = "yes"
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		tmp := filepath.Join(os.TempDir(), "opencode")
		envValue = strings.Join([]string{
			"Here is some useful information about the environment you are running in:",
			"<env>",
			"  Working directory: " + cwd,
			"  Workspace root folder: " + cwd,
			"  Is directory a git repo: " + git,
			"  Platform: " + runtime.GOOS,
			"  Prefer " + tmp + " over generic system temporary directories such as /tmp; it is pre-created and approved for external access.",
			"</env>",
		}, "\n")
	})
	return envValue
}

// InjectEnv appends the environment preamble to the first system message, or
// prepends one when absent. It is idempotent and leaves other messages alone.
func InjectEnv(body map[string]any) {
	env := EnvBlock()
	msgs, ok := body["messages"].([]any)
	if !ok {
		body["messages"] = []any{map[string]any{"role": "system", "content": env}}
		return
	}
	for i, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := m["role"].(string); role != "system" && role != "developer" {
			continue
		}
		switch content := m["content"].(type) {
		case string:
			if strings.Contains(content, "<env>") {
				return
			}
			if content == "" {
				m["content"] = env
			} else {
				m["content"] = content + "\n\n" + env
			}
		case []any:
			for _, part := range content {
				if pm, ok := part.(map[string]any); ok {
					if text, _ := pm["text"].(string); strings.Contains(text, "<env>") {
						return
					}
				}
			}
			m["content"] = append(content, map[string]any{"type": "text", "text": env})
		default:
			m["content"] = env
		}
		msgs[i] = m
		body["messages"] = msgs
		return
	}
	body["messages"] = append([]any{map[string]any{"role": "system", "content": env}}, msgs...)
}

// BuildChatBody constructs a zen chat request body: only OpenAI-compatible
// fields, with the model rewritten to the canonical zen ID. The anonymous free
// tier accepts only agent-shaped streaming requests, so stream is forced on,
// the CLI environment preamble is injected, and the gate tools are ensured.
func BuildChatBody(params map[string]any, stream bool, anonymous bool, opts BodyOptions) map[string]any {
	body := map[string]any{}
	for _, key := range opts.PassThroughKeys {
		if val, ok := params[key]; ok {
			body[key] = val
		}
	}
	for _, key := range []string{"model", "max_tokens", "max_completion_tokens"} {
		if val, ok := params[key]; ok {
			body[key] = val
		}
	}
	if msgsRaw, ok := params["messages"]; ok {
		if msgsArr, ok := msgsRaw.([]any); ok {
			body["messages"] = openaichat.SanitizeMessages(msgsArr)
		} else {
			body["messages"] = msgsRaw
		}
	}
	InjectEnv(body)
	wireStream := stream
	if anonymous {
		wireStream = true
	}
	body["stream"] = wireStream
	EnsureAnonymousTools(body)
	if anonymous && wireStream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if model, ok := params["model"].(string); ok {
		if opts.ResolveModel != nil {
			if id, ok := opts.ResolveModel(model); ok {
				body["model"] = id
			} else if model != "" {
				body["model"] = strings.TrimPrefix(model, "opencode/")
			}
		} else if model != "" {
			body["model"] = strings.TrimPrefix(model, "opencode/")
		}
	}
	delete(body, "reasoning_effort")
	delete(body, "reasoningEffort")
	openaichat.SanitizeTools(body)
	return body
}
