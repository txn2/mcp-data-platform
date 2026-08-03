package pollutionplant

// The plant's correctness is the sequence, not the I/O: capture it, prove
// the store holds exactly what was captured, promote it, and prove another
// identity is handed it. A fake platform lets every step of that sequence
// be driven wrong on purpose, which is the only way to know the checks fire.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// fakeSession records the calls made through it and answers them from
// scripted state.
type fakeSession struct {
	platform *fakePlatform
	seq      int
	closed   bool
}

// open counts the sessions the fake handed out that are still unclosed. A
// leaked session is a real defect: a plant and a remediation open several,
// and a study run would exhaust the platform's session budget long before
// anyone noticed.
func (f *fakePlatform) open() int {
	n := 0
	for _, s := range f.sessions {
		if !s.closed {
			n++
		}
	}
	return n
}

// call is one recorded tool call.
type call struct {
	seq  int
	tool string
	args map[string]any
}

// fakePlatform is a whole stack in a struct: what each identity's search
// and entity read returns, what capture does, and what the insights API
// reports back.
type fakePlatform struct {
	calls []call
	// sessions are every session the fake handed out, so a test can require
	// them all closed.
	sessions []*fakeSession
	// stored is what the insights API reports, keyed by capturing email.
	stored map[string][]lifecycleapi.Insight
	// insight is what GetInsight reports, keyed by id.
	insight map[string]lifecycleapi.Insight
	// pages is what the page-sink read-back sees.
	pages    []lifecycleapi.KnowledgePage
	pagesErr error
	// searchBody and entityBody are what the read-back surfaces return;
	// the refuse/err fields drive their failure paths.
	searchBody    string
	entityBody    string
	searchRefuses bool
	searchErr     error
	entityRefuses bool
	entityErr     error
	// captureErr and captureRefuses drive the capture failure paths.
	captureErr     error
	captureRefuses bool
	// nextID is the id the next capture returns.
	nextID string
	// applied records apply calls; appliedOK is what the applier returns.
	applied   []string
	appliedOK bool
	applyErr  error
	// rollbackRefuses drives the rollback failure path, and
	// rollbackChangeset is the id it confirms (empty echoes the request).
	rollbackRefuses   bool
	rollbackChangeset string
}

func newFakePlatform() *fakePlatform {
	return &fakePlatform{
		stored:    map[string][]lifecycleapi.Insight{},
		insight:   map[string]lifecycleapi.Insight{},
		nextID:    "insight-1",
		appliedOK: true,
	}
}

func (f *fakePlatform) Call(_ context.Context, seq int, tool string, args map[string]any) (string, bool, error) {
	f.calls = append(f.calls, call{seq: seq, tool: tool, args: args})
	switch tool {
	case searchTool:
		return f.searchBody, f.searchRefuses, f.searchErr
	case entityTool:
		return f.entityBody, f.entityRefuses, f.entityErr
	case applyTool:
		return f.rollbackResult(args)
	case captureTool:
		return f.captureResult()
	default:
		return "", true, nil
	}
}

// captureResult answers a capture with the next id. The captured content
// itself is recorded in f.calls, which is where the assertions read it.
func (f *fakePlatform) captureResult() (string, bool, error) {
	if f.captureErr != nil {
		return "", false, f.captureErr
	}
	if f.captureRefuses {
		return "capture refused", true, nil
	}
	return `{"id":"` + f.nextID + `"}`, false, nil
}

// rollbackResult answers an apply_knowledge rollback.
func (f *fakePlatform) rollbackResult(args map[string]any) (string, bool, error) {
	if f.rollbackRefuses {
		return "rollback refused", true, nil
	}
	id := f.rollbackChangeset
	if id == "" {
		id, _ = args["changeset_id"].(string)
	}
	raw, err := json.Marshal(map[string]any{"changeset_id": id})
	if err != nil {
		return "", false, err
	}
	return string(raw), false, nil
}

func (s *fakeSession) Call(ctx context.Context, tool string, args map[string]any) (string, bool, error) {
	return s.platform.Call(ctx, s.seq, tool, args)
}

func (s *fakeSession) Close() error {
	s.closed = true
	return nil
}

// ListInsights answers the store read-back.
func (f *fakePlatform) ListInsights(_ context.Context, filter lifecycleapi.InsightFilter) ([]lifecycleapi.Insight, error) {
	return f.stored[filter.CapturedBy], nil
}

// ListKnowledgePages answers the page-sink read-back.
func (f *fakePlatform) ListKnowledgePages(context.Context) ([]lifecycleapi.KnowledgePage, error) {
	return f.pages, f.pagesErr
}

