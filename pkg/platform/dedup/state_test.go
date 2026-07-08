package dedup

import (
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func TestParseState(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := ParseState(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("non-map input", func(t *testing.T) {
		result := ParseState("not a map")
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("typed SentTableEntry map (memory store)", func(t *testing.T) {
		now := time.Now()
		input := map[string]middleware.SentTableEntry{
			"table1": {SentAt: now, TokenCount: 100},
			"table2": {SentAt: now.Add(-5 * time.Minute), TokenCount: 200},
		}
		result := ParseState(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if !result["table1"].SentAt.Equal(now) {
			t.Errorf("table1 time mismatch")
		}
		if result["table1"].TokenCount != 100 {
			t.Errorf("table1 token count: got %d, want 100", result["table1"].TokenCount)
		}
		if result["table2"].TokenCount != 200 {
			t.Errorf("table2 token count: got %d, want 200", result["table2"].TokenCount)
		}
	})

	t.Run("new JSON format with object values", func(t *testing.T) {
		now := time.Now().UTC()
		input := map[string]any{
			"table1": map[string]any{
				"sent_at":     now.Format(time.RFC3339Nano),
				"token_count": float64(150),
			},
		}
		result := ParseState(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result["table1"].TokenCount != 150 {
			t.Errorf("token count: got %d, want 150", result["table1"].TokenCount)
		}
	})

	t.Run("old format: map[string]any with time.Time values", func(t *testing.T) {
		now := time.Now()
		input := map[string]any{
			"table1": now,
			"table2": now.Add(-5 * time.Minute),
		}
		result := ParseState(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if !result["table1"].SentAt.Equal(now) {
			t.Errorf("table1 time mismatch")
		}
		if result["table1"].TokenCount != 0 {
			t.Errorf("old format should have TokenCount 0, got %d", result["table1"].TokenCount)
		}
	})

	t.Run("old format: map with RFC3339 string values", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Nanosecond)
		input := map[string]any{
			"table1": now.Format(time.RFC3339Nano),
		}
		result := ParseState(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result["table1"].TokenCount != 0 {
			t.Errorf("old format should have TokenCount 0, got %d", result["table1"].TokenCount)
		}
	})

	t.Run("map with invalid string skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": "not-a-timestamp",
		}
		result := ParseState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for bad timestamp, got %d", len(result))
		}
	})

	t.Run("map with unsupported type skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": 12345,
		}
		result := ParseState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for int value, got %d", len(result))
		}
	})

	t.Run("new format: missing sent_at skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": map[string]any{
				"token_count": float64(100),
			},
		}
		result := ParseState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for missing sent_at, got %d", len(result))
		}
	})

	t.Run("new format: invalid sent_at type skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": map[string]any{
				"sent_at":     12345,
				"token_count": float64(100),
			},
		}
		result := ParseState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for invalid sent_at type, got %d", len(result))
		}
	})
}
