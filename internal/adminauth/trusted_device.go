package adminauth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxTrustedDevices = 128
const TrustedDeviceTTL = 90 * 24 * time.Hour
const trustedDeviceSessionTTL = 12 * time.Hour
const trustedDeviceAuthenticationWindow = 5 * time.Minute
const trustedDeviceEnrollmentLifetime = 5 * time.Minute

var (
	ErrTrustedDeviceUntrusted          = fmt.Errorf("trusted desktop device credential is untrusted: %w", ErrUnauthorized)
	ErrTrustedDeviceNotFound           = fmt.Errorf("trusted desktop device credential is not registered: %w", ErrTrustedDeviceUntrusted)
	ErrTrustedDeviceExpired            = fmt.Errorf("trusted desktop device credential expired: %w", ErrUnauthorized)
	ErrTrustedDeviceAutoSignInDisabled = fmt.Errorf("trusted desktop device automatic sign-in is disabled: %w", ErrUnauthorized)
)

type DesktopCredentialStorageStatus struct {
	Available       bool   `json:"available"`
	Stored          bool   `json:"stored"`
	AutomaticSignIn bool   `json:"automaticSignIn"`
	State           string `json:"state"`
}

type TrustedDeviceMetadata struct {
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
}

type TrustedDevice struct {
	ID              string                `json:"id"`
	UserID          string                `json:"userID"`
	UserRevision    uint64                `json:"userRevision"`
	SecretHash      string                `json:"secretHash"`
	Metadata        TrustedDeviceMetadata `json:"metadata"`
	CreatedAt       time.Time             `json:"createdAt"`
	ExpiresAt       time.Time             `json:"expiresAt"`
	LastUsedAt      time.Time             `json:"lastUsedAt"`
	ActivatedAt     time.Time             `json:"activatedAt,omitempty"`
	AutomaticSignIn bool                  `json:"automaticSignIn"`
}

type TrustedDeviceCredential struct {
	ID        string    `json:"id"`
	Secret    string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type TrustedDeviceView struct {
	ID              string                `json:"id"`
	UserID          string                `json:"userID"`
	Metadata        TrustedDeviceMetadata `json:"metadata"`
	CreatedAt       time.Time             `json:"createdAt"`
	ExpiresAt       time.Time             `json:"expiresAt"`
	LastUsedAt      *time.Time            `json:"lastUsedAt,omitempty"`
	AutomaticSignIn bool                  `json:"automaticSignIn"`
	Current         bool                  `json:"current"`
}

func (TrustedDevice) String() string             { return "[redacted trusted desktop device]" }
func (TrustedDeviceMetadata) String() string     { return "[redacted trusted desktop device metadata]" }
func (TrustedDeviceCredential) String() string   { return "[redacted trusted desktop device credential]" }
func (TrustedDeviceView) String() string         { return "[redacted trusted desktop device view]" }
func (d TrustedDevice) GoString() string         { return d.String() }
func (d TrustedDeviceMetadata) GoString() string { return d.String() }
func (d TrustedDeviceCredential) GoString() string {
	return d.String()
}
func (d TrustedDeviceView) GoString() string { return d.String() }

func (d TrustedDevice) Redacted() TrustedDeviceView {
	view := TrustedDeviceView{ID: d.ID, UserID: d.UserID, Metadata: d.Metadata, CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt, AutomaticSignIn: d.AutomaticSignIn}
	if !d.LastUsedAt.IsZero() {
		view.LastUsedAt = &d.LastUsedAt
	}
	return view
}

func validTrustedDeviceToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validTrustedDeviceText(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) || unicode.Is(unicode.Zl, char) || unicode.Is(unicode.Zp, char) {
			return false
		}
	}
	return true
}

func validTrustedDeviceMetadata(metadata TrustedDeviceMetadata) bool {
	return metadata.Name != "" && validTrustedDeviceText(metadata.Name, 128) && validTrustedDeviceText(metadata.Platform, 32)
}

func validSecurityTimestamp(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

func validTrustedDevice(device TrustedDevice) bool {
	if !validTrustedDeviceToken(device.ID) || device.UserID == "" || len(device.UserID) > 128 || device.UserRevision == 0 || !validDigest(device.SecretHash) || !validTrustedDeviceMetadata(device.Metadata) {
		return false
	}
	if !validSecurityTimestamp(device.CreatedAt) || !validSecurityTimestamp(device.ExpiresAt) || !device.ExpiresAt.After(device.CreatedAt) || device.ExpiresAt.Sub(device.CreatedAt) > TrustedDeviceTTL {
		return false
	}
	if !device.ActivatedAt.IsZero() && (!validSecurityTimestamp(device.ActivatedAt) || device.ActivatedAt.Before(device.CreatedAt) || !device.ActivatedAt.Before(device.ExpiresAt)) {
		return false
	}
	return device.LastUsedAt.IsZero() || !device.ActivatedAt.IsZero() && validSecurityTimestamp(device.LastUsedAt) && !device.LastUsedAt.Before(device.ActivatedAt) && device.LastUsedAt.Before(device.ExpiresAt)
}

func trustedDeviceUser(st State, id string, device TrustedDevice) (User, error) {
	if !st.Initialized || st.RecoveryPHC != "" || id != device.ID || !validTrustedDevice(device) {
		return User{}, ErrTrustedDeviceUntrusted
	}
	user, ok := st.Users[device.UserID]
	if !ok || user.ID != device.UserID || user.Disabled || user.Provider != "" || user.PasswordPHC == "" || user.Legacy != nil || user.Revision != device.UserRevision {
		return User{}, ErrTrustedDeviceUntrusted
	}
	if _, err := ParsePHC(user.PasswordPHC); err != nil {
		return User{}, ErrTrustedDeviceUntrusted
	}
	return user, nil
}

func validTrustedDeviceSession(st State, session Session, now time.Time) bool {
	if session.DeviceID == "" {
		return true
	}
	device, ok := st.TrustedDevices[session.DeviceID]
	if !ok || session.Method != MethodLocal || session.Provider != "" || session.UserID != device.UserID || session.UserRevision != device.UserRevision {
		return false
	}
	_, err := trustedDeviceUser(st, session.DeviceID, device)
	return err == nil && !now.Before(device.CreatedAt) && !device.ActivatedAt.After(now) && !device.LastUsedAt.After(now) && device.ExpiresAt.After(now)
}
