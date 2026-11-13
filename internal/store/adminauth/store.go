// Package adminauth provides the single-process, fail-closed administrator store.
// Its local hash chain detects accidental or partial tampering; a host owner can
// rewrite all authority and therefore it is not independent tamper-proof retention.
package adminauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	auth "routenest/internal/adminauth"
	"sync"
)

const SnapshotName = ".routenest-admin-auth.json"
const JournalName = ".routenest-admin-auth.journal"
const LockName = ".routenest-admin-auth.lock"

var ErrLocked = errors.New("administrator store is owned by another process; local multi-replica authentication is unsupported")
var ErrCorrupt = errors.New("administrator store evidence is corrupt")

type FaultPoint string

const (
	BeforePrepare       FaultPoint = "before_prepare"
	AfterPrepare        FaultPoint = "after_prepare"
	BeforePrepareSync   FaultPoint = "before_prepare_sync"
	AfterPrepareSync    FaultPoint = "after_prepare_sync"
	BeforeSnapshotWrite FaultPoint = "before_snapshot_write"
	AfterSnapshotWrite  FaultPoint = "after_snapshot_write"
	AfterSnapshotSync   FaultPoint = "after_snapshot_sync"
	AfterSnapshotRename FaultPoint = "after_snapshot_rename"
	AfterDirectorySync  FaultPoint = "after_directory_sync"
	BeforeCommit        FaultPoint = "before_commit"
	AfterCommit         FaultPoint = "after_commit"
	BeforeCommitSync    FaultPoint = "before_commit_sync"
	AfterCommitSync     FaultPoint = "after_commit_sync"
)

type Options struct{ Fault func(FaultPoint) error }
type Store struct {
	mu       sync.Mutex
	dir      string
	journal  *os.File
	lock     *os.File
	state    auth.State
	head     string
	sequence uint64
	events   []auth.AuditEvent
	fault    func(FaultPoint) error
	closed   bool
	poisoned bool
}
type snapshot struct {
	Revision  uint64     `json:"revision"`
	StateHash string     `json:"stateHash"`
	State     auth.State `json:"state"`
}

