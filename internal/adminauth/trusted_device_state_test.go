package adminauth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTrustedDeviceStateValidationAndCloning(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	baseline := CloneState(repo.state)
	if err := ValidateState(baseline); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*State)
	}{
		{"invalid ID", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			delete(st.TrustedDevices, credential.ID)
			d.ID = "invalid"
			st.TrustedDevices[d.ID] = d
		}},
		{"noncanonical ID", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			delete(st.TrustedDevices, credential.ID)
			d.ID = strings.Repeat("B", 43)
			st.TrustedDevices[d.ID] = d
		}},
		{"key mismatch", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			delete(st.TrustedDevices, credential.ID)
			st.TrustedDevices[strings.Repeat("A", 43)] = d
		}},
		{"invalid hash", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.SecretHash = credential.Secret
			st.TrustedDevices[credential.ID] = d
		}},
		{"unknown user", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.UserID = "missing"
			st.TrustedDevices[credential.ID] = d
		}},
		{"zero user revision", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.UserRevision = 0
			st.TrustedDevices[credential.ID] = d
		}},
		{"future user revision", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.UserRevision++
			st.TrustedDevices[credential.ID] = d
		}},
		{"stale user revision", func(st *State) { u := st.Users[BreakGlassID]; u.Revision++; st.Users[BreakGlassID] = u }},
		{"empty metadata", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.Metadata = TrustedDeviceMetadata{}
			st.TrustedDevices[credential.ID] = d
		}},
		{"oversized name", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.Metadata.Name = strings.Repeat("a", 129)
			st.TrustedDevices[credential.ID] = d
		}},
		{"oversized platform", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.Metadata.Platform = strings.Repeat("a", 33)
			st.TrustedDevices[credential.ID] = d
		}},
		{"control metadata", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.Metadata.Name = "bad\x00name"
			st.TrustedDevices[credential.ID] = d
		}},
		{"invalid UTF-8", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.Metadata.Name = string([]byte{0xff})
			st.TrustedDevices[credential.ID] = d
		}},
		{"zero creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.CreatedAt = time.Time{}
			st.TrustedDevices[credential.ID] = d
		}},
		{"zero expiry", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ExpiresAt = time.Time{}
			st.TrustedDevices[credential.ID] = d
		}},
		{"expiry before creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ExpiresAt = d.CreatedAt
			st.TrustedDevices[credential.ID] = d
		}},
		{"overlong lifetime", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ExpiresAt = d.CreatedAt.Add(TrustedDeviceTTL + time.Second)
			st.TrustedDevices[credential.ID] = d
		}},
		{"activation before creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ActivatedAt = d.CreatedAt.Add(-time.Second)
			st.TrustedDevices[credential.ID] = d
		}},
		{"activation at expiry", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ActivatedAt = d.ExpiresAt
			st.TrustedDevices[credential.ID] = d
		}},
		{"last use without activation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ActivatedAt = time.Time{}
			d.LastUsedAt = d.CreatedAt
			st.TrustedDevices[credential.ID] = d
		}},
		{"last use before activation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ActivatedAt = d.CreatedAt.Add(time.Minute)
			d.LastUsedAt = d.CreatedAt
			st.TrustedDevices[credential.ID] = d
		}},
		{"last use before creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.LastUsedAt = d.CreatedAt.Add(-time.Second)
			st.TrustedDevices[credential.ID] = d
		}},
		{"last use at expiry", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.LastUsedAt = d.ExpiresAt
			st.TrustedDevices[credential.ID] = d
		}},
		{"missing session device", func(st *State) {
			session := st.Sessions[admin.Principal.SessionHash]
			session.DeviceID = strings.Repeat("A", 43)
			st.Sessions[session.TokenHash] = session
		}},
		{"stale session revision", func(st *State) {
			u := st.Users[BreakGlassID]
			u.Revision++
			st.Users[BreakGlassID] = u
			d := st.TrustedDevices[credential.ID]
			d.UserRevision++
			st.TrustedDevices[credential.ID] = d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := CloneState(baseline)
			tc.change(&state)
			if err := ValidateState(state); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("invalid device state accepted: %v", err)
			}
		})
	}
	clone := CloneState(baseline)
	device := clone.TrustedDevices[credential.ID]
	device.Metadata.Name = "changed"
	clone.TrustedDevices[credential.ID] = device
	delete(clone.TrustedDevices, credential.ID)
	if len(baseline.TrustedDevices) != 1 || baseline.TrustedDevices[credential.ID].Metadata.Name == "changed" {
		t.Fatal("trusted device cloning aliases persisted state")
	}
	empty := EmptyState()
	if empty.TrustedDevices == nil || CloneState(State{}).TrustedDevices == nil {
		t.Fatal("trusted device map was not initialized safely")
	}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "trustedDevices") {
		t.Fatal("empty trusted devices changed legacy state hash representation")
	}
	var old State
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	if old.TrustedDevices != nil || ValidateState(old) != nil || CloneState(old).TrustedDevices == nil {
		t.Fatal("legacy state without trusted devices is not compatible")
	}
}