// GetInsight answers the lifecycle read-back.
func (f *fakePlatform) GetInsight(_ context.Context, id string) (*lifecycleapi.Insight, error) {
	in, ok := f.insight[id]
	if !ok {
		return nil, errors.New("no such insight " + id)
	}
	return &in, nil
}

// client wires a Client against the fake platform.
func (f *fakePlatform) client() *Client {
	return &Client{
		dial: func(_ context.Context, seq int) (session, error) {
			s := &fakeSession{platform: f, seq: seq}
			f.sessions = append(f.sessions, s)
			return s, nil
		},
		insights: f,
		apply: func(_ context.Context, _ Treatment, insightID string) (bool, error) {
			if f.applyErr != nil {
				return false, f.applyErr
			}
			f.applied = append(f.applied, insightID)
			return f.appliedOK, nil
		},
	}
}

// wrongFiscal is the treatment most tests plant.
func wrongFiscal(t *testing.T) Treatment {
	t.Helper()
	tr, err := TreatmentByID("fiscal-boundary-wrong")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	return tr
}

// teacherSeq and witnessSeq are the identities the plant tests use.
const (
	teacherSeq = 200
	witnessSeq = 201
)

// plantable sets the fake up so a plant of tr succeeds end to end.
func plantable(f *fakePlatform, tr Treatment) {
	f.stored[pool.Email(teacherSeq)] = []lifecycleapi.Insight{{ID: f.nextID, InsightText: tr.Text}}
	f.insight[f.nextID] = lifecycleapi.Insight{ID: f.nextID, Status: "applied", ChangesetRef: "cs-1"}
	f.searchBody = "some result carrying " + tr.Needle + " and other text"
	f.entityBody = `{"description":"` + tr.Text + `"}`
}

func TestPlantProvesTheClaimReachesAnotherIdentity(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	plantable(f, tr)

	got, err := f.client().Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got.InsightID != "insight-1" || got.ChangesetID != "cs-1" {
		t.Errorf("plant did not record the ids it must remediate with later: %+v", got)
	}
	if !got.InSearch || !got.InSink {
		t.Errorf("plant reported the claim unreachable on a stack that carries it: %+v", got)
	}
	if got.Text != tr.Text {
		t.Error("the result does not record the treatment as delivered")
	}
	if len(f.applied) != 1 || f.applied[0] != "insight-1" {
		t.Errorf("the captured insight was not the one promoted: %v", f.applied)
	}
	// The witness must be a different identity from the teacher, and must
	// read both surfaces: that difference is the whole check.
	assertCalledAs(t, f, witnessSeq, searchTool)
	assertCalledAs(t, f, witnessSeq, entityTool)
	assertCalledAs(t, f, teacherSeq, captureTool)
	if n := f.open(); n != 0 {
		t.Errorf("the plant left %d session(s) open", n)
	}
}

// assertCalledAs requires a tool to have been called as one identity.
func assertCalledAs(t *testing.T, f *fakePlatform, seq int, tool string) {
	t.Helper()
	for _, c := range f.calls {
		if c.seq == seq && c.tool == tool {
			return
		}
	}
	t.Errorf("%s was never called as identity %d", tool, seq)
}

func TestPlantOpensTheGateBeforeCapturing(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	plantable(f, tr)
	if _, err := f.client().Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	// The search-first gate refuses a capture that arrives before any
	// discovery, so the teacher's first call has to be the search.
	var first string
	for _, c := range f.calls {
		if c.seq == teacherSeq {
			first = c.tool
			break
		}
	}
	if first != searchTool {
		t.Errorf("the teacher's first call was %q, not the discovery front door", first)
	}
}

func TestPlantRefusesAnUnreachableClaim(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	plantable(f, tr)
	// The store holds it and the promotion succeeded, but no surface
	// carries it: an arm run now would measure an unplanted stack.
	f.searchBody = "unrelated results"
	f.entityBody = `{"description":"the seeded description"}`

	_, err := f.client().Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq})
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("a stored-but-undelivered claim was accepted: %v", err)
	}
}

func TestPlantRefusesAlteredText(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	plantable(f, tr)
	// Capture normalized the text. The discriminant table was computed for
	// the exact string, so the run must not proceed.
	f.stored[pool.Email(teacherSeq)] = []lifecycleapi.Insight{{ID: "insight-1", InsightText: tr.Text + " (edited)"}}

	_, err := f.client().Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq})
	if err == nil || !strings.Contains(err.Error(), "none matching the planted text exactly") {
		t.Fatalf("an altered treatment was accepted: %v", err)
	}
}

