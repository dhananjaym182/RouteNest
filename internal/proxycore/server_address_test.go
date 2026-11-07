package proxycore

import "testing"

func TestListenerAddressAndLoopbackIPv6(t *testing.T) {
	if got := listenerAddress("::1", 3457); got != "[::1]:3457" {
		t.Fatalf("IPv6 listener address = %q", got)
	}
	if got := listenerAddress("[::1]", 3457); got != "[::1]:3457" {
		t.Fatalf("bracketed IPv6 listener address = %q", got)
	}
	if !IsLoopbackHost("[::1]") || EffectiveAdminHost("[::1]") != "::1" {
		t.Fatal("bracketed IPv6 loopback normalization failed")
	}
}
