package pkplant

// The refusals. Planting is mostly I/O, but the checks that run before any
// of it are what keep an unaudited or malformed treatment from reaching an
// agent, and the surfacing predicate is what keeps a stored-but-invisible
// belief from being counted as delivered.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
)

// neutral is the seed most cells deliver.
func neutral() pkseed.Seed {
	for _, s := range pkseed.Seeds() {
		if s.BeliefID == "perishable-absent" && !s.Phrasing.Dated && !s.Phrasing.Suppressive && !s.Phrasing.Affordance {
			return s
		}
	}
	panic("pkplant: the neutral seed is missing")
}

func TestCheckDeliverableRefusals(t *testing.T) {
	good := Request{Seed: neutral(), Seq: 1}
	if err := checkDeliverable(good, pkseed.Delivered(good.Seed, good.Metadata)); err != nil {
		t.Fatalf("a well-formed plant was refused: %v", err)
	}

	if err := checkDeliverable(Request{Seed: neutral(), Seq: 0}, "text"); err == nil {
		t.Error("accepted a plant with no identity")
	}
	if err := checkDeliverable(Request{Seed: neutral(), Seq: 1}, "   "); err == nil {
		t.Error("accepted a plant with no text")
	}
	// A malformed metadata block is refused before it reaches an agent: an
	// estimator that is wrong rather than imprecise would lead a correctly
	// reasoning agent to the wrong answer.
	backwards := Request{Seed: neutral(), Seq: 1, Metadata: pkseed.Metadata{
		Enriched: true, AsOf: pkseed.CaptureDate(), Now: pkseed.CaptureDate().AddDate(0, 0, -5), RecheckCalls: 1,
	}}
	if err := checkDeliverable(backwards, "text"); err == nil {
		t.Error("accepted a block whose age runs backwards")
	}
	// The run-time gate is the same rule the build gates on: a string that
	// commands the measured action, or claims the state is settled, never
	// reaches an agent even if it somehow reached this call.
	for _, bad := range []string{
		"The account has no monitors. Verify this before answering.",
		"The account will never have monitors.",
	} {
		err := checkDeliverable(Request{Seed: neutral(), Seq: 1}, bad)
		if err == nil {
			t.Errorf("accepted a string violating the delivery invariants: %q", bad)
		} else if !strings.Contains(err.Error(), "delivery invariants") {
			t.Errorf("refused %q for the wrong reason: %v", bad, err)
		}
	}
}

func TestMentionsRequiresRealOverlap(t *testing.T) {
	asserts := neutral().Asserts
	if !mentions(neutral().Text, asserts) {
		t.Error("a result carrying the belief was not recognized")
	}
	if mentions("nothing to do with any of this", asserts) {
		t.Error("an unrelated result was counted as carrying the belief")
	}
	// A single coincidental word is not a surfacing. "monitors" alone
	// appears in plenty of unrelated text.
	if mentions("the monitors are fine", asserts) {
		t.Error("one coincidental word counted as carrying the belief")
	}
	// Short words never count, so common filler cannot accumulate hits.
	if mentions("the has so no a of", "the has so no a of") {
		t.Error("filler words accumulated into a match")
	}
}

// TestPlantedTextIsTheAuditedText checks the two arms deliver what the
// audit signed off, so what an agent reads is what a reviewer read.
func TestPlantedTextIsTheAuditedText(t *testing.T) {
	s := neutral()
	bare := pkseed.Delivered(s, pkseed.Metadata{})
	if bare != s.Text {
		t.Error("the bare arm alters the audited prose")
	}
	enriched := pkseed.Delivered(s, pkseed.Metadata{
		Enriched: true, AsOf: pkseed.CaptureDate(),
		Now: pkseed.CaptureDate().AddDate(0, 0, 24), RecheckCalls: 1,
	})
	if !strings.HasPrefix(enriched, s.Text) {
		t.Error("the enriched arm alters the prose rather than appending to it")
	}
	if found := pkseed.AuditDelivered(enriched); len(found) > 0 {
		t.Errorf("the enriched delivery violates the invariants: %v", found)
	}
}

// fakeSession records the calls a plant makes and answers them from a
// script, so the orchestration is exercisable without a live stack.
type fakeSession struct {
	captureBody string
	captureErr  bool
	searchBody  string
	searchErr   bool
	calls       []string
	closed      bool
}

func (f *fakeSession) Call(_ context.Context, tool string, _ map[string]any) (string, bool, error) {
	f.calls = append(f.calls, tool)
	if tool == captureTool {
		return f.captureBody, f.captureErr, nil
	}
	return f.searchBody, f.searchErr, nil
}

func (f *fakeSession) Close() error { f.closed = true; return nil }

