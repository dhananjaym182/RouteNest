package desktopauth

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"routenest/internal/adminauth"
)

func TestManagerCorruptMaterialIsPreserved(t *testing.T) {
	credential := testCredential(20)
	valid := testRecord(t, credential, true)
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"malformed", `{"version":`},
		{"truncated", valid[:len(valid)-10]},
		{"null", "null"},
		{"array", "[" + valid + "]"},
		{"empty object", "{}"},
		{"oversized", valid + strings.Repeat(" ", maxRecordBytes)},
		{"invalid UTF-8", valid + string([]byte{0xff})},
		{"unsupported version", strings.Replace(valid, `"version":1`, `"version":2`, 1)},
		{"version string", strings.Replace(valid, `"version":1`, `"version":"1"`, 1)},
		{"version null", strings.Replace(valid, `"version":1`, `"version":null`, 1)},
		{"missing version", strings.Replace(valid, `"version":1,`, "", 1)},
		{"duplicate version", strings.Replace(valid, `"version":1`, `"version":2,"version":1`, 1)},
		{"unknown field", strings.Replace(valid, `"version":1`, `"version":1,"extra":"private"`, 1)},
		{"case variant", strings.Replace(valid, `"version":1`, `"Version":1`, 1)},
		{"trailing object", valid + "{}"},
		{"trailing garbage", valid + "invalid"},
		{"missing preference", strings.Replace(valid, `,"automaticSignIn":true`, "", 1)},
		{"null preference", strings.Replace(valid, `"automaticSignIn":true`, `"automaticSignIn":null`, 1)},
		{"string preference", strings.Replace(valid, `"automaticSignIn":true`, `"automaticSignIn":"true"`, 1)},
		{"duplicate preference", strings.Replace(valid, `"automaticSignIn":true`, `"automaticSignIn":false,"automaticSignIn":true`, 1)},
		{"null credential", `{"version":1,"credential":null,"automaticSignIn":true}`},
		{"missing credential", `{"version":1,"automaticSignIn":true}`},
		{"empty credential", `{"version":1,"credential":{},"automaticSignIn":true}`},
		{"credential array", `{"version":1,"credential":[],"automaticSignIn":true}`},
		{"empty ID", strings.Replace(valid, credential.ID, "", 1)},
		{"null ID", strings.Replace(valid, `"id":"`+credential.ID+`"`, `"id":null`, 1)},
		{"unknown credential field", strings.Replace(valid, `"credential":{`, `"credential":{"extra":true,`, 1)},
		{"empty secret", strings.Replace(valid, credential.Secret, "", 1)},
		{"missing secret", strings.Replace(valid, `"secret":"`+credential.Secret+`",`, "", 1)},
		{"null secret", strings.Replace(valid, `"secret":"`+credential.Secret+`"`, `"secret":null`, 1)},
		{"wrong secret type", strings.Replace(valid, `"secret":"`+credential.Secret+`"`, `"secret":32`, 1)},
		{"invalid secret", strings.Replace(valid, credential.Secret, strings.Repeat("!", 43), 1)},
		{"noncanonical secret", strings.Replace(valid, credential.Secret, strings.Repeat("A", 42)+"B", 1)},
		{"duplicate secret", strings.Replace(valid, `"secret":`, `"secret":"bad","secret":`, 1)},
		{"escaped duplicate secret", strings.Replace(valid, `"secret":`, `"\u0073ecret":"bad","secret":`, 1)},
		{"invalid expiry", strings.Replace(valid, credential.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"), "not a date", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring := newFakeKeyring()
			keyring.put(tc.value)
			before := keyring.snapshot()
			manager := NewManagerWithKeyring(keyring)
			assertStatus(t, manager.Inspect(), "corrupt", false)
			got, status, err := manager.Credential()
			if !errors.Is(err, ErrCorrupt) || got != (adminauth.TrustedDeviceCredential{}) {
				t.Fatal("corrupt storage returned a usable credential")
			}
			assertStatus(t, status, "corrupt", false)
			if err := manager.Store(testCredential(21)); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("overwriting corrupt storage = %v", err)
			}
			for _, enabled := range []bool{true, false} {
				if err := manager.SetAutoSignIn(enabled); !errors.Is(err, ErrCorrupt) {
					t.Fatalf("updating corrupt storage = %v", err)
				}
			}
			if keyring.count("set") != 0 || keyring.count("delete") != 0 || !reflect.DeepEqual(before, keyring.snapshot()) {
				t.Fatal("corrupt storage was overwritten or silently deleted")
			}
			assertStatus(t, manager.Status(), "corrupt", false)
		})
	}
}

func TestDecodeRecordAcceptsWhitespaceAndFieldOrder(t *testing.T) {
	credential := testCredential(22)
	value := ` { "automaticSignIn": false, "credential": { "secret": "` + credential.Secret +
		`", "expiresAt": "` + credential.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00") +
		`", "id": "` + credential.ID + `" }, "version": 1 } `
	record, err := decodeRecord(value)
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, record.Credential, credential)
	if record.Version != 1 || record.AutomaticSignIn {
		t.Fatal("decoded record did not preserve its version and preference")
	}
}

func TestEncodeRecordRejectsUnknownVersion(t *testing.T) {
	_, err := encodeRecord(credentialRecord{Version: 2, Credential: testCredential(23)})
	if !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("invalid record version = %v", err)
	}
}

func FuzzDecodeRecord(f *testing.F) {
	valid, err := encodeRecord(credentialRecord{recordVersion, testCredential(24), true})
	if err != nil {
		f.Fatal(err)
	}
	for _, value := range []string{"", "{}", "null", valid, valid + "{}", strings.Repeat("x", maxRecordBytes+1)} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		record, err := decodeRecord(value)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatal("invalid record returned an unsanitized error")
			}
			return
		}
		if record.Version != recordVersion || !validCredential(record.Credential) {
			t.Fatal("decoder accepted an invalid record")
		}
		encoded, err := encodeRecord(record)
		if err != nil {
			t.Fatal("accepted record could not be encoded")
		}
		got, err := decodeRecord(encoded)
		if err != nil {
			t.Fatal("accepted record could not be decoded again")
		}
		assertCredential(t, got.Credential, record.Credential)
		if got.AutomaticSignIn != record.AutomaticSignIn {
			t.Fatal("round trip changed the preference")
		}
	})
}
