package budget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"routenest/internal/domain"
)

// sharedBackend is a deterministic second adapter: every command reads, checks,
// and publishes under one shared mutex. No store-local lock can enforce its
// limits. The serialized document reconstructs a wholly new backend on restart.
type sharedBackend struct {
	mu           sync.Mutex
	states       map[string]State
	outage       bool
	failWrites   bool
	unsafeHealth bool
}

func newSharedBackend() *sharedBackend { return &sharedBackend{states: map[string]State{}} }

func (b *sharedBackend) Snapshot(ctx context.Context, providerID string) (State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, false); err != nil {
		return State{}, err
	}
	return b.states[providerID].Clone(), nil
}

func (b *sharedBackend) CheckAndRecord(ctx context.Context, command AdmissionCommand) (Admission, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, false); err != nil {
		return Admission{}, err
	}
	state := b.states[command.ProviderID].Clone()
	receipt, err := Admit(&state, command)
	if err != nil && !errors.Is(err, ErrExhausted) {
		return Admission{}, err
	}
	if err := b.check(ctx, true); err != nil {
		return Admission{}, err
	}
	b.states[command.ProviderID] = state
	return receipt, err
}

func (b *sharedBackend) RecordTokens(ctx context.Context, event TokenEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, false); err != nil {
		return err
	}
	state := b.states[event.ProviderID].Clone()
	if err := Account(&state, event); err != nil {
		return err
	}
	if err := b.check(ctx, true); err != nil {
		return err
	}
	b.states[event.ProviderID] = state
	return nil
}

func (b *sharedBackend) check(ctx context.Context, write bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.outage || (write && b.failWrites) {
		return errors.New("cannot reach https://secret:credential@example.test/proxy")
	}
	return nil
}

func (b *sharedBackend) Health() domain.RuntimeStateHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unsafeHealth {
		return domain.RuntimeStateHealth{Backend: "https://secret:credential@example.test/proxy", Reason: "secret credential at /private/path"}
	}
	ready := !b.outage && !b.failWrites
	return domain.RuntimeStateHealth{Backend: "shared", Ready: ready, SingleNodeReady: ready, MultiReplicaSupported: true, Degraded: !ready}
}

func (b *sharedBackend) setOutage(outage bool) {
	b.mu.Lock()
	b.outage = outage
	b.mu.Unlock()
}

func (b *sharedBackend) restart(t *testing.T) *sharedBackend {
	t.Helper()
	b.mu.Lock()
	data, err := json.Marshal(b.states)
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	out := newSharedBackend()
	if err := json.Unmarshal(data, &out.states); err != nil {
		t.Fatal(err)
	}
	return out
}

func testStore(backend Backend, now func() time.Time, policy Policy, options Options) *Store {
	options.Now = now
	return NewStore(backend, func(string) Policy { return policy }, options)
}

