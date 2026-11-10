package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	auth "routenest/internal/adminauth"
	"routenest/internal/platform"
	storeadminauth "routenest/internal/store/adminauth"
)

const adminStatusFixturePHC = "$argon2id$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

type adminStatusEntry struct {
	info os.FileInfo
	data []byte
}

func adminStatusTree(t *testing.T, dir string) map[string]adminStatusEntry {
	t.Helper()
	entries := map[string]adminStatusEntry{}
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
		value := adminStatusEntry{info: info}
		if info.Mode().IsRegular() {
			value.data, err = os.ReadFile(path)
		}
		entries[path] = value
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func assertAdminStatusUnchanged(t *testing.T, dir string, before map[string]adminStatusEntry) {
	t.Helper()
	after := adminStatusTree(t, dir)
	if len(before) != len(after) {
		t.Fatal("status changed directory entries")
	}
	for path, original := range before {
		current, ok := after[path]
		if !ok || !os.SameFile(original.info, current.info) || original.info.Mode() != current.info.Mode() || !original.info.ModTime().Equal(current.info.ModTime()) || !bytes.Equal(original.data, current.data) {
			t.Fatalf("status changed %s", path)
		}
	}
}

func adminStatusFixture(now time.Time) auth.State {
	state := auth.EmptyState()
	state.Initialized = true
	state.RecoveryCodePHC = adminStatusFixturePHC
	state.Users[auth.BreakGlassID] = auth.User{ID: auth.BreakGlassID, Username: auth.BreakGlassUsername, PasswordPHC: adminStatusFixturePHC, Roles: []auth.Role{auth.RoleBreakGlass, auth.RoleSecurityAdmin}, Revision: 2, CreatedAt: now.Add(-24 * time.Hour)}
	state.Users["disabled"] = auth.User{ID: "disabled", Username: "disabled", PasswordPHC: adminStatusFixturePHC, Roles: []auth.Role{auth.RoleViewer}, Revision: 1, Disabled: true, CreatedAt: now.Add(-24 * time.Hour)}
	digest := func(label string) string {
		hash := sha256.Sum256([]byte(label))
		return hex.EncodeToString(hash[:])
	}
	addSession := func(label, userID string, revision uint64, expires time.Time, deviceID string) {
		hash := digest(label)
		state.Sessions[hash] = auth.Session{TokenHash: hash, CSRFHash: digest("csrf-" + label), UserID: userID, UserRevision: revision, Method: auth.MethodLocal, DeviceID: deviceID, IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: expires}
	}
	addSession("active", auth.BreakGlassID, 2, now.Add(time.Hour), "")
	addSession("expired", auth.BreakGlassID, 2, now.Add(-time.Hour), "")
	addSession("expires-now", auth.BreakGlassID, 2, now, "")
	addSession("stale-user", auth.BreakGlassID, 1, now.Add(time.Hour), "")
	addSession("disabled-user", "disabled", 1, now.Add(time.Hour), "")
	addSession("future-issued", auth.BreakGlassID, 2, now.Add(2*time.Hour), "")
	future := state.Sessions[digest("future-issued")]
	future.IssuedAt = now.Add(time.Hour)
	state.Sessions[future.TokenHash] = future
	for index, kind := range []string{"enabled", "paused", "expired", "expires-now", "pending", "future-activation", "future-use", "future-created"} {
		id := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(index + 1)}, 32))
		device := auth.TrustedDevice{ID: id, UserID: auth.BreakGlassID, UserRevision: 2, SecretHash: digest("device-" + kind), Metadata: auth.TrustedDeviceMetadata{Name: "private-device-" + kind, Platform: "linux"}, CreatedAt: now.Add(-24 * time.Hour), ActivatedAt: now.Add(-23 * time.Hour), ExpiresAt: now.Add(24 * time.Hour), AutomaticSignIn: true}
		switch kind {
		case "paused":
			device.AutomaticSignIn = false
		case "expired":
			device.ExpiresAt = now.Add(-time.Hour)
		case "expires-now":
			device.ExpiresAt = now
		case "pending":
			device.ActivatedAt = time.Time{}
		case "future-activation":
			device.ActivatedAt = now.Add(time.Hour)
		case "future-use":
			device.LastUsedAt = now.Add(time.Hour)
		case "future-created":
			device.CreatedAt = now.Add(time.Hour)
			device.ActivatedAt = now.Add(time.Hour)
		}
		state.TrustedDevices[id] = device
		addSession("device-session-"+kind, auth.BreakGlassID, 2, now.Add(time.Hour), id)
	}
	return state
}

