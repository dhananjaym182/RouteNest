package platform

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// DataDirEnv fixes all data files to one directory when set (highest priority).
// Docker deployments rely on it to keep data on a mounted volume.
const DataDirEnv = "ROUTENEST_DATA_DIR"

// LegacyDataDirEnv is the pre-rename data-directory variable. It is still
// honored when DataDirEnv is unset so existing deployments keep working.
const LegacyDataDirEnv = "CLINE2API_DATA_DIR"

const AccountPoolWitnessName = ".routenest-accounts.json.initialized"

var currentCompanionFiles = []string{AccountPoolWitnessName}

// legacyDataFiles lists every LEGACY (pre-rename) data file name considered
// when migrating an old data directory. Keep this list in sync with the files
// the application persists under names that predate the RouteNest rename.
var legacyDataFiles = []string{
	".cline-accounts.json",
	".cline-config.json",
	".cline-connections.json",
	".cline-providers.json",
	".cline-providers-cooldowns.json",
	".cline-providers-sync.json",
	".cline-request-logs.json",
	".cline-zen.json",
	".cline-proxy.json",
	".cline-credentials.json",
}

// currentDataFileName maps a legacy data file name to its RouteNest
// ("routenest") equivalent. Same suffix, new ".routenest-" prefix. If the
// given name is not a legacy name it is returned unchanged.
func CurrentDataFileName(name string) string {
	return strings.Replace(name, ".cline-", ".routenest-", 1)
}

// LegacyDataFileName is the inverse of CurrentDataFileName: it maps a current
// ".routenest-" data file name back to the legacy ".cline-" spelling. The
// second return value reports whether a legacy counterpart exists.
func LegacyDataFileName(name string) (string, bool) {
	legacy := strings.Replace(name, ".routenest-", ".cline-", 1)
	return legacy, legacy != name
}

// CurrentDataFiles returns the current (post-rename) data file names, derived
// from the legacy list so both lists stay in sync.
func CurrentDataFiles() []string {
	out := make([]string, 0, len(legacyDataFiles)+len(currentCompanionFiles))
	for _, name := range legacyDataFiles {
		out = append(out, CurrentDataFileName(name))
	}
	out = append(out, currentCompanionFiles...)
	return out
}

// KnownDataFiles returns a copy of the legacy data file list so callers cannot
// mutate the migration list.
func KnownDataFiles() []string {
	out := make([]string, 0, len(legacyDataFiles)+1)
	out = append(out, legacyDataFiles...)
	out = append(out, ".cline-accounts.json.initialized")
	return out
}

// EstablishedDataEvidence reports whether the account-pool directory contains
// independent evidence of an existing RouteNest/legacy installation. The pool
// and its initialization witness are deliberately excluded: callers use this
// only when both are missing and must not guess that the directory is fresh.
func EstablishedDataEvidence(accountPath string) (bool, error) {
	dir := filepath.Dir(accountPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	poolName := filepath.Base(accountPath)
	witnessName := poolName + ".initialized"
	legacyPool, _ := LegacyDataFileName(poolName)
	legacyWitness := legacyPool + ".initialized"
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case poolName, witnessName:
			continue
		}
		if name == legacyPool || name == legacyWitness || name == "admin-auth" || name == "capture-logs" ||
			strings.HasPrefix(name, poolName+".corrupt-") ||
			strings.HasPrefix(name, witnessName+".corrupt-") {
			return true, nil
		}
		if strings.HasPrefix(name, ".routenest-") || strings.HasPrefix(name, ".cline-") {
			return true, nil
		}
	}
	return false, nil
}

// migrateOnce makes automatic legacy migration run at most once per process.
var migrateOnce sync.Once

// MigrateLegacyDataFilesOnce runs the migration for the first data directory
// selected in this process. It is idempotent.
func MigrateLegacyDataFilesOnce(dir string, legacyDirs func() []string) {
	migrateOnce.Do(func() {
		if err := MigrateDataFiles(dir, legacyDirs); err != nil {
			panicDataDir(fmt.Errorf("migrate existing state: %w", err))
		}
	})
}

var (
	testDataDirOnce sync.Once
	testDataDir     string
	userConfigDir   = os.UserConfigDir
	isTesting       = testing.Testing
)

// TestIsolatedDataDir lazily creates a temporary directory used by go test so
// tests never read or write the developer's real data files.
func TestIsolatedDataDir() string {
	testDataDirOnce.Do(func() {
		dir, err := os.MkdirTemp(os.TempDir(), "routenest-test-data-")
		if err != nil {
			log.Printf("Failed to create isolated test data dir: %v", err)
			return
		}
		testDataDir = dir
	})
	return testDataDir
}

func createDataDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("empty directory path")
	}
	if err := SecureDataPermissions(dir); err != nil {
		return fmt.Errorf("secure %s: %w", dir, err)
	}
	return nil
}

func panicDataDir(err error) {
	panic("RouteNest data directory: " + err.Error())
}

func defaultDataDir() (string, error) {
	base, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("resolve user config directory: empty path")
	}
	dir := filepath.Join(base, "routenest")
	if err := createDataDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// EnvDataDir returns the configured data directory, creating it if needed.
// ROUTENEST_DATA_DIR wins; the legacy CLINE2API_DATA_DIR is still honored as a
// fallback for existing deployments.
func EnvDataDir() string {
	dir := strings.TrimSpace(os.Getenv(DataDirEnv))
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv(LegacyDataDirEnv))
	}
	if dir == "" {
		return ""
	}
	if err := createDataDir(dir); err != nil {
		panicDataDir(err)
	}
	return dir
}

