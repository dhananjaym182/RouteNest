package routing

import (
	"errors"
	"testing"
)

// Table: which upstream errors count as context-length overflows. Compaction and
// the retry paths branch on this, so the phrases are pinned.
func TestIsContextLengthError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("API 400: context length exceeded"), true},
		{errors.New("This model's maximum context length is 8192"), true},
		{errors.New("prompt exceeds the context window"), true},
		{errors.New("request has too many tokens"), true},
		{errors.New("input exceeds token limit"), true},
		{errors.New("API 500: internal error"), false},
		{errors.New("invalid api key"), false},
	}
	for _, c := range cases {
		if got := IsContextLengthError(c.err); got != c.want {
			t.Errorf("IsContextLengthError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