func mustAdmit(t *testing.T, store *Store, provider, model, attempt string) Admission {
	t.Helper()
	receipt, err := store.CheckAndRecord(context.Background(), provider, model, attempt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func mustTokens(t *testing.T, store *Store, provider, attempt, event string, tokens int64) {
	t.Helper()
	if err := store.RecordTokens(context.Background(), provider, attempt, event, tokens); err != nil {
		t.Fatal(err)
	}
}

func TestRestartReconstructsAccountingAndReceipts(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	backend := newSharedBackend()
	policy := Policy{RPMLimit: 2, DailyLimit: 2, TokenLimit: 10, TokenWindow: "daily"}
	store := testStore(backend, clock, policy, Options{})
	first := mustAdmit(t, store, "p", "m", "attempt-1")
	mustAdmit(t, store, "p", "m", "attempt-2")
	mustTokens(t, store, "p", "attempt-1", "tokens-1", 10)

	backend = backend.restart(t)
	restarted := testStore(backend, clock, policy, Options{})
	if exhausted, err := restarted.Exhausted(context.Background(), "p", "m"); !exhausted || err != nil {
		t.Fatalf("restart exhaustion = %v, %v", exhausted, err)
	}
	if _, err := restarted.CheckAndRecord(context.Background(), "p", "m", "new"); !errors.Is(err, ErrExhausted) {
		t.Fatalf("restart admission = %v", err)
	}
	replay := mustAdmit(t, restarted, "p", "m", "attempt-1")
	if !replay.Replay || replay.Day != first.Day || !replay.At.Equal(first.At) {
		t.Fatalf("reconstructed replay = %+v", replay)
	}
	mustTokens(t, restarted, "p", "attempt-1", "tokens-1", 10)
	state, _ := backend.Snapshot(context.Background(), "p")
	if state.DailyRequests[first.Day] != 2 || state.Tokens[first.Day] != 10 || state.Tokens[first.Month] != 10 {
		t.Fatalf("replay changed accounting: %+v", state)
	}
}

func TestUTCWindowsUseAdmissionTimeAfterRollover(t *testing.T) {
	// Local February 1 is still January 31 in UTC.
	zone := time.FixedZone("IST", 5*3600+30*60)
	now := time.Date(2026, 2, 1, 5, 29, 0, 0, zone)
	backend := newSharedBackend()
	policy := Policy{DailyLimit: 1, TokenLimit: 10, TokenWindow: "daily"}
	store := testStore(backend, func() time.Time { return now }, policy, Options{})
	old := mustAdmit(t, store, "p", "m", "old")
	if old.Day != "2026-01-31" || old.Month != "2026-01" || old.TokenWindow != "daily" {
		t.Fatalf("wrong UTC capture: %+v", old)
	}
	now = now.Add(time.Minute)
	mustTokens(t, store, "p", "old", "late-tokens", 10)
	state, _ := backend.Snapshot(context.Background(), "p")
	if state.Tokens["2026-01-31"] != 10 || state.Tokens["2026-01"] != 10 || state.Tokens["2026-02-01"] != 0 || state.Tokens["2026-02"] != 0 {
		t.Fatalf("late tokens charged completion window: %+v", state.Tokens)
	}
	if exhausted, err := store.Exhausted(context.Background(), "p", "m"); exhausted || err != nil {
		t.Fatalf("new UTC day remained exhausted: %v, %v", exhausted, err)
	}
	newReceipt := mustAdmit(t, store, "p", "m", "new")
	mustTokens(t, store, "p", "new", "new-tokens", 10)
	if newReceipt.Day != "2026-02-01" || newReceipt.Month != "2026-02" {
		t.Fatalf("new windows = %+v", newReceipt)
	}
	state, _ = backend.Snapshot(context.Background(), "p")
	if _, oldDay := state.DailyRequests[old.Day]; oldDay {
		t.Fatal("successful mutation retained expired daily counter")
	}
	if exhausted, err := store.Exhausted(context.Background(), "p", "other-model"); !exhausted || err != nil {
		t.Fatalf("provider-wide day limit = %v, %v", exhausted, err)
	}
}

func TestTokenWindowsAndLimitsAreProviderWide(t *testing.T) {
	now := time.Date(2026, 1, 15, 23, 59, 0, 0, time.UTC)
	policy := Policy{TokenLimit: 20, TokenWindow: "monthly"}
	backend := newSharedBackend()
	store := NewStore(backend, func(string) Policy { return policy }, Options{Now: func() time.Time { return now }})
	mustAdmit(t, store, "p", "a", "a")
	mustTokens(t, store, "p", "a", "a-tokens", 20)
	if _, err := store.CheckAndRecord(context.Background(), "p", "b", "b"); !errors.Is(err, ErrExhausted) {
		t.Fatalf("another model escaped token budget: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if exhausted, _ := store.Exhausted(context.Background(), "p", "b"); !exhausted {
		t.Fatal("monthly budget reset at daily rollover")
	}
	policy.TokenWindow = "daily"
	mustAdmit(t, store, "p", "b", "b-new-day")
	mustTokens(t, store, "p", "b-new-day", "b-tokens", 20)
	policy.TokenWindow = "unknown"
	if exhausted, _ := store.Exhausted(context.Background(), "p", "b"); !exhausted {
		t.Fatal("unknown window did not default monthly")
	}
	now = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mustAdmit(t, store, "p", "b", "february")
}

func TestRPMIsSlidingAndModelScopedSnapshotDoesNotMutate(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	store := testStore(backend, func() time.Time { return now }, Policy{RPMLimit: 1}, Options{})
	mustAdmit(t, store, "p", "a", "a")
	mustAdmit(t, store, "p", "b", "b")
	before, _ := backend.Snapshot(context.Background(), "p")
	now = now.Add(time.Minute - time.Nanosecond)
	if exhausted, _ := store.Exhausted(context.Background(), "p", "a"); !exhausted {
		t.Fatal("RPM reset before full sliding minute")
	}
	now = now.Add(time.Nanosecond)
	if exhausted, _ := store.Exhausted(context.Background(), "p", "a"); exhausted {
		t.Fatal("RPM did not reset at exact minute boundary")
	}
	after, _ := backend.Snapshot(context.Background(), "p")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("planner snapshot mutated accounting or expired state")
	}
	mustAdmit(t, store, "p", "a", "next")
	state, _ := backend.Snapshot(context.Background(), "p")
	if len(state.RPM["a"]) != 1 || len(state.RPM["b"]) != 0 {
		t.Fatalf("successful mutation did not purge expired RPM: %+v", state.RPM)
	}
}

func TestExactConcurrentLimitAcrossTwoStores(t *testing.T) {
	for _, policy := range []Policy{{RPMLimit: 17}, {DailyLimit: 17}} {
		t.Run(fmt.Sprintf("rpm=%d,daily=%d", policy.RPMLimit, policy.DailyLimit), func(t *testing.T) {
			backend := newSharedBackend()
			now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
			clock := func() time.Time { return now }
			stores := []*Store{testStore(backend, clock, policy, Options{}), testStore(backend, clock, policy, Options{})}
			var accepted, rejected atomic.Int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < 100; i++ {
				wg.Go(func() {
					<-start
					_, err := stores[i%2].CheckAndRecord(context.Background(), "p", "m", fmt.Sprintf("attempt-%d", i))
					if err == nil {
						accepted.Add(1)
					} else if errors.Is(err, ErrExhausted) {
						rejected.Add(1)
					} else {
						t.Errorf("unexpected command error: %v", err)
					}
				})
			}
			close(start)
			wg.Wait()
			if accepted.Load() != 17 || rejected.Load() != 83 {
				t.Fatalf("combined admission = %d accepted, %d rejected", accepted.Load(), rejected.Load())
			}
			state, _ := backend.Snapshot(context.Background(), "p")
			if state.DailyRequests["2026-11-07"] != 17 || len(state.Attempts) != 100 {
				t.Fatalf("unexpected durable combined accounting: %+v", state)
			}
		})
	}
}

func TestConcurrentDuplicateAndConflictingIdentities(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	store := testStore(backend, func() time.Time { return now }, Policy{DailyLimit: 1}, Options{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if _, err := store.CheckAndRecord(context.Background(), "p", "m", "same"); err != nil {
				t.Errorf("identical admission failed: %v", err)
			}
			if err := store.RecordTokens(context.Background(), "p", "same", "tokens", 7); err != nil {
				t.Errorf("identical tokens failed: %v", err)
			}
		})
	}
	wg.Wait()
	state, _ := backend.Snapshot(context.Background(), "p")
	if state.DailyRequests["2026-11-07"] != 1 || state.Tokens["2026-11-07"] != 7 || state.Tokens["2026-11"] != 7 {
		t.Fatalf("concurrent replay double charged: %+v", state)
	}
	if _, err := store.CheckAndRecord(context.Background(), "p", "different-model", "same"); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("scope conflict = %v", err)
	}
	for _, event := range []TokenEvent{
		{ProviderID: "p", AttemptID: "same", EventID: "tokens", Tokens: 8},
		{ProviderID: "p", AttemptID: "missing", EventID: "tokens", Tokens: 7},
		{ProviderID: "p", AttemptID: "missing", EventID: "unknown-attempt", Tokens: 7},
	} {
		if err := store.RecordTokens(context.Background(), event.ProviderID, event.AttemptID, event.EventID, event.Tokens); !errors.Is(err, ErrIdempotency) {
			t.Fatalf("event conflict %+v = %v", event, err)
		}
	}
	after, _ := backend.Snapshot(context.Background(), "p")
	if !reflect.DeepEqual(state, after) {
		t.Fatal("conflicting identities mutated durable state")
	}
}

