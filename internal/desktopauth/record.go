package desktopauth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"routenest/internal/adminauth"
)

const (
	recordVersion  = 1
	maxRecordBytes = 2048
)

type credentialRecord struct {
	Version         int
	Credential      adminauth.TrustedDeviceCredential
	AutomaticSignIn bool
}

type protectedCredential struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type protectedRecord struct {
	Version         int                 `json:"version"`
	Credential      protectedCredential `json:"credential"`
	AutomaticSignIn bool                `json:"automaticSignIn"`
}

func (credentialRecord) String() string    { return "[redacted desktop credential record]" }
func (credentialRecord) GoString() string  { return "[redacted desktop credential record]" }
func (protectedCredential) String() string { return "[redacted desktop credential]" }
func (protectedCredential) GoString() string {
	return "[redacted desktop credential]"
}
func (protectedRecord) String() string   { return "[redacted desktop credential record]" }
func (protectedRecord) GoString() string { return "[redacted desktop credential record]" }

func validCredential(credential adminauth.TrustedDeviceCredential) bool {
	return validToken(credential.ID) && validToken(credential.Secret) &&
		!credential.ExpiresAt.IsZero() && credential.ExpiresAt.Year() >= 1 && credential.ExpiresAt.Year() <= 9999
}

func validToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}

func encodeRecord(record credentialRecord) (string, error) {
	if record.Version != recordVersion || !validCredential(record.Credential) {
		return "", ErrInvalidCredential
	}
	encoded, err := json.Marshal(protectedRecord{
		Version: record.Version,
		Credential: protectedCredential{
			ID:        record.Credential.ID,
			Secret:    record.Credential.Secret,
			ExpiresAt: record.Credential.ExpiresAt,
		},
		AutomaticSignIn: record.AutomaticSignIn,
	})
	if err != nil || len(encoded) > maxRecordBytes {
		return "", ErrInvalidCredential
	}
	return string(encoded), nil
}

func decodeRecord(value string) (credentialRecord, error) {
	if len(value) == 0 || len(value) > maxRecordBytes || !utf8.ValidString(value) {
		return credentialRecord{}, ErrCorrupt
	}
	fields, err := decodeFields(value, "version", "credential", "automaticSignIn")
	if err != nil {
		return credentialRecord{}, ErrCorrupt
	}
	var record credentialRecord
	if json.Unmarshal(fields["version"], &record.Version) != nil || record.Version != recordVersion ||
		json.Unmarshal(fields["automaticSignIn"], &record.AutomaticSignIn) != nil {
		return credentialRecord{}, ErrCorrupt
	}
	credential, err := decodeFields(string(fields["credential"]), "id", "secret", "expiresAt")
	if err != nil || json.Unmarshal(credential["id"], &record.Credential.ID) != nil ||
		json.Unmarshal(credential["secret"], &record.Credential.Secret) != nil ||
		json.Unmarshal(credential["expiresAt"], &record.Credential.ExpiresAt) != nil || !validCredential(record.Credential) {
		return credentialRecord{}, ErrCorrupt
	}
	return record, nil
}

func decodeFields(value string, names ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrCorrupt
	}
	fields := make(map[string]json.RawMessage, len(names))
	for decoder.More() {
		token, err = decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, ErrCorrupt
		}
		allowed := false
		for _, expected := range names {
			if name == expected {
				allowed = true
				break
			}
		}
		if _, duplicate := fields[name]; !allowed || duplicate {
			return nil, ErrCorrupt
		}
		var field json.RawMessage
		if decoder.Decode(&field) != nil || bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
			return nil, ErrCorrupt
		}
		fields[name] = field
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || len(fields) != len(names) {
		return nil, ErrCorrupt
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, ErrCorrupt
	}
	return fields, nil
}