func TestPlantRefusesADeclinedPromotion(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	plantable(f, tr)
	f.appliedOK = false

	_, err := f.client().Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq})
	if err == nil || !strings.Contains(err.Error(), "declined the promotion") {
		t.Fatalf("a claim that never reached the applied tier was accepted: %v", err)
	}
}

func TestPlantSurfacesCaptureFailures(t *testing.T) {
	tr := wrongFiscal(t)
	for name, setup := range map[string]func(*fakePlatform){
		"refused":   func(f *fakePlatform) { f.captureRefuses = true },
		"transport": func(f *fakePlatform) { f.captureErr = errors.New("connection reset") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform()
			plantable(f, tr)
			setup(f)
			_, err := f.client().Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq})
			if err == nil {
				t.Fatal("a failed capture was reported as a plant")
			}
			if len(f.applied) != 0 {
				t.Error("a failed capture still promoted something")
			}
		})
	}
}

func TestPlantRefusesAnIncoherentRequest(t *testing.T) {
	tr := wrongFiscal(t)
	cases := map[string]Request{
		"same identity":  {Treatment: tr, TeacherSeq: 200, WitnessSeq: 200},
		"no teacher":     {Treatment: tr, TeacherSeq: 0, WitnessSeq: 201},
		"no witness":     {Treatment: tr, TeacherSeq: 200, WitnessSeq: 0},
		"bad treatment":  {Treatment: Treatment{ID: "x"}, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq},
		"empty treatmnt": {TeacherSeq: teacherSeq, WitnessSeq: witnessSeq},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform()
			if _, err := f.client().Plant(context.Background(), req); err == nil {
				t.Fatal("an incoherent plant request was accepted")
			}
			if len(f.calls) != 0 {
				t.Error("the platform was touched before the request was checked")
			}
		})
	}
}

func TestCaptureAnchorsToTheEntityOnlyWhenThereIsOne(t *testing.T) {
	f := newFakePlatform()
	warehouse := wrongFiscal(t)
	if _, err := capture(context.Background(), &fakeSession{platform: f}, warehouse); err != nil {
		t.Fatalf("capture: %v", err)
	}
	api, err := TreatmentByID("coverage-threshold-wrong")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	f.nextID = "insight-2"
	if _, err := capture(context.Background(), &fakeSession{platform: f}, api); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, ok := f.calls[0].args["entity_urns"]; !ok {
		t.Error("a warehouse claim was captured with no entity anchor")
	}
	if _, ok := f.calls[1].args["entity_urns"]; ok {
		t.Error("a claim about a fixture with no catalog entity was anchored to one anyway")
	}
}