func adminStatusStore(t *testing.T, state auth.State) (string, *storeadminauth.Store) {
	t.Helper()
	dataDir := t.TempDir()
	if err := platform.EnsurePrivateDir(dataDir); err != nil {
		t.Fatal(err)
	}
	store, err := storeadminauth.Open(filepath.Join(dataDir, "admin-auth"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	event := auth.AuditEvent{ID: strings.Repeat("A", 43), Kind: auth.AuditBootstrapComplete, Outcome: "success", At: time.Now().UTC()}
	if err := store.Apply(t.Context(), event, func(next *auth.State) error {
		*next = auth.CloneState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(platform.DataDirEnv, dataDir)
	return dataDir, store
}

func assertAdminStatusRedacted(t *testing.T, output string, state auth.State) {
	t.Helper()
	secrets := []string{adminStatusFixturePHC, "$argon2id$", "tokenHash", "secretHash"}
	for _, session := range state.Sessions {
		secrets = append(secrets, session.TokenHash, session.CSRFHash)
	}
	for _, device := range state.TrustedDevices {
		secrets = append(secrets, device.ID, device.SecretHash, device.Metadata.Name)
	}
	for _, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Fatal("status disclosed credential material or private metadata")
		}
	}
}

func TestAuthStatusReadOnlyBeforeAndDuringRunningStore(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%t", running), func(t *testing.T) {
			state := adminStatusFixture(time.Now().UTC())
			dataDir, store := adminStatusStore(t, state)
			if !running {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := adminStatusTree(t, dataDir)
			for i := 0; i < 2; i++ {
				var output bytes.Buffer
				if handled, err := runAdminAuthCommand([]string{"auth", "status"}, &output); !handled || err != nil {
					t.Fatalf("status handled = %t, error = %v", handled, err)
				}
				for _, expected := range []string{"Administrator authentication: configured\n", "Active sessions: 4\n", "Trusted desktop devices: 1 (enabled, activated, unexpired)\n", "Recovery code: configured\n", "Offline owner recovery: available after stopping every RouteNest process"} {
					if !strings.Contains(output.String(), expected) {
						t.Fatalf("missing %q in status %q", expected, output.String())
					}
				}
				assertAdminStatusRedacted(t, output.String(), state)
			}
			assertAdminStatusUnchanged(t, dataDir, before)
			if running {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := storeadminauth.OpenForRecovery(dataDir)
			if err != nil {
				t.Fatal("status retained a lock", err)
			}
			reopened.Close()
		})
	}
}

func TestAuthStatusExpiryBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	state := adminStatusFixture(now)
	if err := auth.ValidateState(state); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeAdminAuthStateStatus(&output, state, now); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Active sessions: 4\n", "Trusted desktop devices: 1 (enabled, activated, unexpired)\n"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expiry boundary status = %q", output.String())
		}
	}
	if len(state.Sessions) != 14 || len(state.TrustedDevices) != 8 {
		t.Fatal("status pruned persisted sessions or devices")
	}
	assertAdminStatusRedacted(t, output.String(), state)
}

func TestAuthStatusSessionStartAndDeviceActivation(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		issuedAt  time.Time
		activated time.Time
		sessions  int
		devices   int
	}{
		{"issued-and-activated-now", now, now, 1, 1},
		{"future-session", now.Add(time.Nanosecond), now, 0, 1},
		{"future-activation", now, now.Add(time.Nanosecond), 0, 0},
		{"pending-device-existing-session", now, time.Time{}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := adminStatusFixture(now)
			var device auth.TrustedDevice
			for _, candidate := range state.TrustedDevices {
				if candidate.Metadata.Name == "private-device-enabled" {
					device = candidate
				}
			}
			device.ActivatedAt = tc.activated
			state.TrustedDevices = map[string]auth.TrustedDevice{device.ID: device}
			session := auth.Session{TokenHash: strings.Repeat("a", 64), CSRFHash: strings.Repeat("b", 64), UserID: device.UserID, UserRevision: device.UserRevision, Method: auth.MethodLocal, DeviceID: device.ID, IssuedAt: tc.issuedAt, ExpiresAt: now.Add(time.Hour)}
			state.Sessions = map[string]auth.Session{session.TokenHash: session}
			if err := auth.ValidateState(state); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := writeAdminAuthStateStatus(&output, state, now); err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{fmt.Sprintf("Active sessions: %d\n", tc.sessions), fmt.Sprintf("Trusted desktop devices: %d (enabled, activated, unexpired)\n", tc.devices)} {
				if !strings.Contains(output.String(), expected) {
					t.Fatalf("start boundary status = %q", output.String())
				}
			}
			assertAdminStatusRedacted(t, output.String(), state)
		})
	}
}

