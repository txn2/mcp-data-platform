package dedup

import (
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// ParseState converts a persisted enrichment_dedup state value into the typed
// map the session cache expects. It handles three storage formats:
//   - map[string]middleware.SentTableEntry (memory store preserves Go types directly)
//   - map[string]any with object values (new JSON format: {"sent_at": ..., "token_count": ...})
//   - map[string]any with string/time values (old JSON format: table → timestamp)
func ParseState(raw any) map[string]middleware.SentTableEntry {
	// Memory store: value is already the correct type.
	if typed, ok := raw.(map[string]middleware.SentTableEntry); ok {
		return typed
	}

	// Database store: JSON deserialized as map[string]any.
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]middleware.SentTableEntry, len(m))
	for table, v := range m {
		entry, entryOK := parseEntry(v)
		if entryOK {
			result[table] = entry
		}
	}
	return result
}

// parseEntry parses a single dedup entry from either new or old format.
func parseEntry(v any) (middleware.SentTableEntry, bool) {
	switch t := v.(type) {
	case middleware.SentTableEntry:
		return t, true
	case map[string]any:
		return parseEntryFromMap(t)
	case time.Time:
		return middleware.SentTableEntry{SentAt: t}, true
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return middleware.SentTableEntry{SentAt: parsed}, true
		}
	}
	return middleware.SentTableEntry{}, false
}

// parseEntryFromMap parses a SentTableEntry from a JSON-deserialized map.
func parseEntryFromMap(m map[string]any) (middleware.SentTableEntry, bool) {
	entry := middleware.SentTableEntry{}
	sentAt, ok := m["sent_at"]
	if !ok {
		return entry, false
	}
	switch t := sentAt.(type) {
	case time.Time:
		entry.SentAt = t
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, t)
		if err != nil {
			return entry, false
		}
		entry.SentAt = parsed
	default:
		return entry, false
	}
	if tc, ok := m["token_count"]; ok {
		switch n := tc.(type) {
		case float64:
			entry.TokenCount = int(n)
		case int:
			entry.TokenCount = n
		}
	}
	return entry, true
}