func TestTrustedDeviceMetadataValidation(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	for _, metadata := range []TrustedDeviceMetadata{
		{},
		{Name: " "},
		{Name: " padded"},
		{Name: "padded "},
		{Name: "bad\nname"},
		{Name: "bad\u202ename"},
		{Name: "bad\u2028name"},
		{Name: strings.Repeat("a", 129)},
		{Name: string([]byte{0xff})},
		{Name: "valid", Platform: strings.Repeat("a", 33)},
		{Name: "valid", Platform: "linux\r"},
	} {
		if got, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, metadata, ""); !errors.Is(err, ErrInvalidState) || got.Secret != "" {
			t.Fatal("invalid metadata enrolled a device", err)
		}
	}
	if len(repo.state.TrustedDevices) != 0 {
		t.Fatal("invalid metadata changed persisted state")
	}
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "工作站 🔒", Platform: "linux"}, ""); err != nil {
		t.Fatal("valid Unicode device name rejected", err)
	}
}

func TestTrustedDeviceCapacityAndExpiryPruning(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	manager := devicePasswordLogin(t, service)
	base := repo.state.TrustedDevices[first.ID]
	for i := 0; len(repo.state.TrustedDevices) < MaxTrustedDevices; i++ {
		device := base
		device.ID = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%032d", i)))
		repo.state.TrustedDevices[device.ID] = device
	}
	if err := ValidateState(repo.state); err != nil {
		t.Fatal("maximum device count rejected", err)
	}
	before := CloneState(repo.state)
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), manager.Principal, base.Metadata, ""); !errors.Is(err, ErrRateLimited) {
		t.Fatal("device capacity was not enforced", err)
	}
	if !equalTrustedDevices(repo.state.TrustedDevices, before.TrustedDevices) {
		t.Fatal("capacity denial evicted trusted devices")
	}
	replacement, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), manager.Principal, base.Metadata, first.ID)
	if err != nil || len(repo.state.TrustedDevices) != MaxTrustedDevices {
		t.Fatal("atomic replacement at capacity failed", err)
	}
	tooMany := CloneState(repo.state)
	extra := base
	extra.ID = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{255}, 32))
	tooMany.TrustedDevices[extra.ID] = extra
	if err := ValidateState(tooMany); !errors.Is(err, ErrInvalidState) {
		t.Fatal("over-capacity state accepted", err)
	}
	*now = replacement.ExpiresAt
	fresh := devicePasswordLogin(t, service)
	newDevice := enrollDevice(t, service, fresh.Principal)
	if len(repo.state.TrustedDevices) != 1 || repo.state.TrustedDevices[newDevice.ID].ID != newDevice.ID {
		t.Fatal("expired devices were not pruned during enrollment")
	}
	if _, err := service.Authenticate(t.Context(), manager.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired device session survived pruning", err)
	}
}

func TestTrustedDeviceGenericMutationsCannotReplaceProof(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	for _, action := range []string{"verifier", "delete", "expiry", "metadata"} {
		err := service.Mutate(t.Context(), principal, AuditUserChange, principal.UserID, func(state *State) error {
			device := state.TrustedDevices[credential.ID]
			switch action {
			case "verifier":
				device.SecretHash = hashToken("attacker-supplied-credential")
			case "delete":
				delete(state.TrustedDevices, credential.ID)
				return nil
			case "expiry":
				device.ExpiresAt = device.ExpiresAt.Add(-time.Hour)
			case "metadata":
				device.Metadata.Name = "changed"
			}
			state.TrustedDevices[credential.ID] = device
			return nil
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("generic %s mutation bypassed device authentication: %v", action, err)
		}
	}
	if err := service.Mutate(t.Context(), principal, AuditUserChange, principal.UserID, func(state *State) error {
		session := state.Sessions[principal.SessionHash]
		session.DeviceID = ""
		state.Sessions[principal.SessionHash] = session
		return nil
	}); !errors.Is(err, ErrForbidden) {
		t.Fatal("generic mutation detached device proof from a session", err)
	}
	if len(repo.state.TrustedDevices) != 1 || repo.state.TrustedDevices[credential.ID].SecretHash != hashToken(credential.Secret) {
		t.Fatal("generic mutation altered trusted device proof")
	}
}

func TestTrustedDevicePersistenceRoundTripAndRestart(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	*now = now.Add(time.Minute)
	session, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.30")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(repo.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{credential.Secret, session.Token, session.CSRFToken, "permanent admin password"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("plaintext secret persisted")
		}
	}
	if !strings.Contains(string(encoded), `"secretHash":"`+hashToken(credential.Secret)+`"`) || !strings.Contains(string(encoded), `"trustedDevices"`) {
		t.Fatal("device persistence did not contain a SHA-256 verifier")
	}
	var restored State
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if err := ValidateState(restored); err != nil {
		t.Fatal(err)
	}
	if !equalTrustedDevices(restored.TrustedDevices, repo.state.TrustedDevices) {
		t.Fatal("device persistence lost verifier or timestamps")
	}
	newRepo := &memoryRepo{state: restored}
	restarted, err := NewService(newRepo, Options{PasswordParams: service.hasher.params, Now: service.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Authenticate(t.Context(), session.Token); err != nil {
		t.Fatal("device session lost after restart", err)
	}
	*now = now.Add(time.Hour)
	if _, err := restarted.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.30"); err != nil {
		t.Fatal("device proof lost after restart", err)
	}
	if !newRepo.state.TrustedDevices[credential.ID].LastUsedAt.Equal(*now) {
		t.Fatal("restarted service failed to persist last use")
	}
}
