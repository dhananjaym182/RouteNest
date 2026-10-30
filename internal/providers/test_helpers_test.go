package providers

import (
	"path/filepath"
	"testing"
	"time"
)

func installProviders(t *testing.T, provs ...*CustomProvider) {
	t.Helper()
	Mu.Lock()
	oldPath, oldCooldownsPath := FilePath, CooldownsPath
	oldProviders, oldLoaded, oldCooldowns := CustomProviders, Loaded, Cooldowns
	FilePath = filepath.Join(t.TempDir(), ".routenest-providers.json")
	CooldownsPath = filepath.Join(t.TempDir(), ".routenest-providers-cooldowns.json")
	CustomProviders = append([]*CustomProvider(nil), provs...)
	Loaded = true
	Cooldowns = map[string]time.Time{}
	Mu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		FilePath, CooldownsPath = oldPath, oldCooldownsPath
		CustomProviders, Loaded, Cooldowns = oldProviders, oldLoaded, oldCooldowns
		Mu.Unlock()
	})
}
