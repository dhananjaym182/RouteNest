package admin

import (
	"reflect"
	"strings"
	"testing"
)

func TestSecurityOptionsFromEnvironment(t *testing.T) {
	t.Setenv(AdminCanonicalURLEnv, " https://admin.example.test ")
	t.Setenv(AdminProxyModeEnv, "true")
	t.Setenv(AdminTrustedProxyCIDRsEnv, "10.0.0.0/8, 192.0.2.8/32;2001:db8::/32")
	t.Setenv(AdminHSTSEnv, "1")

	options, err := SecurityOptionsFromEnvironment(SecurityOptions{ListenHost: "0.0.0.0", ListenPort: 3457})
	if err != nil {
		t.Fatal(err)
	}
	if options.CanonicalURL != "https://admin.example.test" || !options.ProxyMode || !options.EnableHSTS {
		t.Fatalf("security environment = %+v", options)
	}
	want := []string{"10.0.0.0/8", "192.0.2.8/32", "2001:db8::/32"}
	if !reflect.DeepEqual(options.TrustedProxyCIDRs, want) {
		t.Fatalf("trusted proxy CIDRs = %v, want %v", options.TrustedProxyCIDRs, want)
	}
}

func TestSecurityOptionsFromEnvironmentRejectsInvalidBoolean(t *testing.T) {
	t.Setenv(AdminProxyModeEnv, "sometimes")
	if _, err := SecurityOptionsFromEnvironment(SecurityOptions{}); err == nil {
		t.Fatal("invalid proxy-mode boolean accepted")
	}
}

func TestRequestSecurityOptionsRequiresCompleteHostedConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		options SecurityOptions
		want    string
	}{
		{
			name:    "non-loopback without proxy mode",
			options: SecurityOptions{ListenHost: "0.0.0.0", ListenPort: 3457},
			want:    "non-loopback administration",
		},
		{
			name:    "proxy mode without CIDR",
			options: SecurityOptions{ListenHost: "127.0.0.1", ListenPort: 3457, ProxyMode: true, CanonicalURL: "https://admin.example.test"},
			want:    "at least one trusted proxy CIDR",
		},
		{
			name:    "proxy mode without canonical URL",
			options: SecurityOptions{ListenHost: "127.0.0.1", ListenPort: 3457, ProxyMode: true, TrustedProxyCIDRs: []string{"127.0.0.1/32"}},
			want:    "explicit canonical administrator URL",
		},
		{
			name:    "HSTS over HTTP",
			options: SecurityOptions{ListenHost: "127.0.0.1", ListenPort: 3457, CanonicalURL: "http://127.0.0.1:3457", EnableHSTS: true},
			want:    "HSTS requires an HTTPS canonical URL",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := requestSecurityOptions(test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("requestSecurityOptions() error = %v, want %q", err, test.want)
			}
		})
	}

	if _, err := requestSecurityOptions(SecurityOptions{
		ListenHost:        "0.0.0.0",
		ListenPort:        3457,
		CanonicalURL:      "https://admin.example.test",
		ProxyMode:         true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"},
		EnableHSTS:        true,
	}); err != nil {
		t.Fatalf("complete hosted configuration rejected: %v", err)
	}
}
