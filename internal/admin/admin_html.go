package admin

// ============================================================================
// 管理面板单页装配：ui/ 静态资源按 ui_manifest.go 的显式清单拼成完整 HTML：
// head.html、<style> 的 CSS 层（adminUICSSLayers）、<body> 的 HTML 片段
// （adminUIBodyParts）、<script> 的 JS 文件（adminUIJSAssets），再加收尾标签。
// /admin/ 原样输出且 no-cache。JS 文件共享全局作用域；每个行为资产文件保持
// ≤ 1000 行，超限时按 // ==== 段落横幅拆分。i18n 字典是纯数据，单独放在
// 01_i18n.js，不计入代码行数上限（其内容由 tools/i18n_*.js 校验）。
// ============================================================================

import (
	"bytes"
	"embed"
)

//go:embed ui/head.html ui/css/*.css ui/html/*.html ui/html/pages/*.html ui/html/dialogs/*.html ui/js/*.js ui/assets/providers/* ui/assets/*.svg ui/assets/*.png ui/assets/*.ico
var adminUIFS embed.FS

var adminHTML string

func init() { adminHTML = buildAdminHTML() }

func buildAdminHTML() string {
	var buf bytes.Buffer
	write := func(name string) {
		b, err := adminUIFS.ReadFile(name)
		if err != nil {
			panic("admin ui asset unreadable: " + name)
		}
		buf.Write(b)
	}
	write("ui/head.html")
	buf.WriteString("<style>\n")
	for _, layer := range adminUICSSLayers {
		write(layer)
	}
	buf.WriteString("</style>\n")
	for _, part := range adminUIBodyParts {
		write(part)
	}
	buf.WriteString("<script>\n")
	names, err := adminUIJSNames()
	if err != nil {
		panic("admin ui js glob: " + err.Error())
	}
	for _, n := range names {
		write(n)
	}
	buf.WriteString("</script>\n</body>\n</html>")
	return buf.String()
}

// adminUIJSNames returns the admin JS asset paths in assembly order: the manifest.
func adminUIJSNames() ([]string, error) {
	out := make([]string, len(adminUIJSAssets))
	copy(out, adminUIJSAssets)
	return out, nil
}
