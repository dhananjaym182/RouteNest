package adminauth

import (
	"fmt"
	"path/filepath"
)

// OpenForRecovery requires an existing authority in a private, current-owner
// data directory. Unlike normal startup, recovery does not create or tighten
// directories or missing authority files to make them pass its checks.
func OpenForRecovery(dataDir string) (*Store, error) {
	if err := checkSecurePath(dataDir, true); err != nil {
		return nil, fmt.Errorf("verify recovery data directory: %w", err)
	}
	dir := filepath.Join(dataDir, "admin-auth")
	if err := checkSecurePath(dir, true); err != nil {
		return nil, fmt.Errorf("verify recovery authority directory: %w", err)
	}
	for _, name := range []string{SnapshotName, JournalName, LockName} {
		if err := checkSecurePath(filepath.Join(dir, name), false); err != nil {
			return nil, fmt.Errorf("verify existing recovery authority: %w", err)
		}
	}
	// The same lifetime lock held by the server enforces downtime, including
	// servers listening on a different port or owned by this process.
	return Open(dir)
}
