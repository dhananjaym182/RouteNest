package qoder

import (
	"fmt"
	"strings"
)

// normalizeMessages hoists every system message into one string (Qoder rejects
// role:"system" inside messages) and normalizes each remaining content block.
func normalizeMessages(raw any) ([]any, string) {
	list := anySlice(raw)
	if len(list) == 0 {
		return []any{}, ""
	}
	var systemParts []string
	out := make([]any, 0, len(list))
	for _, item := range list {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "system" {
			if text := extractText(msg["content"]); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		cloned := make(map[string]any, len(msg))
		for k, v := range msg {
			cloned[k] = v
		}
		cloned["content"] = normalizeContent(msg["content"])
		out = append(out, cloned)
	}
	return out, strings.Join(systemParts, "\n\n")
}

// normalizeContent flattens text-only content to a plain string. When images
// are present the content stays an array of OpenAI `image_url` parts; Claude
// `{type:"image", source:{...}}` blocks are converted to the same shape.
func normalizeContent(content any) any {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		return normalizeContentBlocks(c)
	default:
		return fmt.Sprint(content)
	}
}

func normalizeContentBlocks(content []any) any {
	blocks := make([]any, 0, len(content))
	var textParts []string
	hasImage := false
	for _, item := range content {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := part["type"].(string)
		if typ == "image_url" {
			if iu, ok := part["image_url"].(map[string]any); ok {
				if u, _ := iu["url"].(string); u != "" {
					blocks = append(blocks, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": u},
					})
					hasImage = true
					continue
				}
			}
		}
		if typ == "image" {
			if src, ok := part["source"].(map[string]any); ok {
				if url := imageSourceURL(src); url != "" {
					blocks = append(blocks, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": url},
					})
					hasImage = true
					continue
				}
			}
		}
		if txt, ok := part["text"].(string); ok && txt != "" {
			if hasImage || len(blocks) > 0 {
				// Keep ordering faithful once images are in play.
				blocks = append(blocks, map[string]any{"type": "text", "text": txt})
			} else {
				textParts = append(textParts, txt)
			}
		}
	}
	if !hasImage {
		return strings.Join(textParts, "\n")
	}
	if len(textParts) > 0 {
		// Text collected before the first image is prepended as one block.
		blocks = append([]any{map[string]any{"type": "text", "text": strings.Join(textParts, "\n")}}, blocks...)
	}
	return blocks
}

// imageSourceURL converts a Claude image source into an OpenAI-compatible URL.
func imageSourceURL(src map[string]any) string {
	if typ, _ := src["type"].(string); typ == "base64" {
		data, _ := src["data"].(string)
		if data == "" {
			return ""
		}
		mediaType, _ := src["media_type"].(string)
		if mediaType == "" {
			mediaType = "image/png"
		}
		return "data:" + mediaType + ";base64," + data
	}
	u, _ := src["url"].(string)
	return u
}

func extractText(content any) string {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, item := range c {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if txt, ok := part["text"].(string); ok {
				parts = append(parts, txt)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(content)
	}
}

func lastUserText(messages []any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			return c
		case []any:
			return extractText(c)
		}
	}
	return ""
}
