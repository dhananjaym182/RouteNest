package adminauth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	auth "routenest/internal/adminauth"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixturePHC = "$argon2id$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

func event(i int) auth.AuditEvent {
	b := make([]byte, 32)
	b[0] = byte(i)
	b[1] = byte(i >> 8)
	return auth.AuditEvent{ID: base64.RawURLEncoding.EncodeToString(b), Kind: auth.AuditBootstrapPending, Outcome: "success", At: fixedTime}
}

var fixedTime = func() time.Time { return time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC) }()

func applyFixture(s *Store, i int) error {
	return s.Apply(context.Background(), event(i), func(st *auth.State) error { st.BootstrapPHC = fixturePHC; return nil })
}
func TestStoreRestartAndIsolation(t *testing.T) {
	dir := privateDir(t)
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = applyFixture(s, 1); e != nil {
		t.Fatal(e)
	}
	st, _ := s.Load(context.Background())
	st.BootstrapPHC = "corrupt caller mutation"
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st, e = s.Load(context.Background())
	if e != nil || st.Revision != 1 || st.BootstrapPHC != fixturePHC {
		t.Fatal(st.Revision, e)
	}
	events, e := s.AuditEvents(context.Background())
	if e != nil || len(events) != 1 {
		t.Fatal(events, e)
	}
	if e = applyFixture(s, 1); e == nil {
		t.Fatal("duplicate event accepted")
	}
}

