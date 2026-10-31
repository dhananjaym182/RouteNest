package compaction

import "testing"

func TestContextManagementParse(t *testing.T) {
	raw := []byte(`{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":10},"pause_after_compaction":true},{"type":"clear_tool_uses_20250919","trigger":{"type":"input_tokens","value":1},"keep":{"type":"tool_uses","value":1}},{"type":"clear_thinking_20251015"}]}`)
	cfg, err := parseCMConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || len(cfg.Edits) != 3 {
		t.Fatalf("expected 3 edits, got %v", cfg)
	}
	if cfg.Edits[0].Type != "compact_20260112" || !cfg.Edits[0].PauseAfterCompact {
		t.Fatalf("compact edit parsed wrong: %+v", cfg.Edits[0])
	}
	if cfg.Edits[1].Type != "clear_tool_uses_20250919" {
		t.Fatalf("clear_tool_uses edit parsed wrong: %+v", cfg.Edits[1])
	}
	if cfg.Edits[2].Type != "clear_thinking_20251015" {
		t.Fatalf("clear_thinking edit parsed wrong: %+v", cfg.Edits[2])
	}
}