func TestDeniedReceiptSurvivesRolloverAndRestart(t *testing.T) {
	now := time.Date(2026, 11, 7, 23, 59, 0, 0, time.UTC)
	backend := newSharedBackend()
	store := testStore(backend, func() time.Time { return now }, Policy{DailyLimit: 1}, Options{})
	mustAdmit(t, store, "p", "m", "allowed")
	command := AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: "denied", At: now}
	denied, err := store.CheckAndRecordCommand(context.Background(), command)
	if !errors.Is(err, ErrExhausted) || denied.Allowed || denied.Exhaustion == nil {
		t.Fatalf("denial receipt = %+v, %v", denied, err)
	}
	originalReset := denied.Exhaustion.ResetAt
	denied.Exhaustion.ResetAt = now.Add(24 * time.Hour)
	detached, _ := backend.Snapshot(context.Background(), "p")
	if !detached.Attempts["denied"].Exhaustion.ResetAt.Equal(originalReset) {
		t.Fatal("returned denied receipt aliased backend state")
	}
	denied.Exhaustion.ResetAt = originalReset
	backend = backend.restart(t)
	now = now.Add(2 * time.Minute)
	restarted := testStore(backend, func() time.Time { return now }, Policy{DailyLimit: 1}, Options{})
	replay, err := restarted.CheckAndRecordCommand(context.Background(), command)
	if !errors.Is(err, ErrExhausted) || replay.Allowed || !replay.Replay || !replay.Exhaustion.ResetAt.Equal(denied.Exhaustion.ResetAt) {
		t.Fatalf("denied replay changed after rollover: %+v, %v", replay, err)
	}
	if err := restarted.RecordTokens(context.Background(), "p", "denied", "denied-tokens", 10); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("denied attempt accepted tokens: %v", err)
	}
	mustAdmit(t, restarted, "p", "m", "fresh-next-day")
	state, _ := backend.Snapshot(context.Background(), "p")
	if state.DailyRequests["2026-11-08"] != 1 || len(state.Attempts) != 3 {
		t.Fatalf("denied replay consumed capacity: %+v", state)
	}
}

