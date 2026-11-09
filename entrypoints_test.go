package main

import (
	"os"
	"strings"
	"testing"
)

// Both startup paths must call app.Configure explicitly. Desktop setup defers
// composition until trusted first-run authorization has created the account
// store, so its call lives in the focused desktop runtime module.
func TestEntryPointsCallConfigureExplicitly(t *testing.T) {
	for _, file := range []string{"main.go", "desktop_runtime.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		if !strings.Contains(text, "app.Configure()") {
			t.Errorf("%s does not call app.Configure()", file)
		}
		if strings.Contains(text, `_ "routenest/internal/app"`) {
			t.Errorf("%s still applies the composition by blank import", file)
		}
	}
}
