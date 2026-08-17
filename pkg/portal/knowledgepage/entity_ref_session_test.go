package knowledgepage

import (
	"strings"
	"testing"
)

func TestSessionReferenceRoundTrips(t *testing.T) {
	t.Parallel()

	const id = "dps_9f2c1a4b8e7d6c5a"
	if got := SessionRef(id); got != "mcp:session:"+id {
		t.Fatalf("SessionRef = %q", got)
	}
	if SessionRef("") != "" {
		t.Error("a reference to nothing must not be emitted")
	}

	ref, err := ParseEntityRef(SessionRef(id))
	if err != nil {
		t.Fatalf("ParseEntityRef: %v", err)
	}
	if ref.TargetType != RefTargetSession || ref.SessionID != id {
		t.Errorf("parsed = %+v", ref)
	}
	if ref.URN() != SessionRef(id) {
		t.Errorf("URN() = %q, want the reference it was parsed from", ref.URN())
	}
	if ref.identity() != "session:"+id {
		t.Errorf("identity = %q", ref.identity())
	}
}

// A session is read back from the calls one caller made, so a citation on a
// shared page would resolve for that caller alone.
func TestSessionReferenceIsNotCitableOnAPage(t *testing.T) {
	t.Parallel()

	_, err := ParseCitableRef(SessionRef("dps_9f2c"))
	if err == nil {
		t.Fatal("a session reference must be refused on a knowledge page")
	}
	if !strings.Contains(err.Error(), "instead") {
		t.Errorf("the refusal must say what to cite instead, got %q", err)
	}
}
