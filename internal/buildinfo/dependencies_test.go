package buildinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	// Keep the security dependencies in the module graph during Gate 0, before
	// the protocol implementations are enabled. These also compile their APIs.
	_ "github.com/coreos/go-oidc/v3/oidc"
	_ "github.com/crewjam/saml"
	_ "golang.org/x/oauth2"
)

func TestAdminSecurityDependencyVersions(t *testing.T) {
	exact := map[string]string{
		"github.com/andybalholm/brotli": "v1.2.8",
		"github.com/coreos/go-oidc/v3":  "v3.21.0",
		"github.com/crewjam/saml":       "v0.6.3",
		"github.com/go-jose/go-jose/v4": "v4.1.9",
		"github.com/labstack/echo/v4":   "v4.16.4",
		"github.com/klauspost/compress": "v1.20.3",
		"github.com/wailsapp/wails/v2":  "v2.17.1",
		"github.com/zalando/go-keyring": "v0.2.8",
		"golang.org/x/oauth2":           "v0.37.0",
	}
	args := []string{"list", "-mod=readonly", "-m", "-json", "routenest",
		"github.com/andybalholm/brotli", "github.com/coreos/go-oidc/v3",
		"github.com/crewjam/saml", "github.com/go-jose/go-jose/v4",
		"github.com/labstack/echo/v4", "github.com/klauspost/compress",
		"github.com/wailsapp/wails/v2", "github.com/zalando/go-keyring",
		"golang.org/x/oauth2", "github.com/russellhaering/goxmldsig"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect selected module versions: %v\n%s", err, output)
	}
	seen := make(map[string]bool)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var module struct {
			Path, Version, GoVersion string
			Replace                  *json.RawMessage
		}
		if err := decoder.Decode(&module); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode module version: %v", err)
		}
		seen[module.Path] = true
		if module.Replace != nil {
			t.Errorf("security module %s must not be replaced", module.Path)
		}
		if want, ok := exact[module.Path]; ok && module.Version != want {
			t.Errorf("%s = %s, want %s", module.Path, module.Version, want)
		}
		if module.Path == "routenest" && module.GoVersion != "1.26.11" {
			t.Errorf("Go baseline = %s, want 1.26.11", module.GoVersion)
		}
		if module.Path == "github.com/russellhaering/goxmldsig" &&
			(strings.Contains(module.Version, "-") ||
				(module.Version != "v1.6.1" && !versionNewer("v1.6.1", module.Version))) {
			t.Errorf("goxmldsig = %s, want stable v1.6.1 or newer", module.Version)
		}
	}
	for _, path := range args[4:] {
		if !seen[path] {
			t.Errorf("required security module %s is missing", path)
		}
	}
}
