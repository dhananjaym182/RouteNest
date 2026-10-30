package qoder

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Expected values were produced by running the reference JavaScript
// (open-sse/shared/qoder/encoding.js) under node:
//
//	node -e '...qoderEncodeBody(...)...'
//
// and are pinned here so a refactor cannot silently change the wire format.
func TestEncodeBodyKnownValues(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "q$FruHPH"},
		{"hello world", "YuHp$Hq&J(WPHFru"},
		{`{"a":1}`, "ep$$BMn%mYKi"},
		{"The quick brown fox jumps over the lazy dog", "BZCnD,dr#x)QBHCbDf$$BHDbmodsJS.fNYdbJOFYFHiLBZg.PSjUBHKYukJh"},
		// "a" -> base64 "YQ==" (two padding chars) -> "$p$#".
		{"a", "$p$#"},
	}
	for _, tc := range cases {
		if got := encodeBody([]byte(tc.in)); got != tc.want {
			t.Errorf("encodeBody(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodeBodyInvariants(t *testing.T) {
	inputs := []string{"", "a", "ab", "abc", "abcd", "hello world", strings.Repeat("x", 1000)}
	for _, in := range inputs {
		std := base64.StdEncoding.EncodeToString([]byte(in))
		out := encodeBody([]byte(in))
		if len(out) != len(std) {
			t.Fatalf("len(encodeBody(%q)) = %d, want %d", in, len(out), len(std))
		}
		if strings.Count(out, "$") != strings.Count(std, "=") {
			t.Errorf("encodeBody(%q): %d '$' but base64 had %d '='", in, strings.Count(out, "$"), strings.Count(std, "="))
		}
		// Every non-padding character must come from the custom alphabet.
		for i := 0; i < len(out); i++ {
			c := out[i]
			if c == '$' {
				continue
			}
			if !strings.ContainsRune(customAlphabet, rune(c)) {
				t.Errorf("encodeBody(%q): output byte %q is not in the custom alphabet", in, c)
			}
		}
	}
}

// decodeBody reverses encodeBody. It is test-only (the client never decodes),
// and doubles as the round-trip check for the encoder.
func decodeBody(encoded string) ([]byte, error) {
	rev := make(map[byte]byte, len(customAlphabet)+1)
	for i := 0; i < len(customAlphabet); i++ {
		rev[customAlphabet[i]] = stdAlphabet[i]
	}
	rev['$'] = '='

	restored := make([]byte, len(encoded))
	for i := 0; i < len(encoded); i++ {
		if b, ok := rev[encoded[i]]; ok {
			restored[i] = b
		} else {
			restored[i] = encoded[i]
		}
	}

	n := len(restored)
	a := n / 3
	std := make([]byte, 0, n)
	std = append(std, restored[n-a:]...)  // head
	std = append(std, restored[a:n-a]...) // mid
	std = append(std, restored[:a]...)    // tail
	return base64.StdEncoding.DecodeString(string(std))
}

func TestEncodeBodyRoundTrip(t *testing.T) {
	inputs := []string{"", "hello", `{"messages":[{"role":"user","content":"héllo ✓"}]}`}
	for _, in := range inputs {
		got, err := decodeBody(encodeBody([]byte(in)))
		if err != nil {
			t.Fatalf("decodeBody(encodeBody(%q)): %v", in, err)
		}
		if string(got) != in {
			t.Errorf("round trip: got %q, want %q", got, in)
		}
	}
}
