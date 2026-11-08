package config

import (
	"log"
	"sync"
	"sync/atomic"

	"routenest/internal/platform"
)

// Mu guards the live proxy configuration. Callers that write fields of the live
// value hold Mu for the whole operation; readers use the published value.
var Mu sync.Mutex

// live holds the published configuration. Publication is atomic, and the value
// behind the pointer is never edited: writers build a new value and call
// Publish, which stores a clone. A reader therefore keeps a coherent value for
// the whole operation even while an admin update is applied.
var live atomic.Pointer[Proxy]

// heldLocked returns the published configuration, loading it on first use. The
// caller must hold Mu (or otherwise guarantee exclusive access).
func heldLocked() *Proxy {
	if p := live.Load(); p != nil {
		return p
	}
	p := LoadLiveFromDisk()
	live.Store(p)
	return p
}

// current returns the published configuration, taking Mu only when the value has
// not been loaded yet.
func current() *Proxy {
	if p := live.Load(); p != nil {
		return p
	}
	Mu.Lock()
	defer Mu.Unlock()
	return heldLocked()
}

// Live returns the published configuration. The value is immutable after
// publication, so callers may read fields without holding Mu. Use Snapshot when
// the caller needs to modify a copy.
func Live() *Proxy {
	return current()
}

// Held returns the published configuration without taking Mu. The caller must
// already hold Mu.
func Held() *Proxy { return heldLocked() }

// Snapshot returns a deep copy of the live configuration, taken under Mu.
// Writers edit the copy and publish it with Publish, so they never mutate the
// configuration that requests are reading.
func Snapshot() *Proxy {
	return current().Clone()
}

// Publish stores a clone of c as the live configuration and persists it. Stable
// provider namespaces are retained across stale snapshots, and configurations
// carrying those namespaces are persisted before publication. Rejected updates
// are logged; callers cannot observe a partially published configuration.
func Publish(c *Proxy) {
	if c == nil {
		return
	}
	Mu.Lock()
	defer Mu.Unlock()
	next := c.Clone()
	next.RuntimeState.Normalize()
	for prefix, providerID := range heldLocked().ProviderModelPrefixes {
		if incoming, present := next.ProviderModelPrefixes[prefix]; present && incoming != providerID {
			log.Printf("proxy config publish rejected: provider model prefix %q is reserved for provider %q", prefix, providerID)
			return
		}
		if next.ProviderModelPrefixes == nil {
			next.ProviderModelPrefixes = make(map[string]string)
		}
		next.ProviderModelPrefixes[prefix] = providerID
	}
	if err := next.ValidateProviderModelPrefixes(); err != nil {
		log.Printf("proxy config publish rejected: %v", err)
		return
	}
	if len(next.ProviderModelPrefixes) > 0 {
		if err := SaveProxy(platform.ResolveDataPath(ProxyPath, nil), next); err != nil {
			log.Printf("proxy config save failed: %v", err)
			return
		}
		live.Store(next)
		return
	}
	live.Store(next)
	SaveLocked()
}

// SaveLocked persists the live configuration. The caller must hold Mu.
func SaveLocked() {
	if err := SaveProxy(platform.ResolveDataPath(ProxyPath, nil), heldLocked()); err != nil {
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
