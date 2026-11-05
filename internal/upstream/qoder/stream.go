package qoder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ============ Response handling ============

// cancelReadCloser cancels the request context when the caller closes the body,
// which unblocks any goroutine still reading the upstream response.
type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// wrapStream returns the OpenAI-SSE view of an upstream envelope stream. The
// goroutine owns the upstream body and always closes it (and releases the
// request context) when the stream ends or the caller closes the pipe.
func wrapStream(resp *http.Response, model string, cancel context.CancelFunc) *http.Response {
	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		defer resp.Body.Close()
		err := pumpEnvelopes(resp.Body, model, func(payload string) error {
			_, werr := io.WriteString(pw, "data: "+payload+"\n\n")
			return werr
		})
		// A pipe write error means the caller went away; either way the pipe
		// is finished. Close (not CloseWithError) so downstream sees EOF.
		_ = err
		_ = pw.Close()
	}()
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}},
		Body:          &cancelReadCloser{ReadCloser: pr, cancel: cancel},
		ContentLength: -1,
		Request:       resp.Request,
	}
}

// pumpEnvelopes reads Qoder's `{"statusCodeValue":N,"body":"<inner>"}` SSE
// envelope and emits each inner OpenAI payload (including "[DONE]"). A non-200
// statusCodeValue becomes a synthetic OpenAI error chunk followed by "[DONE]";
// nothing is emitted after the terminator.
func pumpEnvelopes(src io.Reader, model string, emit func(string) error) error {
	br := bufio.NewReaderSize(src, 64<<10)
	for {
		line, readErr := br.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data:") {
				data := strings.TrimSpace(trimmed[len("data:"):])
				switch data {
				case "":
					// keepalive / empty frame
				case "[DONE]":
					return emit("[DONE]")
				default:
					var env struct {
						StatusCodeValue any    `json:"statusCodeValue"`
						Body            string `json:"body"`
					}
					if err := json.Unmarshal([]byte(data), &env); err == nil {
						status := http.StatusOK
						if n, ok := numberToInt(env.StatusCodeValue); ok {
							status = n
						}
						if status != http.StatusOK {
							if werr := emit(errorChunk(model, status, env.Body)); werr != nil {
								return werr
							}
							return emit("[DONE]")
						}
						switch env.Body {
						case "":
							// Nothing to forward.
						case "[DONE]":
							return emit("[DONE]")
						default:
							// The inner payload must stay on one SSE data line;
							// embedded newlines would split the frame.
							inner := strings.NewReplacer("\r\n", "", "\n", "").Replace(env.Body)
							if werr := emit(inner); werr != nil {
								return werr
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				// Upstream closed without an explicit terminator; flush one so
				// downstream parsers finish cleanly.
				return emit("[DONE]")
			}
			if werr := emit(errorChunk(model, 0, readErr.Error())); werr != nil {
				return werr
			}
			return emit("[DONE]")
		}
	}
}

// errorChunk builds the synthetic OpenAI error chunk used for envelope failures
// and stream read errors.
func errorChunk(model string, status int, message string) string {
	if message == "" {
		message = fmt.Sprintf("upstream status %d", status)
	}
	text := fmt.Sprintf("\n[qoder stream error: %s]", truncate(message, 200))
	if status != 0 {
		text = fmt.Sprintf("\n[qoder error %d: %s]", status, truncate(message, 200))
	}
	chunk := map[string]any{
		"id":      fmt.Sprintf("qoder-error-%d", time.Now().UnixMilli()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"content": text},
			"finish_reason": "stop",
		}},
	}
	b, err := marshalNoEscape(chunk)
	if err != nil {
		return "{}"
	}
	return string(b)
}
