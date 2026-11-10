package desktopauth

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestManagerMetadataUsesOnlyHostnameAndOS(t *testing.T) {
	t.Setenv("USER", "not-device-metadata")
	t.Setenv("USERNAME", "not-device-metadata")
	t.Setenv("HOSTNAME", "not-device-metadata")
	t.Setenv("COMPUTERNAME", "not-device-metadata")
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	hostname, _ := os.Hostname()
	metadata := manager.Metadata()
	if metadata != desktopMetadata(hostname) || metadata.Platform != runtime.GOOS {
		t.Fatal("metadata was not derived from the OS hostname and platform")
	}
	if len(keyring.calls) != 0 {
		t.Fatal("metadata unexpectedly accessed credential storage")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["name"] == nil || fields["platform"] == nil || strings.Contains(string(encoded), "not-device-metadata") {
		t.Fatal("metadata contains more than a descriptive name and OS platform")
	}
}

func TestDesktopMetadataSanitizesAndBoundsHostname(t *testing.T) {
	for _, tc := range []struct {
		hostname string
		name     string
	}{
		{"workstation.example", "workstation.example"},
		{" Work Laptop ", "Work Laptop"},
		{"", "RouteNest desktop"},
		{"\x00\n\r\t ", "RouteNest desktop"},
		{"host\x00\n\r\u202ename\u2028\u2029", "hostname"},
		{string([]byte{0xff}), "RouteNest desktop"},
		{strings.Repeat("x", 140), strings.Repeat("x", 128)},
		{strings.Repeat("桌", 44), strings.Repeat("桌", 42)},
	} {
		metadata := desktopMetadata(tc.hostname)
		if metadata.Name != tc.name || metadata.Platform != runtime.GOOS || len(metadata.Name) > 128 || !utf8.ValidString(metadata.Name) {
			t.Fatal("hostname was not sanitized to valid bounded descriptive metadata")
		}
		for _, char := range metadata.Name {
			if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) || unicode.Is(unicode.Zl, char) || unicode.Is(unicode.Zp, char) {
				t.Fatal("hostname contains a control or formatting character")
			}
		}
	}
}