func TestAuthStatusTrustedDevicesRequireLocalAuthority(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"uninitialized", "recovery", "missing-user", "disabled-user", "stale-revision", "missing-password", "invalid-password", "legacy-password", "federated-user", "user-id-mismatch", "device-id-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			state := adminStatusFixture(now)
			user := state.Users[auth.BreakGlassID]
			switch kind {
			case "uninitialized":
				state.Initialized = false
			case "recovery":
				state.RecoveryPHC = adminStatusFixturePHC
			case "disabled-user":
				user.Disabled = true
			case "stale-revision":
				user.Revision++
			case "missing-password":
				user.PasswordPHC = ""
			case "invalid-password":
				user.PasswordPHC = "invalid"
			case "legacy-password":
				user.Legacy = &auth.LegacyCredential{}
			case "federated-user":
				user.Provider = "oidc"
			case "user-id-mismatch":
				user.ID = "different-user"
			case "device-id-mismatch":
				for id, device := range state.TrustedDevices {
					device.ID = "different-device"
					state.TrustedDevices[id] = device
				}
			}
			state.Users[auth.BreakGlassID] = user
			if kind == "missing-user" {
				delete(state.Users, auth.BreakGlassID)
			}
			var output bytes.Buffer
			if err := writeAdminAuthStateStatus(&output, state, now); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "Trusted desktop devices: 0 (enabled, activated, unexpired)\n") {
				t.Fatalf("unusable local device authority counted: %q", output.String())
			}
			if kind == "uninitialized" || kind == "recovery" {
				if !strings.Contains(output.String(), "Active sessions: 0\n") || !strings.Contains(output.String(), "Recovery code: not configured\n") {
					t.Fatalf("inactive authority reported live sessions or recovery code: %q", output.String())
				}
			}
		})
	}
}

func TestAuthStatusBootstrapAndRecoveryModes(t *testing.T) {
	for _, mode := range []string{"bootstrap", "configured-without-code", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			state := auth.EmptyState()
			status := "bootstrap required"
			recovery := "unavailable before administrator setup"
			if mode == "bootstrap" {
				state.BootstrapPHC = adminStatusFixturePHC
			} else {
				state = adminStatusFixture(time.Now().UTC())
				state.RecoveryCodePHC = ""
				status = "configured"
				recovery = "available after stopping every RouteNest process"
			}
			if mode == "recovery" {
				state.RecoveryPHC = adminStatusFixturePHC
				state.Sessions = map[string]auth.Session{}
				state.TrustedDevices = nil
				user := state.Users[auth.BreakGlassID]
				user.PasswordPHC = ""
				state.Users[auth.BreakGlassID] = user
				status = "offline recovery completion required"
			}
			dataDir, _ := adminStatusStore(t, state)
			before := adminStatusTree(t, dataDir)
			var output bytes.Buffer
			if err := writeAdminAuthStatus(t.Context(), &output); err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"Administrator authentication: " + status + "\n", "Recovery code: not configured\n", "Offline owner recovery: " + recovery} {
				if !strings.Contains(output.String(), expected) {
					t.Fatalf("missing %q in %q", expected, output.String())
				}
			}
			assertAdminStatusRedacted(t, output.String(), state)
			assertAdminStatusUnchanged(t, dataDir, before)
		})
	}
}