func TestStoreAcceptsFailedSecurityAuditOutcome(t *testing.T) {
	dir := privateDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := event(77)
	e.Kind = auth.AuditClientKeyDelete
	e.Outcome = "failed"
	if err := s.Apply(context.Background(), e, func(st *auth.State) error {
		st.BootstrapPHC = fixturePHC
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	events, err := s.AuditEvents(context.Background())
	if err != nil || len(events) != 1 || events[0].Outcome != "failed" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestStoreRejectsFailedOutcomeForNonCredentialEvent(t *testing.T) {
	dir := privateDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := event(78)
	e.Outcome = "failed"
	if err := s.Apply(context.Background(), e, func(*auth.State) error { return nil }); err == nil {
		t.Fatal("non-credential failed outcome accepted")
	}
}
func TestExclusiveLock(t *testing.T) {
	dir := privateDir(t)
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Open(dir); !errors.Is(e, ErrLocked) {
		t.Fatal("second process owner allowed", e)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
}
func TestFaultRecoveryEveryBoundary(t *testing.T) {
	for _, point := range []FaultPoint{BeforePrepare, AfterPrepare, BeforePrepareSync, AfterPrepareSync, BeforeSnapshotWrite, AfterSnapshotWrite, AfterSnapshotSync, AfterSnapshotRename, AfterDirectorySync, BeforeCommit, AfterCommit, BeforeCommitSync, AfterCommitSync} {
		t.Run(string(point), func(t *testing.T) {
			dir := privateDir(t)
			s, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			if e = applyFixture(s, 1); e != nil {
				t.Fatal(e)
			}
			s.fault = func(p FaultPoint) error {
				if p == point {
					return fmt.Errorf("injected failure")
				}
				return nil
			}
			if e = applyFixture(s, 2); e == nil {
				t.Fatal("fault not injected")
			}
			if _, e = s.Load(context.Background()); !errors.Is(e, auth.ErrDegraded) {
				t.Fatal("failed commit instance still serves", e)
			}
			s.Close()
			s, e = Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			st, _ := s.Load(context.Background())
			want := uint64(2)
			if point == BeforePrepare {
				want = 1
			}
			if st.Revision != want {
				t.Fatal("recovered wrong revision", st.Revision, want)
			}
			events, e := s.AuditEvents(context.Background())
			if e != nil || len(events) != int(want) {
				t.Fatal("audit not exactly once", len(events), e)
			}
			s.Close()
			s, e = Open(dir)
			if e != nil {
				t.Fatal("second recovery", e)
			}
			defer s.Close()
			events, _ = s.AuditEvents(context.Background())
			if len(events) != int(want) {
				t.Fatal("duplicate recovered audit")
			}
		})
	}
}
func TestCorruptEvidencePreserved(t *testing.T) {
	for _, kind := range []string{"snapshot", "chain", "truncated", "missing_snapshot"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateDir(t)
			s, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			if e = applyFixture(s, 1); e != nil {
				t.Fatal(e)
			}
			s.Close()
			path := filepath.Join(dir, JournalName)
			switch kind {
			case "snapshot":
				path = filepath.Join(dir, SnapshotName)
				if e = os.WriteFile(path, []byte("{}"), 0600); e != nil {
					t.Fatal(e)
				}
			case "chain":
				b, _ := os.ReadFile(path)
				b = []byte(strings.Replace(string(b), "bootstrap.pending", "password.rotate", 1))
				os.WriteFile(path, b, 0600)
			case "truncated":
				b, _ := os.ReadFile(path)
				os.WriteFile(path, b[:len(b)-1], 0600)
			case "missing_snapshot":
				os.Remove(filepath.Join(dir, SnapshotName))
			}
			before, _ := os.ReadFile(path)
			if _, e = Open(dir); e == nil {
				t.Fatal("corruption accepted")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("corrupt evidence rewritten")
			}
		})
	}
}
func TestConcurrentMutations(t *testing.T) {
	s, e := Open(privateDir(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for i := 1; i <= 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := applyFixture(s, i); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	st, e := s.Load(context.Background())
	if e != nil || st.Revision != 16 {
		t.Fatal(st.Revision, e)
	}
}
func TestCanceledAndInvalidMutationsLeaveStoreUsable(t *testing.T) {
	s, e := Open(privateDir(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.Apply(ctx, event(1), func(*auth.State) error { t.Fatal("canceled callback"); return nil }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = s.Apply(context.Background(), event(2), func(st *auth.State) error { st.Initialized = true; return nil }); e == nil {
		t.Fatal("invalid initialization accepted")
	}
	if e = applyFixture(s, 3); e != nil {
		t.Fatal("validation failure poisoned store", e)
	}
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	return dir
}
func TestAuthorityRemovalLocksLiveStore(t *testing.T) {
	s, e := Open(privateDir(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = applyFixture(s, 1); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(s.dir, SnapshotName)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Load(context.Background()); e == nil {
		t.Fatal("missing live authority served stale state")
	}
	if e = applyFixture(s, 2); !errors.Is(e, auth.ErrDegraded) {
		t.Fatal("live corruption did not lock down", e)
	}
}
func TestJournalReplacementRejected(t *testing.T) {
	s, e := Open(privateDir(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = applyFixture(s, 1); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.dir, JournalName)
	b, _ := os.ReadFile(path)
	if e = os.Rename(path, path+".old"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Load(context.Background()); !errors.Is(e, ErrCorrupt) {
		t.Fatal("replaced journal descriptor accepted", e)
	}
}
func TestDurableBootstrapSessionAndPasswordRestart(t *testing.T) {
	dir := privateDir(t)
	repo, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	options := auth.Options{PasswordParams: auth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}, BootstrapPassword: "local one-time bootstrap"}
	service, e := auth.NewService(repo, options)
	if e != nil {
		t.Fatal(e)
	}
	credentials, e := service.CompleteBootstrap(context.Background(), "local one-time bootstrap", "permanent durable password")
	if e != nil {
		t.Fatal(e)
	}
	repo.Close()
	repo, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer repo.Close()
	options.BootstrapPassword = "ignored changed bootstrap"
	service, e = auth.NewService(repo, options)
	if e != nil {
		t.Fatal(e)
	}
	principal, e := service.Authenticate(context.Background(), credentials.Token)
	if e != nil {
		t.Fatal("durable session lost", e)
	}
	if e = service.ValidateCSRF(principal, credentials.CSRFToken); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Login(context.Background(), "admin", "permanent durable password", "127.0.0.1"); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{SnapshotName, JournalName} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{credentials.Token, credentials.CSRFToken, "local one-time bootstrap", "permanent durable password"} {
			if strings.Contains(string(b), secret) {
				t.Fatal("durable plaintext secret", name)
			}
		}
	}
}
