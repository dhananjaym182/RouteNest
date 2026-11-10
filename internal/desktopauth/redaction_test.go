package desktopauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	oskeyring "github.com/zalando/go-keyring"
)

func assertRedacted(t *testing.T, secret string, values ...any) {
	t.Helper()
	for _, value := range values {
		for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatal("formatting exposed credential material")
			}
		}
		var output bytes.Buffer
		logger := log.New(&output, "", 0)
		logger.Printf("value=%+v", value)
		if strings.Contains(output.String(), secret) {
			t.Fatal("logging exposed credential material")
		}
	}
}

func TestManagerRedactsSecretsOutsideProtectedRecord(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	credential := testCredential(40)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	got, status, err := manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{manager, status, got} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), credential.Secret) {
			t.Fatal("JSON outside the protected keyring record exposed the secret")
		}
	}
	record := credentialRecord{recordVersion, credential, true}
	protected := protectedCredential{credential.ID, credential.Secret, credential.ExpiresAt}
	assertRedacted(t, credential.Secret, manager, status, got, record, protected, protectedRecord{recordVersion, protected, true})
}

func TestManagerRedactsKeyringAndDecodeErrors(t *testing.T) {
	credential := testCredential(41)
	secret := credential.Secret
	for _, operation := range []string{"get", "set", "delete", "readback", "decode", "missing"} {
		t.Run(operation, func(t *testing.T) {
			keyring := newFakeKeyring()
			keyring.put(testRecord(t, credential, true))
			manager := NewManagerWithKeyring(keyring)
			unsafeError := errors.New("keyring exposed " + secret)
			var err error
			switch operation {
			case "get":
				keyring.getErr = unsafeError
				_, _, err = manager.Credential()
			case "set":
				keyring.setErr = unsafeError
				err = manager.Store(credential)
			case "delete":
				keyring.deleteErr = unsafeError
				err = manager.Remove()
			case "readback":
				keyring.afterSetGetErr = unsafeError
				err = manager.Store(credential)
			case "decode":
				keyring.put(`{"` + secret + `":"invalid"}`)
				_, _, err = manager.Credential()
			case "missing":
				keyring.getErr = fmt.Errorf("%s: %w", secret, oskeyring.ErrNotFound)
				_, _, err = manager.Credential()
			}
			if err == nil {
				t.Fatal("fixture did not produce a failure")
			}
			if errors.Is(err, unsafeError) || errors.Unwrap(err) != nil {
				t.Fatal("error retained a potentially secret-bearing backend cause")
			}
			assertRedacted(t, secret, err, manager, manager.Status())
			encoded, marshalErr := json.Marshal(manager.Status())
			if marshalErr != nil || strings.Contains(string(encoded), secret) {
				t.Fatal("failed-operation JSON status exposed secret material")
			}
		})
	}
	invalid := credential
	invalid.Secret = "invalid-" + secret
	assertRedacted(t, secret, NewManagerWithKeyring(newFakeKeyring()).Store(invalid))
}
