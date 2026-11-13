package adminauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	auth "routenest/internal/adminauth"
)

type inspectionEntry struct {
	info os.FileInfo
	data []byte
	link string
}

func inspectionDataDir(t *testing.T) string {
	t.Helper()
	dir := privateDir(t)
	if err := secureCreatedDirectory(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func inspectionTree(t *testing.T, dir string) map[string]inspectionEntry {
	t.Helper()
	entries := map[string]inspectionEntry{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := inspectionEntry{info: info}
		if info.Mode().IsRegular() {
			value.data, err = os.ReadFile(path)
		} else if info.Mode()&os.ModeSymlink != 0 {
			value.link, err = os.Readlink(path)
		}
		entries[path] = value
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func assertInspectionUnchanged(t *testing.T, dir string, before map[string]inspectionEntry) {
	t.Helper()
	after := inspectionTree(t, dir)
	if len(before) != len(after) {
		t.Fatal("inspection changed directory entries")
	}
	for path, original := range before {
		current, ok := after[path]
		if !ok || !sameInspectionEntry(original, current) {
			t.Fatalf("inspection changed %s", path)
		}
	}
}

func sameInspectionEntry(before, after inspectionEntry) bool {
	identity := os.SameFile(before.info, after.info)
	if runtime.GOOS == "windows" {
		identity = before.info.IsDir() == after.info.IsDir()
	}
	return identity && before.info.Mode() == after.info.Mode() && before.link == after.link && bytes.Equal(before.data, after.data)
}

func inspectionStore(t *testing.T) (string, *Store) {
	t.Helper()
	dataDir := inspectionDataDir(t)
	store, err := Open(filepath.Join(dataDir, "admin-auth"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := applyFixture(store, 1); err != nil {
		t.Fatal(err)
	}
	return dataDir, store
}

func TestInspectReadOnlyBeforeAndDuringStoreOwnership(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "offline"
		if running {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			dataDir, store := inspectionStore(t)
			if !running {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := inspectionTree(t, dataDir)
			state, err := Inspect(t.Context(), dataDir)
			if err != nil || state.Revision != 1 || state.BootstrapPHC != fixturePHC {
				t.Fatalf("inspection revision = %d, error = %v", state.Revision, err)
			}
			state.Users["caller-mutation"] = auth.User{}
			state, err = Inspect(t.Context(), dataDir)
			if err != nil || len(state.Users) != 0 {
				t.Fatal("inspection did not return detached state", err)
			}
			assertInspectionUnchanged(t, dataDir, before)
			if running {
				if other, err := OpenForRecovery(dataDir); !errors.Is(err, ErrLocked) {
					if other != nil {
						other.Close()
					}
					t.Fatal("inspection released the running store's lock", err)
				}
			} else {
				store, err = Open(filepath.Join(dataDir, "admin-auth"))
				if err != nil {
					t.Fatal("inspection retained a store lock", err)
				}
				defer store.Close()
			}
			if err := applyFixture(store, 2); err != nil {
				t.Fatal("inspection interfered with the writer", err)
			}
		})
	}
}

func TestInspectMissingAuthorityDoesNotCreate(t *testing.T) {
	for _, missing := range []string{"data", "authority", "empty", "uncommitted", "snapshot", "journal", "lock", "snapshot-and-journal"} {
		t.Run(missing, func(t *testing.T) {
			dataDir := inspectionDataDir(t)
			dir := filepath.Join(dataDir, "admin-auth")
			switch missing {
			case "data":
				dataDir = filepath.Join(dataDir, "missing", "nested")
			case "authority":
			case "empty":
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := secureCreatedDirectory(dir); err != nil {
					t.Fatal(err)
				}
			default:
				store, err := Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				if missing != "uncommitted" {
					if err := applyFixture(store, 1); err != nil {
						store.Close()
						t.Fatal(err)
					}
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				for label, filename := range map[string]string{"snapshot": SnapshotName, "journal": JournalName, "lock": LockName} {
					if missing == label || missing == "snapshot-and-journal" && label != "lock" {
						if err := os.Remove(filepath.Join(dir, filename)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			before := inspectionTree(t, dataDir)
			state, err := Inspect(t.Context(), dataDir)
			if !errors.Is(err, ErrMissing) || state.Version != 0 {
				t.Fatalf("missing authority = version %d, error %v", state.Version, err)
			}
			assertInspectionUnchanged(t, dataDir, before)
		})
	}
}

func TestInspectPreparedTransactionsRequireRestart(t *testing.T) {
	for _, point := range []FaultPoint{AfterPrepare, BeforePrepareSync, AfterPrepareSync, BeforeSnapshotWrite, AfterSnapshotWrite, AfterSnapshotSync, AfterSnapshotRename, AfterDirectorySync, BeforeCommit} {
		for _, initial := range []bool{false, true} {
			name := string(point) + "/existing"
			if initial {
				name = string(point) + "/first"
			}
			t.Run(name, func(t *testing.T) {
				dataDir := inspectionDataDir(t)
				dir := filepath.Join(dataDir, "admin-auth")
				store, err := Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if !initial {
					if err := applyFixture(store, 1); err != nil {
						t.Fatal(err)
					}
				}
				store.fault = func(current FaultPoint) error {
					if current == point {
						return errors.New("injected preparation failure")
					}
					return nil
				}
				err = store.Apply(t.Context(), event(2), func(state *auth.State) error {
					state.Initialized = true
					state.BootstrapPHC = ""
					state.RecoveryCodePHC = fixturePHC
					state.Users[auth.BreakGlassID] = auth.User{ID: auth.BreakGlassID, Username: auth.BreakGlassUsername, PasswordPHC: fixturePHC, Roles: []auth.Role{auth.RoleBreakGlass, auth.RoleSecurityAdmin}, Revision: 1, CreatedAt: fixedTime}
					return nil
				})
				if err == nil {
					t.Fatal("preparation failure was not injected")
				}
				for _, running := range []bool{true, false} {
					if !running {
						if err := store.Close(); err != nil {
							t.Fatal(err)
						}
					}
					before := inspectionTree(t, dataDir)
					state, err := Inspect(t.Context(), dataDir)
					if !errors.Is(err, ErrPrepared) || !strings.Contains(err.Error(), "restart") || state.Version != 0 {
						t.Fatalf("prepared inspection published state: version %d, error %v", state.Version, err)
					}
					assertInspectionUnchanged(t, dataDir, before)
				}
				restarted, err := Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer restarted.Close()
				state, err := Inspect(t.Context(), dataDir)
				if err != nil || !state.Initialized || state.RecoveryCodePHC != fixturePHC {
					t.Fatal("normal restart failed to finalize prepared authority", err)
				}
			})
		}
	}
}

func TestInspectRejectsCorruptEvidenceWithoutMutation(t *testing.T) {
	for _, corruption := range []string{"snapshot-encoding", "snapshot-unknown-field", "snapshot-hash", "snapshot-state", "snapshot-journal-mismatch", "journal-encoding", "journal-chain", "journal-truncated", "journal-empty-record"} {
		t.Run(corruption, func(t *testing.T) {
			dataDir, store := inspectionStore(t)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dataDir, "admin-auth", SnapshotName)
			if strings.HasPrefix(corruption, "journal-") {
				path = filepath.Join(dataDir, "admin-auth", JournalName)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch corruption {
			case "snapshot-encoding", "journal-encoding":
				data = []byte("{sensitive-invalid-json}\n")
			case "snapshot-unknown-field":
				data = append([]byte(`{"unknown":true,`), data[1:]...)
			case "snapshot-hash":
				data = bytes.Replace(data, []byte(`"stateHash":"`), []byte(`"stateHash":"0`), 1)
			case "snapshot-state", "snapshot-journal-mismatch":
				var disk snapshot
				if err := json.Unmarshal(data, &disk); err != nil {
					t.Fatal(err)
				}
				if corruption == "snapshot-state" {
					disk.State.Initialized = true
				} else {
					disk.State.BootstrapPHC = ""
				}
				disk.StateHash = stateHash(disk.State)
				data, err = json.Marshal(disk)
				if err != nil {
					t.Fatal(err)
				}
			case "journal-chain":
				data = bytes.Replace(data, []byte("bootstrap.pending"), []byte("password.rotate"), 1)
			case "journal-truncated":
				data = data[:len(data)-1]
			case "journal-empty-record":
				data = append(data, '\n')
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			before := inspectionTree(t, dataDir)
			state, err := Inspect(t.Context(), dataDir)
			if !errors.Is(err, ErrCorrupt) || state.Version != 0 {
				t.Fatalf("corrupt inspection = version %d, error %v", state.Version, err)
			}
			if strings.Contains(err.Error(), fixturePHC) || strings.Contains(err.Error(), "sensitive-invalid-json") {
				t.Fatal("inspection error disclosed state contents")
			}
			assertInspectionUnchanged(t, dataDir, before)
		})
	}
}

func TestInspectRejectsInsecurePathsWithoutRepair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission and symlink cases")
	}
	for _, target := range []string{"data", "authority", "snapshot", "journal", "lock"} {
		for _, change := range []string{"permissions", "symlink", "type"} {
			t.Run(target+"/"+change, func(t *testing.T) {
				dataDir, store := inspectionStore(t)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(dataDir, "admin-auth")
				path := map[string]string{"data": dataDir, "authority": dir, "snapshot": filepath.Join(dir, SnapshotName), "journal": filepath.Join(dir, JournalName), "lock": filepath.Join(dir, LockName)}[target]
				isDir := target == "data" || target == "authority"
				switch change {
				case "permissions":
					mode := os.FileMode(0644)
					if isDir {
						mode = 0755
					}
					if err := os.Chmod(path, mode); err != nil {
						t.Fatal(err)
					}
				case "symlink", "type":
					saved := path + ".saved"
					if err := os.Rename(path, saved); err != nil {
						t.Fatal(err)
					}
					if target == "data" {
						t.Cleanup(func() { os.RemoveAll(saved) })
					}
					var err error
					if change == "symlink" {
						err = os.Symlink(saved, path)
					} else if isDir {
						err = os.WriteFile(path, nil, 0600)
					} else {
						err = os.Mkdir(path, 0700)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				before := inspectionTree(t, dataDir)
				for _, inspectionDir := range []string{dataDir, dataDir + string(os.PathSeparator)} {
					state, err := Inspect(t.Context(), inspectionDir)
					if err == nil || state.Version != 0 {
						t.Fatal("insecure path accepted")
					}
				}
				assertInspectionUnchanged(t, dataDir, before)
			})
		}
	}
}

func TestInspectRetryIsBounded(t *testing.T) {
	for _, cause := range []error{ErrUncertain, ErrPrepared, ErrMissing, ErrCorrupt} {
		t.Run(cause.Error(), func(t *testing.T) {
			calls := 0
			state, err := retryInspection(t.Context(), func() (auth.State, error) {
				calls++
				return auth.State{Version: auth.StateVersion, Initialized: true}, cause
			})
			if !errors.Is(err, cause) || calls != inspectionAttempts || state.Version != 0 {
				t.Fatalf("bounded retry = calls %d, version %d, error %v", calls, state.Version, err)
			}
		})
	}
	calls := 0
	_, err := retryInspection(t.Context(), func() (auth.State, error) {
		calls++
		return auth.State{}, fs.ErrPermission
	})
	if !errors.Is(err, fs.ErrPermission) || calls != 1 {
		t.Fatal("unsafe read was retried", err)
	}
}

func TestInspectRetriesConcurrentCommit(t *testing.T) {
	for _, point := range []FaultPoint{AfterPrepareSync, AfterSnapshotRename} {
		t.Run(string(point), func(t *testing.T) {
			dataDir, store := inspectionStore(t)
			ready := make(chan struct{})
			resume := make(chan struct{})
			done := make(chan error, 1)
			store.fault = func(current FaultPoint) error {
				if current == point {
					close(ready)
					<-resume
				}
				return nil
			}
			go func() { done <- applyFixture(store, 2) }()
			<-ready
			calls := 0
			state, err := retryInspection(t.Context(), func() (auth.State, error) {
				calls++
				state, err := inspectOnce(t.Context(), dataDir)
				if calls == 1 {
					close(resume)
					writeErr := <-done
					if !errors.Is(err, ErrPrepared) {
						t.Fatalf("writer was not observed prepared: %v", err)
					}
					if writeErr != nil {
						t.Fatal(writeErr)
					}
				}
				return state, err
			})
			if err != nil || state.Revision != 2 || calls != 2 {
				t.Fatalf("concurrent commit = revision %d, attempts %d, error %v", state.Revision, calls, err)
			}
		})
	}
}

func TestInspectConcurrentSnapshotRevisions(t *testing.T) {
	dataDir, store := inspectionStore(t)
	start := make(chan struct{})
	continueWriter := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(start)
		<-continueWriter
		for i := 2; i <= 50; i++ {
			if err := applyFixture(store, i); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	<-start
	close(continueWriter)
	var failure error
	for i := 0; i < 50; i++ {
		state, err := Inspect(t.Context(), dataDir)
		if err == nil {
			if state.Revision < 1 || state.Revision > 50 || state.BootstrapPHC != fixturePHC {
				failure = errors.New("inconsistent concurrent state")
				break
			}
		} else if !errors.Is(err, ErrPrepared) && !errors.Is(err, ErrUncertain) {
			failure = err
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if failure != nil {
		t.Fatal("concurrent inspection failed closed as corruption", failure)
	}
	before := inspectionTree(t, dataDir)
	state, err := Inspect(t.Context(), dataDir)
	if err != nil || state.Revision != 50 {
		t.Fatalf("settled revision = %d, error = %v", state.Revision, err)
	}
	assertInspectionUnchanged(t, dataDir, before)
}

func TestInspectFileIsReadOnlyAndAllowsSnapshotReplacement(t *testing.T) {
	dataDir, store := inspectionStore(t)
	dir := filepath.Join(dataDir, "admin-auth")
	root, err := openInspectionRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := inspectSecureInfo(filepath.Join(dir, SnapshotName), false)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openInspectionFile(root, SnapshotName, info)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before := inspectionTree(t, dataDir)
	if written, err := file.Write([]byte("forbidden-write")); err == nil || written != 0 {
		t.Fatal("inspection descriptor permitted writes")
	}
	assertInspectionUnchanged(t, dataDir, before)
	if err := applyFixture(store, 2); err != nil {
		t.Fatal("inspection descriptor blocked snapshot replacement", err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	var previous snapshot
	if err := strictJSON(data, &previous); err != nil || previous.Revision != 1 {
		t.Fatal("inspection descriptor lost its original snapshot", err)
	}
	state, err := Inspect(t.Context(), dataDir)
	if err != nil || state.Revision != 2 {
		t.Fatal("inspection did not observe replacement snapshot", err)
	}
}

func TestInspectFileIdentityRejectsReplacement(t *testing.T) {
	dataDir, store := inspectionStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "admin-auth")
	root, err := openInspectionRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(dir, SnapshotName)
	info, err := inspectSecureInfo(path, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	before := inspectionTree(t, dataDir)
	file, err := openInspectionFile(root, SnapshotName, info)
	if file != nil {
		file.Close()
		t.Fatal("replacement file was opened as the original")
	}
	if !errors.Is(err, ErrUncertain) {
		t.Fatal("replacement was not uncertain", err)
	}
	assertInspectionUnchanged(t, dataDir, before)
}

func TestInspectBoundedReads(t *testing.T) {
	for _, kind := range []string{"snapshot", "journal", "record"} {
		t.Run(kind, func(t *testing.T) {
			dataDir, store := inspectionStore(t)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dataDir, "admin-auth", JournalName)
			size := int64(inspectionJournalLimit + 1)
			if kind == "snapshot" {
				path = filepath.Join(dataDir, "admin-auth", SnapshotName)
				size = inspectionSnapshotLimit + 1
			} else if kind == "record" {
				size = inspectionSnapshotLimit + 2
			}
			if err := os.Truncate(path, size); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			state, err := Inspect(t.Context(), dataDir)
			if !errors.Is(err, ErrCorrupt) || state.Version != 0 {
				t.Fatal("oversized evidence accepted", err)
			}
			after, err := os.Stat(path)
			if err != nil || before.Mode() != after.Mode() || before.Size() != after.Size() {
				t.Fatal("oversized evidence changed", err)
			}
		})
	}
}

func TestInspectJournalValidationMatchesRecovery(t *testing.T) {
	initialized := auth.EmptyState()
	initialized.Initialized = true
	initialized.Users[auth.BreakGlassID] = auth.User{ID: auth.BreakGlassID, Username: auth.BreakGlassUsername, PasswordPHC: fixturePHC, Roles: []auth.Role{auth.RoleBreakGlass, auth.RoleSecurityAdmin}, Revision: 1, CreatedAt: fixedTime}
	states := []auth.State{initialized, initialized}
	for _, corruption := range []string{"sequence", "previous-hash", "unknown-type", "missing-state", "missing-event", "event-invalid", "event-duplicate", "revision", "prepare-hash", "state-hash", "state-invalid", "initialized-downgrade", "commit-without-prepare", "double-prepare", "commit-state", "commit-event", "commit-prepare-hash", "commit-revision", "commit-state-hash"} {
		t.Run(corruption, func(t *testing.T) {
			dataDir, store := inspectionStore(t)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			records := inspectionRecords(states...)
			switch corruption {
			case "sequence":
				records[2].Sequence++
			case "previous-hash":
				records[0].PreviousHash = "invalid"
			case "unknown-type":
				records[2].Type = "unknown"
			case "missing-state":
				records[2].State = nil
			case "missing-event":
				records[2].Event = nil
			case "event-invalid":
				records[2].Event.Outcome = "unknown"
			case "event-duplicate":
				records[2].Event.ID = records[0].Event.ID
			case "revision":
				records[2].PreviousRevision = 0
			case "prepare-hash":
				records[2].PrepareHash = records[0].Hash
			case "state-hash":
				records[2].StateHash = strings.Repeat("0", 64)
			case "state-invalid":
				records[2].State.Users = nil
				records[2].StateHash = stateHash(*records[2].State)
			case "initialized-downgrade":
				records[2].State.Initialized = false
				records[2].StateHash = stateHash(*records[2].State)
			case "commit-without-prepare":
				records = records[1:2]
				records[0].Sequence = 1
				records[0].PreviousHash = ""
			case "double-prepare":
				records[1] = records[2]
				records[1].Sequence = 2
			case "commit-state":
				records[3].State = records[2].State
			case "commit-event":
				records[3].Event = records[2].Event
			case "commit-prepare-hash":
				records[3].PrepareHash = "invalid"
			case "commit-revision":
				records[3].NextRevision++
			case "commit-state-hash":
				records[3].StateHash = strings.Repeat("0", 64)
			}
			var journal bytes.Buffer
			for i := range records {
				if i > 0 {
					records[i].PreviousHash = records[i-1].Hash
					if records[i].Type == "commit" && !(corruption == "commit-prepare-hash" && i == 3) {
						records[i].PrepareHash = records[i-1].Hash
					}
				}
				records[i].seal()
				if err := json.NewEncoder(&journal).Encode(records[i]); err != nil {
					t.Fatal(err)
				}
			}
			dir := filepath.Join(dataDir, "admin-auth")
			if err := os.WriteFile(filepath.Join(dir, JournalName), journal.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			before := inspectionTree(t, dataDir)
			_, _, inspectedErr := inspectJournalFixture(t, dir)
			_, recoveryErr := (&Store{dir: dir}).readRecords()
			if !errors.Is(inspectedErr, ErrCorrupt) || !errors.Is(recoveryErr, ErrCorrupt) {
				t.Fatalf("validation mismatch: inspection %v, recovery %v", inspectedErr, recoveryErr)
			}
			assertInspectionUnchanged(t, dataDir, before)
		})
	}
}

func inspectJournalFixture(t *testing.T, dir string) (auth.State, *record, error) {
	t.Helper()
	root, err := openInspectionRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := root.Stat(JournalName)
	if err != nil {
		t.Fatal(err)
	}
	return inspectJournal(t.Context(), root, info)
}

func inspectionRecords(states ...auth.State) []record {
	var records []record
	head := ""
	for i, state := range states {
		state = auth.CloneState(state)
		state.Revision = uint64(i + 1)
		audit := event(i + 1)
		prepare := record{Sequence: uint64(2*i + 1), Type: "prepare", PreviousHash: head, PreviousRevision: uint64(i), NextRevision: state.Revision, Event: &audit, State: &state, StateHash: stateHash(state)}
		prepare.seal()
		commit := record{Sequence: prepare.Sequence + 1, Type: "commit", PreviousHash: prepare.Hash, PreviousRevision: prepare.PreviousRevision, NextRevision: prepare.NextRevision, PrepareHash: prepare.Hash, StateHash: prepare.StateHash}
		commit.seal()
		records = append(records, prepare, commit)
		head = commit.Hash
	}
	return records
}

func TestInspectLegacyStateHashCompatibility(t *testing.T) {
	dataDir, store := inspectionStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	legacyState := []byte(`{"version":1,"revision":1,"initialized":false,"bootstrapPHC":"` + fixturePHC + `","users":{},"sessions":{}}`)
	digest := func(value []byte) string {
		hash := sha256.Sum256(value)
		return hex.EncodeToString(hash[:])
	}
	hash := digest(legacyState)
	var state auth.State
	if err := strictJSON(legacyState, &state); err != nil || stateHash(state) != hash {
		t.Fatal("optional fields changed legacy state hashing", err)
	}
	prepare := `{"sequence":1,"type":"prepare","previousHash":"","previousRevision":0,"nextRevision":1,"event":{"id":"` + event(1).ID + `","kind":"bootstrap.pending","outcome":"success","at":"2026-11-07T00:00:00Z"},"state":` + string(legacyState) + `,"stateHash":"` + hash + `","hash":""}`
	prepareHash := digest([]byte(prepare))
	commit := `{"sequence":2,"type":"commit","previousHash":"` + prepareHash + `","previousRevision":0,"nextRevision":1,"prepareHash":"` + prepareHash + `","stateHash":"` + hash + `","hash":""}`
	journal := strings.Replace(prepare, `"hash":""}`, `"hash":"`+prepareHash+`"}`, 1) + "\n" + strings.Replace(commit, `"hash":""}`, `"hash":"`+digest([]byte(commit))+`"}`, 1) + "\n"
	dir := filepath.Join(dataDir, "admin-auth")
	if err := os.WriteFile(filepath.Join(dir, JournalName), []byte(journal), 0600); err != nil {
		t.Fatal(err)
	}
	snapshotData := `{"revision":1,"stateHash":"` + hash + `","state":` + string(legacyState) + `}`
	if err := os.WriteFile(filepath.Join(dir, SnapshotName), []byte(snapshotData), 0600); err != nil {
		t.Fatal(err)
	}
	before := inspectionTree(t, dataDir)
	state, err := Inspect(t.Context(), dataDir)
	if err != nil || state.Revision != 1 || state.BootstrapPHC != fixturePHC || len(state.TrustedDevices) != 0 || state.RecoveryCodePHC != "" {
		t.Fatal("legacy authority inspection failed", err)
	}
	assertInspectionUnchanged(t, dataDir, before)
}

func TestInspectCanceledDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	started := time.Now()
	_, err := retryInspection(ctx, func() (auth.State, error) {
		calls++
		cancel()
		return auth.State{}, ErrUncertain
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(started) > time.Second {
		t.Fatalf("canceled retry = calls %d, error %v", calls, err)
	}
}

func TestInspectReadOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix read-only permission modes")
	}
	dataDir, store := inspectionStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "admin-auth")
	for _, name := range []string{SnapshotName, JournalName, LockName} {
		if err := os.Chmod(filepath.Join(dir, name), 0400); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(dataDir, 0700)
		os.Chmod(dir, 0700)
	})
	before := inspectionTree(t, dataDir)
	state, err := Inspect(t.Context(), dataDir)
	if err != nil || state.Revision != 1 {
		t.Fatal("inspection required write permission", err)
	}
	assertInspectionUnchanged(t, dataDir, before)
}

func TestInspectPreparedSnapshotMustMatchJournal(t *testing.T) {
	for _, kind := range []string{"missing-committed", "different-state"} {
		t.Run(kind, func(t *testing.T) {
			dataDir, store := inspectionStore(t)
			store.fault = func(point FaultPoint) error {
				if point == BeforeSnapshotWrite {
					return errors.New("injected prepared snapshot failure")
				}
				return nil
			}
			if err := applyFixture(store, 2); !errors.Is(err, auth.ErrMutationPrepared) {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dataDir, "admin-auth", SnapshotName)
			want := ErrMissing
			if kind == "missing-committed" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				want = ErrCorrupt
				state := auth.EmptyState()
				state.Revision = 1
				data, err := json.Marshal(snapshot{state.Revision, stateHash(state), state})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := inspectionTree(t, dataDir)
			state, err := Inspect(t.Context(), dataDir)
			if !errors.Is(err, want) || errors.Is(err, ErrPrepared) || state.Version != 0 {
				t.Fatalf("inconsistent prepared authority = version %d, error %v", state.Version, err)
			}
			assertInspectionUnchanged(t, dataDir, before)
		})
	}
}

func TestInspectCanceledDoesNotCreate(t *testing.T) {
	dataDir := filepath.Join(inspectionDataDir(t), "missing")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(ctx, dataDir); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatal("canceled inspection created state", err)
	}
}
