package admin

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// knownDuplicateGlobals lists top-level JS function names that are already
// declared more than once in the shared admin script scope. Each entry must be
// removed by the owning UI refactor (plan Phase 9); no new entries are allowed.
var knownDuplicateGlobals = map[string]bool{}

var topLevelFuncRe = regexp.MustCompile(`(?m)^(?:async )?function ([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

// TestAdminJSTopLevelFunctionsUnique guards the shared global scope of the
// embedded admin UI. Files are read in the same sorted order buildAdminHTML uses.
func TestAdminJSTopLevelFunctionsUnique(t *testing.T) {
	names, err := adminUIJSNames()
	if err != nil {
		t.Fatalf("list admin js: %v", err)
	}
	counts := map[string][]string{}
	for _, n := range names {
		b, err := adminUIFS.ReadFile(n)
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		for _, m := range topLevelFuncRe.FindAllStringSubmatch(string(b), -1) {
			counts[m[1]] = append(counts[m[1]], n)
		}
	}

	var dups []string
	for fn, files := range counts {
		if len(files) > 1 && !knownDuplicateGlobals[fn] {
			dups = append(dups, fn+" in "+strings.Join(files, ", "))
		}
	}
	sort.Strings(dups)
	for _, d := range dups {
		t.Errorf("duplicate top-level admin JS function: %s", d)
	}

	for fn := range knownDuplicateGlobals {
		if len(counts[fn]) < 2 {
			t.Errorf("knownDuplicateGlobals entry %q is no longer duplicated; remove it from the allowlist", fn)
		}
	}
}