// TestPageSinkIsReadBackFromThePortal covers the cross-fixture arm, whose
// claim lands on a knowledge page rather than a catalog entity: reading the
// entity tool there would report every page-sink plant as unreachable at its
// sink, and the analysis would attribute an API-fixture result to a delivery
// failure that never happened.
func TestPageSinkIsReadBackFromThePortal(t *testing.T) {
	api, err := TreatmentByID("coverage-threshold-wrong")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	f := newFakePlatform()
	f.stored[pool.Email(teacherSeq)] = []lifecycleapi.Insight{{ID: "insight-1", InsightText: api.Text}}
	f.insight["insight-1"] = lifecycleapi.Insight{ID: "insight-1", Status: "applied", ChangesetRef: "cs-1"}
	f.searchBody = "a result carrying " + api.Needle
	f.pages = []lifecycleapi.KnowledgePage{{Slug: api.Page.Slug, Title: api.Page.Title, Summary: api.Page.Summary}}

	got, err := f.client().Plant(context.Background(), Request{Treatment: api, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	if !got.InSink {
		t.Error("the promoted page was not recognized as carrying the claim")
	}
	for _, c := range f.calls {
		if c.tool == entityTool {
			t.Error("a page-sink treatment was read back through the catalog entity tool")
		}
	}

	// A page that exists but no longer carries the claim, and a portal that
	// cannot be read at all, are different states and neither may pass as
	// "the sink carries it".
	f.pages = []lifecycleapi.KnowledgePage{{Slug: api.Page.Slug, Summary: "something else entirely"}}
	if carries, err := f.client().sinkCarries(context.Background(), &fakeSession{platform: f}, api); err != nil || carries {
		t.Errorf("a page without the claim was reported as carrying it (carries=%v err=%v)", carries, err)
	}
	f.pages = nil
	if carries, err := f.client().sinkCarries(context.Background(), &fakeSession{platform: f}, api); err != nil || carries {
		t.Errorf("a missing page was reported as carrying the claim (carries=%v err=%v)", carries, err)
	}
	f.pagesErr = errors.New("portal unavailable")
	if _, err := f.client().sinkCarries(context.Background(), &fakeSession{platform: f}, api); err == nil {
		t.Error("an unreadable portal was reported as a clean sink")
	}
}

func TestCaptureRejectsAnIDLessResponse(t *testing.T) {
	f := newFakePlatform()
	f.nextID = ""
	if _, err := capture(context.Background(), &fakeSession{platform: f}, wrongFiscal(t)); err == nil {
		t.Fatal("a capture that returned no id was accepted")
	}
}

// TestPlantSurfacesSurfaceFailures keeps a broken platform from reading as
// a clean one: a refused or unreachable read-back must fail the plant, not
// report "the claim is not there".
func TestPlantSurfacesSurfaceFailures(t *testing.T) {
	tr := wrongFiscal(t)
	cases := map[string]func(*fakePlatform){
		"search refused":   func(f *fakePlatform) { f.searchRefuses = true },
		"search transport": func(f *fakePlatform) { f.searchErr = errors.New("connection reset") },
		"entity refused":   func(f *fakePlatform) { f.entityRefuses = true },
		"entity transport": func(f *fakePlatform) { f.entityErr = errors.New("connection reset") },
		"dial fails":       nil,
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform()
			plantable(f, tr)
			client := f.client()
			if setup == nil {
				client.dial = func(context.Context, int) (session, error) { return nil, errors.New("no route to host") }
			} else {
				setup(f)
			}
			if _, err := client.Plant(context.Background(), Request{Treatment: tr, TeacherSeq: teacherSeq, WitnessSeq: witnessSeq}); err == nil {
				t.Fatal("a plant against a broken platform was reported as a success")
			}
		})
	}
}

// TestApplyLiveRefusesAFakeSession pins the reviewer path's requirement: the
// promote machinery drives a real MCP session, and a plant that reached it
// with anything else must fail loudly rather than silently skip the
// promotion.
func TestApplyLiveRefusesAFakeSession(t *testing.T) {
	f := newFakePlatform()
	dial := func(context.Context, int) (session, error) { return &fakeSession{platform: f}, nil }
	_, err := applyLive(context.Background(), dial, nil, nil, wrongFiscal(t), "insight-1")
	if err == nil || !strings.Contains(err.Error(), "live MCP session") {
		t.Fatalf("the reviewer path accepted a session it cannot drive: %v", err)
	}
	// A dial failure is reported as such, not as a declined promotion.
	failing := func(context.Context, int) (session, error) { return nil, errors.New("no route to host") }
	if _, err := applyLive(context.Background(), failing, nil, nil, wrongFiscal(t), "insight-1"); err == nil {
		t.Fatal("a reviewer session that never opened was reported as a promotion")
	}
}

// TestDialMCPFailsClosed checks the live dialer reports a connection
// failure instead of handing back a session that answers nothing.
func TestDialMCPFailsClosed(t *testing.T) {
	// Port 0 is never listening, so this exercises the failure path
	// without depending on anything being installed.
	tgt := target.Target{BaseURL: "http://127.0.0.1:0", Credential: "k"}
	if _, err := dialMCP(context.Background(), tgt, pool.Size, 1, time.Second); err == nil {
		t.Fatal("dialing a dead address returned a session")
	}
}

// TestNewWiresTheLivePaths checks the constructor leaves no seam nil: a nil
// applier would panic mid-plant, after the claim was already stored.
func TestNewWiresTheLivePaths(t *testing.T) {
	tgt := target.Target{BaseURL: "http://127.0.0.1:0", Credential: "k"}
	c := New(tgt, pool.Size, lifecycleapi.New(tgt.BaseURL, tgt.HTTPClient(time.Second)), time.Second, nil)
	if c == nil || c.dial == nil || c.apply == nil || c.insights == nil {
		t.Fatal("the constructor left a seam unwired")
	}
}

func TestGateIntentDefaults(t *testing.T) {
	if gateIntent("  ") != DefaultGateIntent {
		t.Error("an empty intent did not fall back to the default")
	}
	if got := gateIntent("fiscal calendar"); got != "fiscal calendar" {
		t.Errorf("a supplied intent was overridden: %q", got)
	}
}
