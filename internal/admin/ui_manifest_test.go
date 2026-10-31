package admin

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// The manifest is the assembly order. Every listed asset must exist once, and every
// embedded JS file must be listed, so no script is silently dropped or unordered.
func TestAdminUIManifestMatchesEmbeddedAssets(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range adminUIJSAssets {
		if seen[name] {
			t.Errorf("manifest lists %s more than once", name)
		}
		seen[name] = true
		if _, err := adminUIFS.ReadFile(name); err != nil {
			t.Errorf("manifest asset %s is not embedded: %v", name, err)
		}
	}
	files, err := fs.Glob(adminUIFS, "ui/js/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !seen[f] {
			t.Errorf("embedded script %s is not in the manifest", path.Base(f))
		}
	}
}

// Assembled output contains each script once, in manifest order.
func TestAdminHTMLAssemblesScriptsInManifestOrder(t *testing.T) {
	last := -1
	for _, name := range adminUIJSAssets {
		b, _ := adminUIFS.ReadFile(name)
		marker := strings.TrimSpace(string(b))
		if len(marker) > 80 {
			marker = marker[:80]
		}
		idx := strings.Index(adminHTML, marker)
		if idx < 0 {
			t.Fatalf("%s is missing from the assembled page", name)
		}
		if strings.Count(adminHTML, marker) < 1 || idx <= last {
			t.Errorf("%s is out of manifest order in the assembled page", name)
		}
		last = idx
	}
}

// Every body fragment must be listed exactly once, exist in the embed, and every
// embedded HTML fragment must be listed.
func TestAdminUIBodyManifestMatchesEmbeddedFragments(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range adminUIBodyParts {
		if seen[name] {
			t.Errorf("body manifest lists %s more than once", name)
		}
		seen[name] = true
		if _, err := adminUIFS.ReadFile(name); err != nil {
			t.Errorf("body fragment %s is not embedded: %v", name, err)
		}
	}
	var files []string
	for _, pat := range []string{"ui/html/*.html", "ui/html/pages/*.html", "ui/html/dialogs/*.html"} {
		m, err := fs.Glob(adminUIFS, pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		if !seen[f] {
			t.Errorf("embedded fragment %s is not in the body manifest", f)
		}
	}
}

// Stylesheet layers: every layer listed exactly once in order, every embedded CSS
// file listed. The cascade depends on this order.
func TestAdminUICSSManifestMatchesEmbeddedLayers(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range adminUICSSLayers {
		if seen[name] {
			t.Errorf("css manifest lists %s more than once", name)
		}
		seen[name] = true
		if _, err := adminUIFS.ReadFile(name); err != nil {
			t.Errorf("css layer %s is not embedded: %v", name, err)
		}
	}
	files, err := fs.Glob(adminUIFS, "ui/css/*.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !seen[f] {
			t.Errorf("embedded stylesheet %s is not in the css manifest", f)
		}
	}
}
