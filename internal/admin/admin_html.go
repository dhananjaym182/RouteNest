package admin

// ============================================================================
// 管理面板单页装配：ui/ 静态资源在 init 时拼成完整 HTML（head.html +
// <style>styles.css + body.html + 按文件名序拼接的 js/NN_*.js + 收尾标签），
// /admin/ 原样输出且 no-cache。JS 文件共享全局作用域，文件名前缀即拼接顺序；
// 每个资产文件保持 ≤ 1000 行，超限时按 // ==== 段落横幅继续拆分。
// ============================================================================

import (
	"bytes"
	"embed"
	"io/fs"
	"sort"
)

//go:embed ui/head.html ui/styles.css ui/body.html ui/js/*.js
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
	write("ui/styles.css")
	buf.WriteString("</style>\n")
	write("ui/body.html")
	buf.WriteString("<script>\n")
	names, err := fs.Glob(adminUIFS, "ui/js/*.js")
	if err != nil {
		panic("admin ui js glob: " + err.Error())
	}
	sort.Strings(names)
	for _, n := range names {
		write(n)
	}
	buf.WriteString("</script>\n</body>\n</html>")
	return buf.String()
}