func TestFullNormalizedAdmissionPayloadMustMatch(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	command := AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: "a", At: now, Policy: Policy{DailyLimit: 10}}
	state := State{}
	if _, err := Admit(&state, command); err != nil {
		t.Fatal(err)
	}
	before := state.Clone()
	changes := []func(*AdmissionCommand){
		func(c *AdmissionCommand) { c.ProviderID = "different" },
		func(c *AdmissionCommand) { c.ModelID = "different" },
		func(c *AdmissionCommand) { c.At = c.At.Add(time.Nanosecond) },
		func(c *AdmissionCommand) { c.Policy.RPMLimit = 1 },
		func(c *AdmissionCommand) { c.Policy.DailyLimit = 11 },
		func(c *AdmissionCommand) { c.Policy.TokenLimit = 1 },
		func(c *AdmissionCommand) { c.Policy.TokenWindow = "daily" },
	}
	for _, change := range changes {
		changed := command
		change(&changed)
		if _, err := Admit(&state, changed); !errors.Is(err, ErrIdempotency) {
			t.Fatalf("changed payload %+v accepted: %v", changed, err)
		}
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("conflicting payload mutated state")
	}
	// Equivalent instants, disabled negative limits, and implicit monthly windows
	// normalize to the same semantic payload.
	command.At = now.In(time.FixedZone("IST", 19800))
	command.Policy.RPMLimit = -1
	command.Policy.TokenWindow = "monthly"
	if receipt, err := Admit(&state, command); err != nil || !receipt.Replay {
		t.Fatalf("normalized replay rejected: %+v, %v", receipt, err)
	}
}

