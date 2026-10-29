package zen

import "testing"

func TestValidSessionID(t *testing.T) {
	good := []string{
		"ses_e8a360891ffeJRd0yQTEp25RTZ",
		"ses_3f5ca0061ffexsdGgNoW6kdUyV",
	}
	for _, id := range good {
		if !ValidSessionID(id) {
			t.Errorf("%q rejected", id)
		}
	}
	bad := []string{
		"",
		"ses_short",
		"msg_e8a360891ffeJRd0yQTEp25RTZ",
		"ses_E8A360891FFEJRd0yQTEp25RTZ", // uppercase hex is not the CLI shape
		"ses_e8a360891ffeJRd0yQTEp25RT",  // 13 trailing chars
	}
	for _, id := range bad {
		if ValidSessionID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestSessionPoolFiltersAndRotates(t *testing.T) {
	c := NewSessionCache()
	c.SetPool([]string{"ses_aaaaaaaaaaaaAAAAAAAAAAAAAA", "not-a-session", "ses_bbbbbbbbbbbbBBBBBBBBBBBBBB", "ses_aaaaaaaaaaaaAAAAAAAAAAAAAA"})
	if n := c.PoolLen(); n != 2 {
		t.Fatalf("pool = %d, want 2 (invalid and duplicate dropped)", n)
	}

	first := c.SessionID(map[string]any{})
	if !ValidSessionID(first) {
		t.Fatalf("session = %q", first)
	}
	// Same conversation keeps its session for prompt-cache affinity.
	params := map[string]any{"metadata": map[string]any{"session_id": "conv-1"}}
	a := c.SessionID(params)
	b := c.SessionID(params)
	if a != b {
		t.Fatalf("seed affinity lost: %q != %q", a, b)
	}

	// Rotate must move off the current session and reset affinity.
	next, ok := c.Rotate()
	if !ok {
		t.Fatal("rotate reported no pool")
	}
	if next == a {
		t.Fatal("rotate returned the same session")
	}
	if got := c.SessionID(params); got != next {
		t.Fatalf("after rotate session = %q, want %q", got, next)
	}
}

func TestSessionPoolEmptyFallsBack(t *testing.T) {
	c := NewSessionCache()
	if _, ok := c.Rotate(); ok {
		t.Fatal("empty pool must not rotate")
	}
	id := c.SessionID(map[string]any{})
	if !ValidSessionID(id) {
		t.Fatalf("fallback session = %q", id)
	}
	// SetPool also clears any previously minted affinity.
	c.SetPool([]string{"ses_ccccccccccccCCCCCCCCCCCCCC"})
	if got := c.SessionID(map[string]any{"metadata": map[string]any{"session_id": "conv-1"}}); got != "ses_ccccccccccccCCCCCCCCCCCCCC" {
		t.Fatalf("pooled session = %q", got)
	}
}
