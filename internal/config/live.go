package config

import (
	"log"
	"sync"

	"routenest/internal/platform"
)

// Mu guards the live proxy configuration. Callers that read or write fields of
// the live value hold Mu for the whole operation.
var Mu sync.Mutex

// live is the process-wide proxy configuration. It is loaded from disk on first
// use, not at package init, so importing this package does no I/O.
var live *Proxy

func liveLocked() *Proxy {
	if live == nil {
		live = LoadLiveFromDisk()
	}
	return live
}

// Live returns the live configuration. The pointer is shared: read fields under
// Mu, or use Snapshot for a private copy.
func Live() *Proxy {
	Mu.Lock()
	defer Mu.Unlock()
	return liveLocked()
}

// Held returns the live configuration without taking Mu. The caller must already
// hold Mu.
func Held() *Proxy { return liveLocked() }

// Snapshot returns a deep copy of the live configuration, taken under Mu.
// Writers edit the copy and publish it with Publish, so they never mutate the
// configuration that requests are reading.
func Snapshot() *Proxy {
	Mu.Lock()
	defer Mu.Unlock()
	return liveLocked().Clone()
}

// Publish replaces the live configuration and persists it.
func Publish(c *Proxy) {
	Mu.Lock()
	defer Mu.Unlock()
	live = c
	SaveLocked()
}

// SaveLocked persists the live configuration. The caller must hold Mu.
func SaveLocked() {
	if err := SaveProxy(platform.ResolveDataPath(ProxyPath, nil), liveLocked()); err != nil {
		log.Printf("proxy config save failed: %v", err)
	}
}

// LoadLiveFromDisk loads the persisted configuration. A corrupt file is renamed
// to a backup and defaults are returned, so a bad file never overwrites silently.
func LoadLiveFromDisk() *Proxy {
	path := platform.ResolveDataPath(ProxyPath, nil)
	cfg, err := LoadProxy(path)
	if err != nil {
		platform.BackupCorruptFile(path, err)
	}
	return cfg
}