func Open(dir string, options ...Options) (*Store, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("one store options value supported")
	}
	_, directoryErr := os.Lstat(dir)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if os.IsNotExist(directoryErr) {
		if e := secureCreatedDirectory(dir); e != nil {
			return nil, e
		}
	}
	if e := checkSecurePath(dir, true); e != nil {
		return nil, e
	}
	lock, e := openPrivate(filepath.Join(dir, LockName))
	if e != nil {
		return nil, e
	}
	if e = acquireLock(lock); e != nil {
		lock.Close()
		return nil, e
	}
	s := &Store{dir: dir, lock: lock, state: auth.EmptyState()}
	if len(options) == 1 {
		s.fault = options[0].Fault
	}
	defer func() {
		if e != nil {
			s.Close()
		}
	}()
	s.journal, e = openPrivate(filepath.Join(dir, JournalName))
	if e != nil {
		return nil, e
	}
	if e = syncDirectory(dir); e != nil {
		return nil, e
	}
	if e = s.recover(); e != nil {
		return nil, e
	}
	return s, nil
}
func openPrivate(path string) (*os.File, error) {
	if _, e := os.Lstat(path); e == nil {
		if e = checkSecurePath(path, false); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	_, existedErr := os.Lstat(path)
	f, e := openPrivateFile(path)
	if e != nil {
		return nil, e
	}
	if os.IsNotExist(existedErr) {
		if e = secureCreatedFile(path); e != nil {
			f.Close()
			return nil, e
		}
	}
	if e = checkSecurePath(path, false); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func (s *Store) hit(p FaultPoint) error {
	if s.fault != nil {
		return s.fault(p)
	}
	return nil
}
func (s *Store) Load(ctx context.Context) (auth.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return auth.State{}, e
	}
	if s.closed || s.poisoned {
		return auth.State{}, auth.ErrDegraded
	}
	disk, present, e := s.readSnapshot()
	if e != nil {
		s.poisoned = true
		return auth.State{}, e
	}
	if present && (disk.Revision != s.state.Revision || disk.StateHash != stateHash(s.state)) || !present && s.state.Revision != 0 {
		s.poisoned = true
		return auth.State{}, ErrCorrupt
	}
	if e = s.checkJournalIdentity(); e != nil {
		s.poisoned = true
		return auth.State{}, e
	}
	return auth.CloneState(s.state), nil
}
func (s *Store) Apply(ctx context.Context, event auth.AuditEvent, fn func(*auth.State) error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.closed || s.poisoned {
		return auth.ErrDegraded
	}
	if fn == nil {
		return fmt.Errorf("security mutation missing")
	}
	if e := validateEvent(event); e != nil {
		return e
	}
	// Check the persisted authority before each security mutation. Any unexpected
	// replacement or write after opening locks this instance down.
	disk, present, e := s.readSnapshot()
	if e != nil {
		s.poisoned = true
		return e
	}
	if present {
		if disk.Revision != s.state.Revision || disk.StateHash != stateHash(s.state) {
			s.poisoned = true
			return ErrCorrupt
		}
	} else if s.state.Revision != 0 {
		s.poisoned = true
		return ErrCorrupt
	}
	if e = s.checkJournalIdentity(); e != nil {
		s.poisoned = true
		return e
	}
	records, e := s.readRecords()
	if e != nil {
		s.poisoned = true
		return e
	}
	head := ""
	if len(records) > 0 {
		head = records[len(records)-1].Hash
	}
	if head != s.head || uint64(len(records)) != s.sequence {
		s.poisoned = true
		return ErrCorrupt
	}
	next := auth.CloneState(s.state)
	if e = fn(&next); e != nil {
		return e
	}
	if s.state.Initialized && !next.Initialized {
		return auth.ErrInvalidState
	}
	next.Revision = s.state.Revision + 1
	if e = auth.ValidateState(next); e != nil {
		return e
	}
	for _, old := range s.events {
		if old.ID == event.ID {
			return fmt.Errorf("duplicate audit event")
		}
	}
	preparedDurable := false
	defer func() {
		if err != nil {
			if preparedDurable {
				err = errors.Join(auth.ErrMutationPrepared, err)
			}
			s.poisoned = true
		}
	}()
	prepare := record{Sequence: s.sequence + 1, Type: "prepare", PreviousHash: s.head, PreviousRevision: s.state.Revision, NextRevision: next.Revision, Event: &event, State: &next, StateHash: stateHash(next)}
	prepare.seal()
	if e = s.hit(BeforePrepare); e != nil {
		return e
	}
	if e = s.appendRecord(prepare); e != nil {
		return e
	}
	if e = s.hit(AfterPrepare); e != nil {
		return e
	}
	if e = s.hit(BeforePrepareSync); e != nil {
		return e
	}
	if e = s.journal.Sync(); e != nil {
		return e
	}
	preparedDurable = true
	if e = s.hit(AfterPrepareSync); e != nil {
		return e
	}
	if e = s.writeSnapshot(next, true); e != nil {
		return e
	}
	commit := record{Sequence: prepare.Sequence + 1, Type: "commit", PreviousHash: prepare.Hash, PreviousRevision: prepare.PreviousRevision, NextRevision: prepare.NextRevision, PrepareHash: prepare.Hash, StateHash: prepare.StateHash}
	commit.seal()
	if e = s.hit(BeforeCommit); e != nil {
		return e
	}
	if e = s.appendRecord(commit); e != nil {
		return e
	}
	if e = s.hit(AfterCommit); e != nil {
		return e
	}
	if e = s.hit(BeforeCommitSync); e != nil {
		return e
	}
	if e = s.journal.Sync(); e != nil {
		return e
	}
	if e = s.hit(AfterCommitSync); e != nil {
		return e
	}
	s.state = next
	s.sequence = commit.Sequence
	s.head = commit.Hash
	s.events = append(s.events, event)
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	if s.journal != nil {
		errs = append(errs, s.journal.Close())
	}
	if s.lock != nil {
		errs = append(errs, releaseLock(s.lock), s.lock.Close())
	}
	return errors.Join(errs...)
}
func (s *Store) AuditEvents(ctx context.Context) ([]auth.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.closed || s.poisoned {
		return nil, auth.ErrDegraded
	}
	return append([]auth.AuditEvent(nil), s.events...), nil
}
func stateHash(state auth.State) string {
	data, _ := json.Marshal(state)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
func strictJSON(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func (s *Store) readSnapshot() (snapshot, bool, error) {
	path := filepath.Join(s.dir, SnapshotName)
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return snapshot{}, false, nil
	}
	if e := checkSecurePath(path, false); e != nil {
		return snapshot{}, false, e
	}
	b, e := readBounded(path, 16<<20)
	if e != nil {
		return snapshot{}, false, e
	}
	var v snapshot
	if e = strictJSON(b, &v); e != nil {
		return v, true, fmt.Errorf("%w: snapshot encoding", ErrCorrupt)
	}
	if v.Revision != v.State.Revision || v.StateHash != stateHash(v.State) {
		return v, true, fmt.Errorf("%w: snapshot hash", ErrCorrupt)
	}
	if e = auth.ValidateState(v.State); e != nil {
		return v, true, fmt.Errorf("%w: snapshot state", ErrCorrupt)
	}
	return v, true, nil
}
func readBounded(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: file size", ErrCorrupt)
	}
	return b, nil
}
func (s *Store) writeSnapshot(state auth.State, faults bool) error {
	data, e := json.Marshal(snapshot{state.Revision, stateHash(state), state})
	if e != nil {
		return e
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("administrator snapshot limit exceeded")
	}
	if faults {
		if e = s.hit(BeforeSnapshotWrite); e != nil {
			return e
		}
	}
	f, e := os.CreateTemp(s.dir, ".routenest-admin-auth-*.tmp")
	if e != nil {
		return e
	}
	path := f.Name()
	defer os.Remove(path)
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return e
	}
	if e = secureCreatedFile(path); e != nil {
		return e
	}
	n, e := f.Write(data)
	if e != nil {
		return e
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if faults {
		if e = s.hit(AfterSnapshotWrite); e != nil {
			return e
		}
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if faults {
		if e = s.hit(AfterSnapshotSync); e != nil {
			return e
		}
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = replaceSnapshot(path, filepath.Join(s.dir, SnapshotName)); e != nil {
		return e
	}
	if faults {
		if e = s.hit(AfterSnapshotRename); e != nil {
			return e
		}
	}
	if e = syncDirectory(s.dir); e != nil {
		return e
	}
	if faults {
		if e = s.hit(AfterDirectorySync); e != nil {
			return e
		}
	}
	v, ok, e := s.readSnapshot()
	if e != nil {
		return e
	}
	if !ok || v.Revision != state.Revision || v.StateHash != stateHash(state) {
		return ErrCorrupt
	}
	return nil
}
func validateEvent(e auth.AuditEvent) error {
	failedCredentialEvent := e.Outcome == "failed" && (e.Kind == auth.AuditClientKeyCreate || e.Kind == auth.AuditClientKeyDelete || e.Kind == auth.AuditClientKeyDeleteFinal)
	validOutcome := e.Outcome == "success" || e.Outcome == "denied" || failedCredentialEvent
	if len(e.ID) != 43 || !auth.ValidAuditKind(e.Kind) || e.At.IsZero() || !validOutcome || len(e.ActorID) > 256 || len(e.TargetID) > 256 || len(e.Source) > 64 {
		return fmt.Errorf("invalid redacted audit event")
	}
	return nil
}

func (s *Store) checkJournalIdentity() error {
	path := filepath.Join(s.dir, JournalName)
	if e := checkSecurePath(path, false); e != nil {
		return e
	}
	disk, e := os.Stat(path)
	if e != nil {
		return e
	}
	held, e := s.journal.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(disk, held) {
		return ErrCorrupt
	}
	return nil
}