// resolveExistingInDir probes dir for the current-name data file. When only
// the legacy-named file exists in the same directory, it is migrated to the
// current name (never overwriting an existing current-name file) so pre-rename
// deployments upgrade in place instead of spawning a parallel empty fileset.
func resolveExistingInDir(dir, filename string) (string, bool) {
	p := filepath.Join(dir, filename)
	if FileExists(p) {
		return p, true
	}
	legacy, ok := LegacyDataFileName(filename)
	if !ok {
		return "", false
	}
	src := filepath.Join(dir, legacy)
	data, err := os.ReadFile(src)
	if err != nil {
		return "", false
	}
	if err := WriteFileAtomic(p, data, 0600); err != nil {
		log.Printf("Failed to migrate legacy %s in %s to %s: %v", legacy, dir, filename, err)
		return "", false
	}
	log.Printf("Migrated legacy %s in %s to %s", legacy, dir, p)
	return p, true
}

// ResolveDataPath resolves a data file path using this priority:
//  1. ROUTENEST_DATA_DIR (falling back to legacy CLINE2API_DATA_DIR,
//     migrating legacy files on first use);
//  2. a per-test temporary directory when running under go test;
//  3. the OS-native user config directory + "routenest".
//
// Executable, working and legacy home directories are migration sources only;
// new state is never created there by default.
func ResolveDataPath(filename string, legacyDirs func() []string) string {
	if dir := EnvDataDir(); dir != "" {
		MigrateLegacyDataFilesOnce(dir, legacyDirs)
		return filepath.Join(dir, filename)
	}
	if isTesting() {
		if dir := TestIsolatedDataDir(); dir != "" {
			return filepath.Join(dir, filename)
		}
	}
	dir, err := defaultDataDir()
	if err != nil {
		panicDataDir(err)
	}
	MigrateLegacyDataFilesOnce(dir, legacyDirs)
	return filepath.Join(dir, filename)
}

// CurrentDataDir returns the directory where new data files will be created.
func CurrentDataDir() string {
	if dir := EnvDataDir(); dir != "" {
		return dir
	}
	if isTesting() {
		if dir := TestIsolatedDataDir(); dir != "" {
			return dir
		}
	}
	dir, err := defaultDataDir()
	if err != nil {
		panicDataDir(err)
	}
	return dir
}

// LegacyDataCandidates returns de-duplicated absolute paths that may contain
// data files from an older installation: the executable directory, the working
// directory, ~/.routenest and ~/.cline2api. This matches the documented sources
// in README. The OS config directory is deliberately not a source: when
// ROUTENEST_DATA_DIR selects another directory, copying from the config directory
// would duplicate live credentials into it.
func LegacyDataCandidates(legacyDirs func() []string) []string {
	if legacyDirs != nil {
		return legacyDirs()
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if pwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, pwd)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".routenest"), filepath.Join(home, ".cline2api"))
	}
	seen := make(map[string]bool, len(dirs))
	out := dirs[:0]
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			abs = d
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out
}

// MigrateDataFiles copies known current and legacy state files into dir. It
// never overwrites a destination or removes a source, and verifies each copy.
func MigrateDataFiles(dir string, legacyDirs func() []string) error {
	if err := createDataDir(dir); err != nil {
		return err
	}
	sources := append([]string{dir}, LegacyDataCandidates(legacyDirs)...)
	seenSources := make(map[string]bool, len(sources))
	uniqueSources := sources[:0]
	for _, source := range sources {
		abs, err := filepath.Abs(source)
		if err != nil {
			abs = source
		}
		if !seenSources[abs] {
			seenSources[abs] = true
			uniqueSources = append(uniqueSources, abs)
		}
	}

	for _, name := range CurrentDataFiles() {
		dst := filepath.Join(dir, name)
		if FileExists(dst) {
			continue
		}
		legacy, _ := LegacyDataFileName(name)
		copied := false
		for _, sourceDir := range uniqueSources {
			sourceNames := []string{name, legacy}
			if name == AccountPoolWitnessName {
				sourceNames = []string{name, ".cline-accounts.json.initialized"}
			}
			for _, sourceName := range sourceNames {
				src := filepath.Join(sourceDir, sourceName)
				if src == dst {
					continue
				}
				data, err := os.ReadFile(src)
				if err != nil {
					continue
				}
				if err := WriteFileAtomic(dst, data, 0600); err != nil {
					return fmt.Errorf("copy %s to %s: %w", src, dst, err)
				}
				got, err := os.ReadFile(dst)
				if err != nil || !bytes.Equal(got, data) {
					_ = os.Remove(dst)
					if err != nil {
						return fmt.Errorf("verify %s: %w", dst, err)
					}
					return fmt.Errorf("verify %s: copied bytes differ", dst)
				}
				log.Printf("Migrated RouteNest state from %s to %s (source preserved)", src, dst)
				copied = true
				break
			}
			if copied {
				break
			}
		}
	}
	return nil
}

// MigrateLegacyDataFiles is retained for callers and tests from before current
// state filenames were included in migration.
func MigrateLegacyDataFiles(dir string, legacyDirs func() []string) {
	if err := MigrateDataFiles(dir, legacyDirs); err != nil {
		log.Printf("Failed to migrate RouteNest state into %s: %v", dir, err)
	}
}

// FileExists reports whether path exists.
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
