package zen

import (
	"crypto/tls"

	utls "github.com/refraction-networking/utls"
)

// The opencode free tier is gated on the TLS ClientHello, not on headers or the
// request body. The official CLI is a Bun binary, so its BoringSSL stack emits
// a very specific hello; Go's crypto/tls, Node/OpenSSL, and curl are all
// rejected with a 429 that is disguised as "Free usage limit reached".
//
// This spec reproduces the observed hello exactly. Verified against
// tls.browserleaks.com with the live CLI's fingerprint:
//
//	ja3 1523504b38f0fae0d881d4b6554aac1b
//	ja4 t13d1713h1_5b57614c22b0_7f527383e249
//
// Two properties are load-bearing and easy to break by accident:
//   - no GREASE anywhere, and
//   - ALPN carries http/1.1 only. JA4 encodes the ALPN in its third field
//     ("h1"), so adding h2 changes the fingerprint and the gate rejects it.
//
// Keep the extension order below in sync with the observed order
// 0,23,65281,10,11,35,16,5,13,18,51,45,43.
func BunClientHelloSpec() *utls.ClientHelloSpec {
	return &utls.ClientHelloSpec{
		TLSVersMin:         utls.VersionTLS12,
		TLSVersMax:         utls.VersionTLS13,
		CipherSuites:       bunCipherSuites(),
		CompressionMethods: []byte{0},
		Extensions: []utls.TLSExtension{
			&utls.SNIExtension{},
			&utls.ExtendedMasterSecretExtension{},
			&utls.RenegotiationInfoExtension{Renegotiation: utls.RenegotiateOnceAsClient},
			&utls.SupportedCurvesExtension{Curves: []utls.CurveID{
				utls.X25519MLKEM768,
				utls.X25519,
				utls.CurveP256,
				utls.CurveP384,
			}},
			&utls.SupportedPointsExtension{SupportedPoints: []byte{0}},
			&utls.SessionTicketExtension{},
			&utls.ALPNExtension{AlpnProtocols: []string{"http/1.1"}},
			&utls.StatusRequestExtension{},
			&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []utls.SignatureScheme{
				0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501,
				0x0806, 0x0601, 0x0807, 0x0603, 0x0201,
			}},
			&utls.SCTExtension{},
			// Empty key shares are filled in by uTLS during the handshake, the
			// same way its own Chrome presets do it.
			&utls.KeyShareExtension{KeyShares: []utls.KeyShare{
				{Group: utls.X25519MLKEM768},
				{Group: utls.X25519},
			}},
			&utls.PSKKeyExchangeModesExtension{Modes: []uint8{utls.PskModeDHE}},
			&utls.SupportedVersionsExtension{Versions: []uint16{utls.VersionTLS13, utls.VersionTLS12}},
		},
	}
}

// BunALPN is the only protocol the CLI's hello offers.
var BunALPN = []string{"http/1.1"}

// bunCipherSuites is the CLI's 17-cipher list in wire order.
func bunCipherSuites() []uint16 {
	return []uint16{
		tls.TLS_AES_128_GCM_SHA256,
		tls.TLS_AES_256_GCM_SHA384,
		tls.TLS_CHACHA20_POLY1305_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	}
}
