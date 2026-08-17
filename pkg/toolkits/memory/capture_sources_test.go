package memory

import (
	"testing"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// A capture that names the call it confirms is the agent's own verdict on its
// query: it costs a description, it flips the record to satisfied, and it is
// what makes the statement findable later (#1321).
func TestCaptureRecordsTheCallsItConfirms(t *testing.T) {
	t.Parallel()

	meta := captureMetadata(memstore.SinkSchemaEntity, "dps_abc", nil, map[string]any{
		// A bare id is the receipt a tool result hands back as call_id, so it
		// is accepted and expanded rather than refused.
		memstore.MetaKeySources: []string{"evt-1", "mcp:call:evt-2", " evt-1 ", "", "   "},
	})

	sources, ok := meta[memstore.MetaKeySources].([]string)
	if !ok {
		t.Fatalf("metadata sources = %T, want the normalized list", meta[memstore.MetaKeySources])
	}
	want := []string{"mcp:call:evt-1", "mcp:call:evt-2"}
	if len(sources) != len(want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	for i, ref := range want {
		if sources[i] != ref {
			t.Errorf("sources[%d] = %q, want %q", i, sources[i], ref)
		}
	}
}

func TestCaptureWithoutSourcesRecordsNone(t *testing.T) {
	t.Parallel()

	meta := captureMetadata(memstore.SinkSchemaEntity, "dps_abc", nil, nil)
	if _, present := meta[memstore.MetaKeySources]; present {
		t.Error("a capture that names no call must not carry an empty source list")
	}
}

func TestCaptureSourcesAreBounded(t *testing.T) {
	t.Parallel()

	many := make([]string, maxCaptureSources+5)
	for i := range many {
		many[i] = "evt-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	meta := captureMetadata(memstore.SinkPersonalPreference, "", nil, map[string]any{
		memstore.MetaKeySources: many,
	})

	// A capture states what answered a question; a list longer than this is
	// not a statement.
	sources, ok := meta[memstore.MetaKeySources].([]string)
	if !ok {
		t.Fatalf("metadata sources = %T, want the normalized list", meta[memstore.MetaKeySources])
	}
	if got := len(sources); got != maxCaptureSources {
		t.Errorf("sources = %d, want the cap %d", got, maxCaptureSources)
	}
}

func TestWithSourcesLeavesOtherMetadataAlone(t *testing.T) {
	t.Parallel()

	extra := withSources(memoryCaptureInput{
		Sources:  []string{"evt-1"},
		Metadata: map[string]any{"note": "keep me"},
	})
	if extra["note"] != "keep me" {
		t.Errorf("metadata = %+v, want the caller's own keys kept", extra)
	}
	if withSources(memoryCaptureInput{Metadata: nil}) != nil {
		t.Error("a capture with no sources must not grow a metadata map it did not have")
	}
}

func TestCaptureDropsSourcesThatNameNoCall(t *testing.T) {
	t.Parallel()

	// The catalog matches on the reference form, so a value that normalizes to
	// nothing must leave no key behind rather than a raw list naming no call.
	meta := captureMetadata(memstore.SinkSchemaEntity, "dps_abc", nil, map[string]any{
		memstore.MetaKeySources: []string{"", "   "},
	})
	if _, present := meta[memstore.MetaKeySources]; present {
		t.Errorf("metadata = %+v, want no sources key", meta)
	}
}
