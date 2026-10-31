package routing

import (
	"errors"
	"net/http"
	"strings"

	"routenest/internal/egress"
)

type ClineAccountUnavailableError struct {
	err error
}

func (e *ClineAccountUnavailableError) Error() string {
	return e.err.Error()
}

func (e *ClineAccountUnavailableError) Unwrap() error {
	return e.err
}

type FreeModelUnavailableError struct {
	Message string
}

func (e *FreeModelUnavailableError) Error() string {
	return e.Message
}

// IsDeterministicRequestError 判断 err 是否为「换账号/换模型重试也无法自愈」的
// 上游确定性 400：invalid-request 语义（invalid_request_error 类型、invalid_request
// 伪码、网关字段校验 "Invalid input"，如工具可选字段被序列化为 null）。
// 上下文超限 400 显式排除（IsContextLengthError）：那类错误有压缩+重试的自愈路径，
// 必须保留现有的 truncate/retry 行为。限流 429/5xx 不在此列。
func IsDeterministicRequestError(err error) bool {
	if err == nil || IsContextLengthError(err) {
		return false
	}
	var apiErr *egress.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "invalid_request_error") ||
		strings.Contains(msg, "invalid_request") ||
		strings.Contains(msg, "invalid input")
}

// IsContextLengthError reports whether err is an upstream 400 indicating the
// request exceeded the model's maximum context window. Compaction and the
// combo and proxy retry paths use it to decide on truncation and retry.
func IsContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context length") ||
		strings.Contains(msg, "maximum context") ||
		strings.Contains(msg, "context window") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "token limit")
}
