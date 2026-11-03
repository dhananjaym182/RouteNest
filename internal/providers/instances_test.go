package providers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/providerreg"
	"routenest/internal/zen"
)

func installZenInstanceConfig(t *testing.T, enabled bool, baseURL string) {
	t.Helper()
	zen.ConfigMu.Lock()
	old := zen.Config
	zen.Config = &config.Zen{Enabled: enabled, BaseURL: baseURL, Key: "public"}
	zen.ConfigMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = old
		zen.ConfigMu.Unlock()
	})
}

func instanceByID(t *testing.T, instances []domain.ProviderInstance, id domain.ProviderInstanceID) domain.ProviderInstance {
	t.Helper()
	for _, instance := range instances {
		if instance.ID == id {
			return instance
		}
	}
	t.Fatalf("provider instance %q not found in %+v", id, instances)
	return domain.ProviderInstance{}
}

func TestSnapshotInstancesIncludesBuiltInsAndCustomProviders(t *testing.T) {
	installZenInstanceConfig(t, false, "https://zen.custom.test/v1")
	installProviders(t,
		&CustomProvider{
			ID: "prov_github", Name: "GitHub", BaseURL: "https://api.githubcopilot.com",
			APIKey: "secret-api-key", ProxyURL: "http://user:pass@proxy.test:8080",
			Enabled: true, Priority: 3, Weight: 2, TimeoutSec: 45, CreatedAt: time.Now(),
		},
		&CustomProvider{ID: "prov_custom", Name: "Custom", BaseURL: "https://urluser:urlpass@custom.test/v1?api_key=query-secret", Enabled: true},
	)

	instances, err := SnapshotInstances()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 4 {
		t.Fatalf("instances = %d, want two built-ins plus two custom", len(instances))
	}

	cline := instanceByID(t, instances, "cline")
	if !cline.RuntimeBuiltin || cline.Deletable || !cline.Enabled || cline.DefinitionID != "cline" || cline.AdapterID != "cline" {
		t.Fatalf("cline instance = %+v", cline)
	}
	openCode := instanceByID(t, instances, "opencode")
	if !openCode.RuntimeBuiltin || openCode.Deletable || openCode.Enabled || openCode.BaseURL != "https://zen.custom.test/v1" {
		t.Fatalf("opencode instance = %+v", openCode)
	}

	github := instanceByID(t, instances, "prov_github")
	if github.DefinitionID != "github-copilot" || github.AdapterID != "openaicompat" || !github.Deletable {
		t.Fatalf("github projection = %+v", github)
	}
	if github.Priority != 3 || github.Weight != 2 || github.TimeoutSec != 45 {
		t.Fatalf("github routing projection = %+v", github)
	}

	custom := instanceByID(t, instances, "prov_custom")
	if custom.DefinitionID != "" || custom.AdapterID != "openaicompat" {
		t.Fatalf("ad-hoc projection = %+v", custom)
	}

	raw, err := json.Marshal(instances)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-api-key", "user:pass", "urluser", "urlpass", "query-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("instance snapshot leaked %q: %s", secret, raw)
		}
	}
}

func TestSnapshotInstancesReturnsDeepCopies(t *testing.T) {
	installZenInstanceConfig(t, true, zen.APIBase)
	installProviders(t)

	first, err := SnapshotInstances()
	if err != nil {
		t.Fatal(err)
	}
	cline := instanceByID(t, first, "cline")
	cline.Capabilities[providerreg.CapabilityReactiveQuota] = domain.CapabilityUnsupported

	second, err := SnapshotInstances()
	if err != nil {
		t.Fatal(err)
	}
	if got := instanceByID(t, second, "cline").Capabilities[providerreg.CapabilityReactiveQuota]; got != domain.CapabilitySupported {
		t.Fatalf("capability snapshot aliased: %q", got)
	}
}

func TestSnapshotInstancesRejectsBuiltInIDCollision(t *testing.T) {
	installZenInstanceConfig(t, true, zen.APIBase)
	installProviders(t, &CustomProvider{ID: "cline", Name: "Collision", BaseURL: "https://custom.test/v1", Enabled: true})
	if _, err := SnapshotInstances(); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision error = %v", err)
	}
}