// fakeReader answers the read-back with a fixed set of stored notes.
type fakeReader struct {
	stored []string
	err    error
}

func (f *fakeReader) ListInsights(context.Context, lifecycleapi.InsightFilter) ([]lifecycleapi.Insight, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]lifecycleapi.Insight, 0, len(f.stored))
	for _, s := range f.stored {
		out = append(out, lifecycleapi.Insight{ID: "ins-1", InsightText: s})
	}
	return out, nil
}

// planter wires a Client over a fake session and reader.
func planter(sess *fakeSession, rd *fakeReader) *Client {
	return &Client{
		dial:     func(context.Context, int) (session, error) { return sess, nil },
		insights: rd,
	}
}

// TestPlantSucceedsWhenStoredAndReachable is the happy path: the note is
// captured, read back byte for byte, and comes back from the question the
// cell will ask.
func TestPlantSucceedsWhenStoredAndReachable(t *testing.T) {
	s := neutral()
	text := pkseed.Delivered(s, pkseed.Metadata{})
	sess := &fakeSession{captureBody: `{"id":"ins-1"}`, searchBody: text}
	c := planter(sess, &fakeReader{stored: []string{text}})

	got, err := c.Plant(context.Background(), Request{Seed: s, Seq: 1, Probe: "what about monitors?"})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got.InsightID != "ins-1" || got.Text != text || !got.Probed {
		t.Errorf("plant returned %+v", got)
	}
	if len(sess.calls) != 2 || sess.calls[0] != captureTool || sess.calls[1] != searchTool {
		t.Errorf("plant made calls %v, want capture then search", sess.calls)
	}
	if !sess.closed {
		t.Error("the session was left open")
	}
}

// TestPlantRefusesWhatTheAgentWillNotReceive covers every way a plant can
// look successful while leaving the cell without its belief. Each of these
// would turn a knowledge cell into a no-knowledge control that nothing
// downstream could distinguish from the real thing.
func TestPlantRefusesWhatTheAgentWillNotReceive(t *testing.T) {
	s := neutral()
	text := pkseed.Delivered(s, pkseed.Metadata{})
	cases := map[string]struct {
		sess *fakeSession
		rd   *fakeReader
		want string
	}{
		"capture refused": {
			&fakeSession{captureBody: "no", captureErr: true, searchBody: text},
			&fakeReader{stored: []string{text}}, "refused",
		},
		"capture returned no id": {
			&fakeSession{captureBody: `{}`, searchBody: text},
			&fakeReader{stored: []string{text}}, "no id",
		},
		"nothing stored": {
			&fakeSession{captureBody: `{"id":"ins-1"}`, searchBody: text},
			&fakeReader{}, "none matching",
		},
		"stored text altered": {
			&fakeSession{captureBody: `{"id":"ins-1"}`, searchBody: text},
			&fakeReader{stored: []string{text + " (trimmed)"}}, "none matching",
		},
		"search does not surface it": {
			&fakeSession{captureBody: `{"id":"ins-1"}`, searchBody: `{"groups":[]}`},
			&fakeReader{stored: []string{text}}, "not reachable",
		},
		"search refused": {
			&fakeSession{captureBody: `{"id":"ins-1"}`, searchBody: "denied", searchErr: true},
			&fakeReader{stored: []string{text}}, "not reachable",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := planter(c.sess, c.rd).Plant(context.Background(),
				Request{Seed: s, Seq: 1, Probe: "what about monitors?"})
			if err == nil {
				t.Fatal("plant reported success")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestPlantWithoutProbeSkipsReachability checks the probe is opt-in and
// that skipping it is visible on the result rather than silent.
func TestPlantWithoutProbeSkipsReachability(t *testing.T) {
	s := neutral()
	text := pkseed.Delivered(s, pkseed.Metadata{})
	sess := &fakeSession{captureBody: `{"id":"ins-1"}`}
	got, err := planter(sess, &fakeReader{stored: []string{text}}).
		Plant(context.Background(), Request{Seed: s, Seq: 1})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got.Probed {
		t.Error("a plant with no probe reported a reachability check")
	}
	if len(sess.calls) != 1 {
		t.Errorf("plant made calls %v, want capture only", sess.calls)
	}
}

// TestPlantSurfacesReadBackFailure checks a broken read-back fails the
// plant rather than being taken for an empty store.
func TestPlantSurfacesReadBackFailure(t *testing.T) {
	s := neutral()
	_, err := planter(&fakeSession{captureBody: `{"id":"ins-1"}`}, &fakeReader{err: errBoom}).
		Plant(context.Background(), Request{Seed: s, Seq: 1})
	if err == nil || !strings.Contains(err.Error(), "read back") {
		t.Errorf("read-back failure surfaced as %v", err)
	}
}

var errBoom = errors.New("boom")