func TestTokenAccountingPreservesCapturedWindowsAcrossPolicyChange(t *testing.T) {
	now := time.Date(2026, 11, 7, 23, 59, 0, 0, time.UTC)
	policy := Policy{TokenLimit: 10, TokenWindow: "monthly"}
	backend := newSharedBackend()
	store := NewStore(backend, func(string) Policy { return policy }, Options{Now: func() time.Time { return now }})
	receipt := mustAdmit(t, store, "p", "m", "a")
	now = now.Add(2 * time.Minute)
	policy.TokenWindow = "daily"
	mustTokens(t, store, "p", "a", "tokens", 10)
	state, _ := backend.Snapshot(context.Background(), "p")
	if state.Attempts["a"].TokenWindow != "monthly" || state.Tokens[receipt.Day] != 10 || state.Tokens[receipt.Month] != 10 || state.Tokens["2026-11-08"] != 0 {
		t.Fatalf("policy change moved captured accounting: %+v", state)
	}
	if exhausted, _ := store.Exhausted(context.Background(), "p", "m"); exhausted {
		t.Fatal("previous day tokens charged new daily policy")
	}
	policy.TokenWindow = "monthly"
	if exhausted, _ := store.Exhausted(context.Background(), "p", "m"); !exhausted {
		t.Fatal("return to monthly policy forgot observed tokens")
	}
}

func TestOutagePolicyRequiresExplicitAcknowledgement(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		policy  Policy
		options Options
		proceed bool
	}{
		{"default closed", Policy{DailyLimit: 1}, Options{}, false},
		{"unknown closed", Policy{DailyLimit: 1}, Options{FailurePolicy: "unknown", FailOpenAcknowledged: true}, false},
		{"unacknowledged open closed", Policy{DailyLimit: 1}, Options{FailurePolicy: FailOpen}, false},
		{"acknowledged open", Policy{DailyLimit: 1}, Options{FailurePolicy: FailOpen, FailOpenAcknowledged: true}, true},
		{"unlimited best effort", Policy{}, Options{}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			backend := newSharedBackend()
			backend.setOutage(true)
			store := testStore(backend, func() time.Time { return now }, tt.policy, tt.options)
			receipt, err := store.CheckAndRecord(context.Background(), "p", "m", "a")
			if tt.proceed {
				if err != nil || !receipt.BestEffort {
					t.Fatalf("best-effort admission = %+v, %v", receipt, err)
				}
				mustTokens(t, store, "p", "a", "tokens", 5)
				mustTokens(t, store, "p", "a", "tokens", 5)
				replay := mustAdmit(t, store, "p", "m", "a")
				if !replay.Replay || !replay.BestEffort {
					t.Fatalf("best-effort replay = %+v", replay)
				}
			} else {
				var unavailable *UnavailableError
				if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) {
					t.Fatalf("strict rejection = %v", err)
				}
			}
			health := store.Health()
			if health.Ready || !health.Degraded || !health.Stale || health.Reason != "backend_unavailable" {
				t.Fatalf("outage health = %+v", health)
			}
			if (health.Enforcement == "best_effort") != tt.proceed {
				t.Fatalf("outage enforcement = %+v", health)
			}
			if strings.Contains(fmt.Sprint(err), "secret") {
				t.Fatal("backend credentials escaped in domain error")
			}
		})
	}
}

func TestOutagePolicySourceAppliesLiveAcknowledgedChanges(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	backend.setOutage(true)
	policy, acknowledged := FailClosed, false
	store := testStore(backend, func() time.Time { return now }, Policy{DailyLimit: 1}, Options{
		FailurePolicySource: func() (string, bool) { return policy, acknowledged },
	})
	if _, err := store.CheckAndRecord(context.Background(), "p", "m", "closed"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed policy admission = %v", err)
	}
	policy = FailOpen
	if _, err := store.CheckAndRecord(context.Background(), "p", "m", "unacknowledged"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unacknowledged policy admission = %v", err)
	}
	acknowledged = true
	if receipt, err := store.CheckAndRecord(context.Background(), "p", "m", "open"); err != nil || !receipt.BestEffort {
		t.Fatalf("live fail-open admission = %+v, %v", receipt, err)
	}
}

