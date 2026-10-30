package qoder

import (
	"encoding/base64"
	"strings"
)

// Qoder's WAF-bypass body encoding (ported from qoder2api's QoderEncoding.java
// via the CLIProxyAPIPlus qoder-provider branch):
//
//  1. base64-encode the plaintext with the standard alphabet;
//  2. split the result into thirds (a = floor(n/3)) and reorder as
//     tail + mid + head;
//  3. substitute every base64 character through a custom alphabet and map the
//     "=" padding character to "$".
//
// The encoded body must be sent with `&Encode=1` on the URL so the server
// reverses it. The obfuscation keeps Alibaba Cloud WAF from pattern-matching
// the plaintext request body.
const (
	stdAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	customAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
)

// s2c maps a standard-alphabet byte to its custom-alphabet byte. Entries left
// at 0 are "not mapped" (NUL is never a mapped output). "=" is mapped to "$".
var s2c = func() [128]byte {
	var table [128]byte
	for i := 0; i < len(stdAlphabet); i++ {
		table[stdAlphabet[i]] = customAlphabet[i]
	}
	table['='] = '$'
	return table
}()

// encodeBody applies Qoder's body encoding. The result is a byte-for-byte
// (latin1) string: every output character is ASCII, so []byte(result) is
// exactly what goes on the wire and into the COSY signature.
func encodeBody(body []byte) string {
	std := base64.StdEncoding.EncodeToString(body)
	n := len(std)
	a := n / 3
	// [tail][mid][head]
	rearranged := std[n-a:] + std[a:n-a] + std[:a]

	var out strings.Builder
	out.Grow(n)
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		if c < 128 && s2c[c] != 0 {
			out.WriteByte(s2c[c])
			continue
		}
		// Unmapped characters (including "=" handled above) pass through.
		out.WriteByte(c)
	}
	return out.String()
}
