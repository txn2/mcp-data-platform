package agentinstructions

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestCheckCustomizedSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"empty is accepted", 0, false},
		{"under the limit is accepted", MaxCustomizedBytes - 1, false},
		{"exactly the limit is accepted", MaxCustomizedBytes, false},
		{"one byte over is refused", MaxCustomizedBytes + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckCustomizedSize(strings.Repeat("x", tt.size))
			if tt.wantErr == (err == nil) {
				t.Fatalf("CheckCustomizedSize(%d bytes) error = %v, wantErr = %v", tt.size, err, tt.wantErr)
			}
		})
	}
}

// The refusal has to be actionable on its own: an agent that only sees this
// string must learn the size, the limit, the overage, and where the content
// belongs instead.
func TestOversizeErrorNamesSizeLimitAndRemedy(t *testing.T) {
	over := 137
	err := CheckCustomizedSize(strings.Repeat("x", MaxCustomizedBytes+over))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	var oversize *OversizeError
	if !errors.As(err, &oversize) {
		t.Fatalf("error is %T, want *OversizeError", err)
	}
	if oversize.Over() != over {
		t.Errorf("Over() = %d, want %d", oversize.Over(), over)
	}
	msg := err.Error()
	for _, want := range []string{
		strconv.Itoa(MaxCustomizedBytes + over),
		strconv.Itoa(over),
		strconv.Itoa(MaxCustomizedBytes),
		"knowledge page",
		"mcp:knowledge_page:<slug>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q: %s", want, msg)
		}
	}
}

func TestCustomizedNotice(t *testing.T) {
	if got := CustomizedNotice(strings.Repeat("x", AdviseCustomizedBytes)); got != "" {
		t.Errorf("at the advisory threshold the notice should be silent, got %q", got)
	}
	got := CustomizedNotice(strings.Repeat("x", AdviseCustomizedBytes+1))
	if got == "" {
		t.Fatal("one byte over the advisory should produce a notice")
	}
	for _, want := range []string{
		strconv.Itoa(AdviseCustomizedBytes + 1),
		strconv.Itoa(AdviseCustomizedBytes),
		strconv.Itoa(MaxCustomizedBytes),
		"knowledge page",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notice does not name %q: %s", want, got)
		}
	}
}

// The advisory must fire well before the refusal, or it is not an early signal.
func TestAdvisoryIsBelowTheLimit(t *testing.T) {
	if AdviseCustomizedBytes >= MaxCustomizedBytes {
		t.Fatalf("advisory %d is not below the limit %d", AdviseCustomizedBytes, MaxCustomizedBytes)
	}
}

func TestIndexEntry(t *testing.T) {
	tests := []struct {
		name  string
		slug  string
		about string
		want  string
	}{
		{
			"reference and one line",
			"opensearch-aggregations",
			"why an aggregation goes through raw_query",
			"- `mcp:knowledge_page:opensearch-aggregations` -- why an aggregation goes through raw_query.",
		},
		{
			"a trailing period is not doubled",
			"a-page",
			"one line.",
			"- `mcp:knowledge_page:a-page` -- one line.",
		},
		{
			"no summary leaves the reference alone",
			"a-page",
			"   ",
			"- `mcp:knowledge_page:a-page`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IndexEntry(tt.slug, tt.about); got != tt.want {
				t.Errorf("IndexEntry() = %q, want %q", got, tt.want)
			}
		})
	}
}
