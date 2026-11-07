package admin

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	AdminCanonicalURLEnv      = "ROUTENEST_ADMIN_CANONICAL_URL"
	AdminProxyModeEnv         = "ROUTENEST_ADMIN_PROXY_MODE"
	AdminTrustedProxyCIDRsEnv = "ROUTENEST_ADMIN_TRUSTED_PROXY_CIDRS"
	AdminHSTSEnv              = "ROUTENEST_ADMIN_HSTS"
)

// SecurityOptionsFromEnvironment overlays hosted-administration settings on
// the listener defaults. Local installations do not need any of these values.
func SecurityOptionsFromEnvironment(base SecurityOptions) (SecurityOptions, error) {
	base.CanonicalURL = strings.TrimSpace(os.Getenv(AdminCanonicalURLEnv))
	base.TrustedProxyCIDRs = splitTrustedProxyCIDRs(os.Getenv(AdminTrustedProxyCIDRsEnv))

	proxyMode, err := optionalBoolEnvironment(AdminProxyModeEnv)
	if err != nil {
		return SecurityOptions{}, err
	}
	base.ProxyMode = proxyMode

	hsts, err := optionalBoolEnvironment(AdminHSTSEnv)
	if err != nil {
		return SecurityOptions{}, err
	}
	base.EnableHSTS = hsts
	return base, nil
}

func optionalBoolEnvironment(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s boolean %q", name, raw)
	}
	return value, nil
}

func splitTrustedProxyCIDRs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
