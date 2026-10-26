package platform

import (
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

// legacyDataFiles lists every LEGACY (pre-rename) data file name considered
// when migrating an old data directory. Keep this list in sync with the files
// the application persists under names that predate the RouteNest rename.
var legacyDataFiles = []string{
	".cline-accounts.json",
	".cline-config.json",
	".cline-providers.json",
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
	out := make([]string, 0, len(legacyDataFiles))
	for _, name := range legacyDataFiles {
		out = append(out, CurrentDataFileName(name))
	}
	return out
}

// KnownDataFiles returns a copy of the legacy data file list so callers cannot
// mutate the migration list.
func KnownDataFiles() []string {
	out := make([]string, len(legacyDataFiles))
	copy(out, legacyDataFiles)
	return out
}

// migrateOnce makes automatic legacy migration run at most once per process.
var migrateOnce sync.Once

// MigrateLegacyDataFilesOnce runs the migration for the first data directory
// selected in this process. It is idempotent.
func MigrateLegacyDataFilesOnce(dir string, legacyDirs func() []string) {
	migrateOnce.Do(func() { MigrateLegacyDataFiles(dir, legacyDirs) })
}

var (
	testDataDirOnce sync.Once
	testDataDir     string
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Printf("Failed to create data dir %s: %v", dir, err)
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
//  3. executable directory, working directory, then ~/.routenest, with legacy
//     names (same location, or ~/.cline2api for the home directory) migrated
//     in place when only those exist;
//  4. executable directory as the creation fallback.
func ResolveDataPath(filename string, legacyDirs func() []string) string {
	if dir := EnvDataDir(); dir != "" {
		MigrateLegacyDataFilesOnce(dir, legacyDirs)
		return filepath.Join(dir, filename)
	}
	if testing.Testing() {
		if dir := TestIsolatedDataDir(); dir != "" {
			return filepath.Join(dir, filename)
		}
	}
	if exe, err := os.Executable(); err == nil {
		if p, ok := resolveExistingInDir(filepath.Dir(exe), filename); ok {
			return p
		}
	}
	if pwd, err := os.Getwd(); err == nil {
		if p, ok := resolveExistingInDir(pwd, filename); ok {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p, ok := resolveExistingInDir(filepath.Join(home, ".routenest"), filename); ok {
			return p
		}
		// Legacy home data directory from before the rename.
		if legacy, ok := LegacyDataFileName(filename); ok {
			src := filepath.Join(home, ".cline2api", legacy)
			if data, err := os.ReadFile(src); err == nil {
				dst := filepath.Join(home, ".routenest", filename)
				if err := os.MkdirAll(filepath.Dir(dst), 0700); err == nil {
					if err := WriteFileAtomic(dst, data, 0600); err == nil {
						log.Printf("Migrated legacy %s from %s to %s", legacy, src, dst)
						return dst
					}
				}
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), filename)
	}
	pwd, _ := os.Getwd()
	return filepath.Join(pwd, filename)
}

// CurrentDataDir returns the directory where new data files will be created.
func CurrentDataDir() string {
	if dir := EnvDataDir(); dir != "" {
		return dir
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	pwd, _ := os.Getwd()
	return pwd
}

// LegacyDataCandidates returns de-duplicated absolute paths that may contain
// data files from an older installation: exe directory, working directory,
// the current home data directory and the legacy ~/.cline2api directory.
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

// MigrateLegacyDataFiles copies known LEGACY data files (".cline-*") from
// legacy locations into dir under their current "routenest" names, only when
// the destination does not already exist. It is idempotent.
func MigrateLegacyDataFiles(dir string, legacyDirs func() []string) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Printf("Failed to create data dir %s for migration: %v", dir, err)
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	for _, legacy := range legacyDataFiles {
		name := CurrentDataFileName(legacy)
		dst := filepath.Join(dir, name)
		if FileExists(dst) {
			continue
		}
		// Legacy file may live beside its renamed successor in dir itself
		// (e.g. data dir == exe dir for a pre-rename deployment).
		if data, err := os.ReadFile(filepath.Join(dir, legacy)); err == nil {
			if err := WriteFileAtomic(dst, data, 0600); err != nil {
				log.Printf("Failed to migrate legacy %s in %s to %s: %v", legacy, dir, dst, err)
			} else {
				log.Printf("Migrated legacy %s in %s to %s", legacy, dir, dst)
			}
			continue
		}
		for _, d := range LegacyDataCandidates(legacyDirs) {
			if d == absDir {
				continue
			}
			src := filepath.Join(d, legacy)
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			if err := WriteFileAtomic(dst, data, 0600); err != nil {
				log.Printf("Failed to migrate %s from %s to %s: %v", legacy, d, dst, err)
				break
			}
			log.Printf("Migrated %s from %s to %s", legacy, d, dst)
			break
		}
	}
}

// FileExists reports whether path exists.
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
