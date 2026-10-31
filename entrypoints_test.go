package main

import (
	"os"
	"strings"
	"testing"
)

// Both entry points must call app.Configure explicitly. The composition is no
// longer applied by importing the package, so a blank import would silently
// leave the hooks unset.
func TestEntryPointsCallConfigureExplicitly(t *testing.T) {
	for _, file := range []string{"main.go", "desktop_main.go"} {
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
