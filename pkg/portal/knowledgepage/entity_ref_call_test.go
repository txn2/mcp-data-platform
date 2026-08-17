package knowledgepage

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// The reference a call hands back and the reference the catalog resolves must
// be the same string. The middleware stamps it on a result (#1320) and this
// package owns the grammar (#1321); if the two ever disagreed, every citation
// would resolve to nothing.
func TestCallReferenceMatchesWhatACallHandsBack(t *testing.T) {
	t.Parallel()

	if CallReferencePrefix != middleware.CallReferenceScheme {
		t.Fatalf("prefix = %q, but a call result stamps %q",
			CallReferencePrefix, middleware.CallReferenceScheme)
	}
	if got := CallRef("evt-1"); got != middleware.CallReferenceScheme+"evt-1" {
		t.Errorf("CallRef = %q", got)
	}
	if CallRef("") != "" {
		t.Error("a reference to nothing must not be emitted")
	}
}

func TestCallReferenceRoundTrips(t *testing.T) {
	t.Parallel()

	ref, err := ParseEntityRef(CallRef("evt-1"))
	if err != nil {
		t.Fatalf("ParseEntityRef: %v", err)
	}
	if ref.TargetType != RefTargetCall || ref.CallID != "evt-1" {
		t.Errorf("parsed = %+v", ref)
	}
	if ref.URN() != CallRef("evt-1") {
		t.Errorf("URN() = %q, want the reference it was parsed from", ref.URN())
	}
	if ref.identity() != "call:evt-1" {
		t.Errorf("identity = %q", ref.identity())
	}
}

// A recorded call resolves only for the caller who made it, so a citation on a
// shared page would be broken for every other reader.
func TestCallReferenceIsNotCitableOnAPage(t *testing.T) {
	t.Parallel()

	_, err := ParseCitableRef(CallRef("evt-1"))
	if err == nil {
		t.Fatal("a call reference must be refused on a knowledge page")
	}
	if !strings.Contains(err.Error(), "promote") {
		t.Errorf("the refusal must say what to do instead, got %q", err)
	}
}
