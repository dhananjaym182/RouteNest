package admin

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Inline handlers (onclick="fn(...)") are resolved against the shared script scope
// at click time. Every function they call must be declared by an embedded script,
// or the control does nothing and fails silently.
var (
	inlineHandlerAttrRe = regexp.MustCompile(`\bon[a-z]+="([^"]*)"`)
	inlineCallRe        = regexp.MustCompile(`(?:^|[^.\w$])([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	declaredFuncRe      = regexp.MustCompile(`(?m)^(?:async )?function ([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	// Browser built-ins and expression keywords that may appear in handler text.
	inlineBuiltins = map[string]bool{
		"if": true, "return": true, "event": true, "this": true, "alert": true,
		"confirm": true, "prompt": true, "String": true, "Number": true, "Boolean": true,
		"parseInt": true, "parseFloat": true, "encodeURIComponent": true, "setTimeout": true,
		"console": true, "Math": true, "JSON": true, "Date": true, "Array": true,
		"Object": true, "Promise": true, "fetch": true, "navigator": true, "window": true,
		"document": true, "location": true, "history": true, "stopPropagation": true,
		"preventDefault": true, "blur": true, "focus": true, "click": true, "select": true,
	}
)

func TestInlineHandlerTargetsAreDeclared(t *testing.T) {
	declared := map[string]bool{}
	var scripts strings.Builder
	for _, name := range adminUIJSAssets {
		b, err := adminUIFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scripts.Write(b)
		for _, m := range declaredFuncRe.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = true
		}
	}
	// Markup-only attributes: the page source is the concatenated body fragments.
	body := adminHTML
	missing := map[string]bool{}
	for _, attr := range inlineHandlerAttrRe.FindAllStringSubmatch(body, -1) {
		for _, c := range inlineCallRe.FindAllStringSubmatch(attr[1], -1) {
			name := c[1]
			if declared[name] || inlineBuiltins[name] {
				continue
			}
			// Assigned or bound by the script (e.g. a const arrow function).
			if strings.Contains(scripts.String(), "const "+name+" ") || strings.Contains(scripts.String(), "let "+name+" ") {
				continue
			}
			missing[name] = true
		}
	}
	if len(missing) > 0 {
		var names []string
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Fatalf("inline handlers call functions no script declares: %v", names)
	}
}
