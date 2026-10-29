package zen

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

// TestBunClientHelloSpecShape pins the fingerprint that the free-tier gate
// checks. Any drift here silently turns every free-model request into a fake
// "Free usage limit reached".
func TestBunClientHelloSpecShape(t *testing.T) {
	spec := BunClientHelloSpec()
	if len(spec.CipherSuites) != 17 {
		t.Fatalf("cipher suites = %d, want 17", len(spec.CipherSuites))
	}
	if spec.CipherSuites[0] != 0x1301 || spec.CipherSuites[1] != 0x1302 || spec.CipherSuites[2] != 0x1303 {
		t.Fatalf("cipher order starts %x %x %x", spec.CipherSuites[0], spec.CipherSuites[1], spec.CipherSuites[2])
	}
	// No GREASE: every cipher must be a real suite, and no extension may be in
	// the 0x?a?a GREASE range.
	for _, cs := range spec.CipherSuites {
		if cs&0x0f0f == 0x0a0a {
			t.Fatalf("GREASE cipher 0x%04x present", cs)
		}
	}

	wantOrder := []string{
		"*utls.SNIExtension",
		"*utls.ExtendedMasterSecretExtension",
		"*utls.RenegotiationInfoExtension",
		"*utls.SupportedCurvesExtension",
		"*utls.SupportedPointsExtension",
		"*utls.SessionTicketExtension",
		"*utls.ALPNExtension",
		"*utls.StatusRequestExtension",
		"*utls.SignatureAlgorithmsExtension",
		"*utls.SCTExtension",
		"*utls.KeyShareExtension",
		"*utls.PSKKeyExchangeModesExtension",
		"*utls.SupportedVersionsExtension",
	}
	if len(spec.Extensions) != len(wantOrder) {
		t.Fatalf("extensions = %d, want %d", len(spec.Extensions), len(wantOrder))
	}
	for i, ext := range spec.Extensions {
		if got := extName(ext); got != wantOrder[i] {
			t.Fatalf("extension %d = %s, want %s", i, got, wantOrder[i])
		}
	}

	var alpn []string
	var curves []utls.CurveID
	var shares []utls.KeyShare
	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *utls.ALPNExtension:
			alpn = e.AlpnProtocols
		case *utls.SupportedCurvesExtension:
			curves = e.Curves
		case *utls.KeyShareExtension:
			shares = e.KeyShares
		}
	}
	if len(alpn) != 1 || alpn[0] != "http/1.1" {
		t.Fatalf("alpn = %v, want [http/1.1] (JA4 encodes it)", alpn)
	}
	if len(curves) != 4 || curves[0] != utls.X25519MLKEM768 || curves[1] != utls.X25519 {
		t.Fatalf("curves = %v", curves)
	}
	if len(shares) != 2 || shares[0].Group != utls.X25519MLKEM768 || shares[1].Group != utls.X25519 {
		t.Fatalf("key shares = %v", shares)
	}
	// uTLS fills key share data during the handshake; pre-setting it breaks it.
	for _, ks := range shares {
		if len(ks.Data) != 0 {
			t.Fatalf("key share %v must carry no data", ks.Group)
		}
	}
	if len(BunALPN) != 1 || BunALPN[0] != "http/1.1" {
		t.Fatalf("BunALPN = %v", BunALPN)
	}
}

// extName returns the concrete type name so the test can assert extension
// order without depending on an exported extension-id accessor.
func extName(ext utls.TLSExtension) string {
	switch ext.(type) {
	case *utls.SNIExtension:
		return "*utls.SNIExtension"
	case *utls.ExtendedMasterSecretExtension:
		return "*utls.ExtendedMasterSecretExtension"
	case *utls.RenegotiationInfoExtension:
		return "*utls.RenegotiationInfoExtension"
	case *utls.SupportedCurvesExtension:
		return "*utls.SupportedCurvesExtension"
	case *utls.SupportedPointsExtension:
		return "*utls.SupportedPointsExtension"
	case *utls.SessionTicketExtension:
		return "*utls.SessionTicketExtension"
	case *utls.ALPNExtension:
		return "*utls.ALPNExtension"
	case *utls.StatusRequestExtension:
		return "*utls.StatusRequestExtension"
	case *utls.SignatureAlgorithmsExtension:
		return "*utls.SignatureAlgorithmsExtension"
	case *utls.SCTExtension:
		return "*utls.SCTExtension"
	case *utls.KeyShareExtension:
		return "*utls.KeyShareExtension"
	case *utls.PSKKeyExchangeModesExtension:
		return "*utls.PSKKeyExchangeModesExtension"
	case *utls.SupportedVersionsExtension:
		return "*utls.SupportedVersionsExtension"
	default:
		return "other"
	}
}
