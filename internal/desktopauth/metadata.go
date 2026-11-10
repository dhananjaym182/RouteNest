package desktopauth

import (
	"os"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"routenest/internal/adminauth"
)

func (m *Manager) Metadata() adminauth.TrustedDeviceMetadata {
	hostname, _ := os.Hostname()
	return desktopMetadata(hostname)
}

func desktopMetadata(hostname string) adminauth.TrustedDeviceMetadata {
	name := strings.Map(func(char rune) rune {
		if char == utf8.RuneError || unicode.IsControl(char) || unicode.Is(unicode.Cf, char) ||
			unicode.Is(unicode.Zl, char) || unicode.Is(unicode.Zp, char) {
			return -1
		}
		return char
	}, hostname)
	name = strings.TrimSpace(name)
	if len(name) > 128 {
		name = name[:128]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name = "RouteNest desktop"
	}
	return adminauth.TrustedDeviceMetadata{Name: name, Platform: runtime.GOOS}
}