func TestAuthStatusMissingStateSafe(t *testing.T) {
	for _, kind := range []string{"fresh-missing-directory", "fresh-empty-directory", "missing-snapshot", "missing-journal", "missing-lock", "missing-both"} {
		t.Run(kind, func(t *testing.T) {
			dataDir := t.TempDir()
			if err := platform.EnsurePrivateDir(dataDir); err != nil {
				t.Fatal(err)
			}
			if kind == "fresh-missing-directory" {
				dataDir = filepath.Join(dataDir, "missing")
			} else if kind != "fresh-empty-directory" {
				var store *storeadminauth.Store
				dataDir, store = adminStatusStore(t, adminStatusFixture(time.Now().UTC()))
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				for label, filename := range map[string]string{"snapshot": storeadminauth.SnapshotName, "journal": storeadminauth.JournalName, "lock": storeadminauth.LockName} {
					if kind == "missing-"+label || kind == "missing-both" && label != "lock" {
						if err := os.Remove(filepath.Join(dataDir, "admin-auth", filename)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			t.Setenv(platform.DataDirEnv, dataDir)
			before := adminStatusTree(t, dataDir)
			var output bytes.Buffer
			err := writeAdminAuthStatus(t.Context(), &output)
			if !errors.Is(err, storeadminauth.ErrMissing) {
				t.Fatal("missing authority not reported", err)
			}
			for _, expected := range []string{"authority missing or incomplete", "fresh installation cannot be confirmed", "Active sessions: unknown", "Recovery code: unknown", "Offline owner recovery: unavailable"} {
				if !strings.Contains(output.String(), expected) {
					t.Fatalf("missing status %q in %q", expected, output.String())
				}
			}
			if strings.Contains(output.String(), "bootstrap required") {
				t.Fatal("missing authority was treated as fresh bootstrap")
			}
			assertAdminStatusUnchanged(t, dataDir, before)
		})
	}
}

func TestAuthStatusPreparedJournalRemainsUnchanged(t *testing.T) {
	for _, point := range []storeadminauth.FaultPoint{storeadminauth.BeforeSnapshotWrite, storeadminauth.AfterSnapshotRename} {
		t.Run(string(point), func(t *testing.T) {
			state := auth.EmptyState()
			state.BootstrapPHC = adminStatusFixturePHC
			dataDir, store := adminStatusStore(t, state)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			faulted, err := storeadminauth.Open(filepath.Join(dataDir, "admin-auth"), storeadminauth.Options{Fault: func(current storeadminauth.FaultPoint) error {
				if current == point {
					return errors.New("injected status fixture failure")
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer faulted.Close()
			event := auth.AuditEvent{ID: strings.Repeat("B", 43), Kind: auth.AuditBootstrapComplete, Outcome: "success", At: time.Now().UTC()}
			err = faulted.Apply(t.Context(), event, func(next *auth.State) error {
				*next = adminStatusFixture(time.Now().UTC())
				return nil
			})
			if !errors.Is(err, auth.ErrMutationPrepared) {
				t.Fatal("fixture was not durably prepared", err)
			}
			for _, running := range []bool{true, false} {
				if !running {
					if err := faulted.Close(); err != nil {
						t.Fatal(err)
					}
				}
				before := adminStatusTree(t, dataDir)
				var output bytes.Buffer
				err := writeAdminAuthStatus(t.Context(), &output)
				if !errors.Is(err, storeadminauth.ErrPrepared) || !strings.Contains(output.String(), "prepared transaction; restart required") {
					t.Fatalf("prepared status = %q, error %v", output.String(), err)
				}
				if strings.Contains(output.String(), "Administrator authentication: configured") || strings.Contains(output.String(), "Recovery code: configured") {
					t.Fatal("prepared authority was implicitly published")
				}
				assertAdminStatusRedacted(t, output.String(), state)
				assertAdminStatusUnchanged(t, dataDir, before)
			}
		})
	}
}

func TestAuthStatusCorruptEvidenceRemainsUnchanged(t *testing.T) {
	for _, filename := range []string{storeadminauth.SnapshotName, storeadminauth.JournalName} {
		t.Run(filename, func(t *testing.T) {
			state := adminStatusFixture(time.Now().UTC())
			dataDir, store := adminStatusStore(t, state)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, "admin-auth", filename), []byte("sensitive-broken-authority\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := adminStatusTree(t, dataDir)
			var output bytes.Buffer
			err := writeAdminAuthStatus(t.Context(), &output)
			if !errors.Is(err, storeadminauth.ErrCorrupt) || !strings.Contains(output.String(), "corrupt authority") || !strings.Contains(output.String(), "Active sessions: unknown") {
				t.Fatalf("corrupt status = %q, error %v", output.String(), err)
			}
			if strings.Contains(output.String()+err.Error(), "sensitive-broken-authority") {
				t.Fatal("status exposed invalid evidence")
			}
			assertAdminStatusRedacted(t, output.String(), state)
			assertAdminStatusUnchanged(t, dataDir, before)
		})
	}
}

func TestAuthStatusCancellation(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(platform.DataDirEnv, dataDir)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	if err := writeAdminAuthStatus(ctx, &output); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatal("canceled status created state", err)
	}
}
