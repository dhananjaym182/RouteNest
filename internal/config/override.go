package config

import (
	"log"
	"os"
	"strings"
	"sync/atomic"
)

// OverrideContent returns the trimmed contents of override.md in the working
// directory, or "" when the file is missing or empty. It is used as the system
// prompt override for Anthropic and chat requests.
func OverrideContent() string {
	data, err := os.ReadFile("override.md")
	if err != nil {
		// 每个请求都会调用本函数；“文件不存在”只在首次打印一次，避免刷屏
		if overrideMissLogged.CompareAndSwap(false, true) {
			log.Printf("  override.md not found: %v", err)
		}
		return ""
	}
	overrideMissLogged.Store(false) // 文件出现了，重新允许日后删除时再提示一次
	content := strings.TrimSpace(string(data))
	if content != "" {
		log.Printf("  using override.md as system prompt (%d bytes)", len(content))
	} else {
		log.Printf("  override.md is empty")
	}
	return content
}

// overrideMissLogged override.md 缺失日志的去重标记（详见 loadOverrideContent）。
var overrideMissLogged atomic.Bool