func TestStalePlannerSnapshotCannotAuthorizeStrictAdmission(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	store := testStore(backend, func() time.Time { return now }, Policy{DailyLimit: 2}, Options{})
	mustAdmit(t, store, "p", "m", "a")
	if exhausted, err := store.Exhausted(context.Background(), "p", "m"); exhausted || err != nil {
		t.Fatalf("first snapshot = %v,%v", exhausted, err)
	}
	backend.setOutage(true)
	if exhausted, err := store.Exhausted(context.Background(), "p", "m"); exhausted || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale snapshot = %v,%v", exhausted, err)
	}
	if _, err := store.CheckAndRecord(context.Background(), "p", "m", "b"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale snapshot authorized strict attempt: %v", err)
	}
	backend.setOutage(false)
	mustAdmit(t, store, "p", "m", "b")
	if health := store.Health(); !health.Ready || health.Degraded || health.Enforcement != FailClosed {
		t.Fatalf("recovered strict health = %+v", health)
	}
}

func TestFailedCommitAndCancellationConsumeNoCapacity(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	backend.failWrites = true
	store := testStore(backend, func() time.Time { return now }, Policy{DailyLimit: 1}, Options{})
	if _, err := store.CheckAndRecord(context.Background(), "p", "m", "a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed commit = %v", err)
	}
	backend.failWrites = false
	state, _ := backend.Snapshot(context.Background(), "p")
	if len(state.Attempts) != 0 || len(state.DailyRequests) != 0 {
		t.Fatalf("failed commit spent capacity: %+v", state)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.CheckAndRecord(ctx, "p", "m", "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission = %v", err)
	}
	mustAdmit(t, store, "p", "m", "a")
	backend.failWrites = true
	if err := store.RecordTokens(context.Background(), "p", "a", "tokens", 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed token commit = %v", err)
	}
	backend.failWrites = false
	state, _ = backend.Snapshot(context.Background(), "p")
	if len(state.TokenEvents) != 0 || len(state.Tokens) != 0 {
		t.Fatal("failed token write published accounting")
	}
	mustTokens(t, store, "p", "a", "tokens", 10)
}

func TestDetachedSnapshotAndHealthRedaction(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	store := testStore(backend, func() time.Time { return now }, Policy{}, Options{})
	mustAdmit(t, store, "p", "m", "a")
	state, _ := backend.Snapshot(context.Background(), "p")
	state.RPM["m"][0] = now.Add(time.Hour)
	state.Attempts["a"] = Admission{}
	state.DailyRequests["2026-11-07"] = 100
	fresh, _ := backend.Snapshot(context.Background(), "p")
	if !fresh.RPM["m"][0].Equal(now) || fresh.Attempts["a"].AttemptID != "a" || fresh.DailyRequests["2026-11-07"] != 1 {
		t.Fatal("snapshot aliased backend state")
	}
	backend.unsafeHealth = true
	data, _ := json.Marshal(store.Health())
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "private") || strings.Contains(string(data), "https") {
		t.Fatalf("health exposed backend internals: %s", data)
	}
}

func TestTokenOverflowSaturatesAndPublication(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := newSharedBackend()
	store := testStore(backend, func() time.Time { return now }, Policy{TokenLimit: math.MaxInt64}, Options{})
	mustAdmit(t, store, "p", "m", "a")
	mustAdmit(t, store, "p", "m", "b")
	mustTokens(t, store, "p", "a", "tokens-a", math.MaxInt64)
	mustTokens(t, store, "p", "b", "tokens-b", 1)
	if exhausted, _ := store.Exhausted(context.Background(), "p", "m"); !exhausted {
		t.Fatal("overflow escaped token limit")
	}
	previous := defaultStore.Load()
	t.Cleanup(func() { defaultStore.Store(previous) })
	if err := Publish(nil); err == nil {
		t.Fatal("nil default was accepted")
	}
	if err := Publish(store); err != nil || !HasDefault() || Default() != store {
		t.Fatalf("default publication failed: %v", err)
	}
}
