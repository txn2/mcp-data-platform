// Package flowrun draws one run of a script on the flow graph of the version it
// executed (#1907): which cards the run reached, the audited calls each made
// and how long they took, what each wrote, and the card the run failed at.
// Calls are attributed by their call site (internal/scriptcallsite), which the
// graph records on each card and the run records on each call.
package flowrun

import (
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/scriptcallsite"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Call is one audited tool call a run made, as the overlay reads it.
type Call struct {
	CallSite      []string
	Tool          string
	DurationMS    int64
	Success       bool
	Error         string
	ResponseChars int
	// At is when the call started.
	At time.Time
	// Arguments is what the call was sent, as its audit row recorded it, in
	// JSON; ArgumentsTruncated is true when it was cut at its bound.
	Arguments          string
	ArgumentsTruncated bool
}

// RunFacts is what the run record says about how the run went.
type RunFacts struct {
	Status string
	Cause  string
	// Error is the run's failure text: a script failure's backtrace, which
	// names where it failed.
	Error      string
	Outputs    []script.RunOutput
	StateSaved bool
	HasResult  bool
	// StartedAt and FinishedAt bound the run in time; zero when it has not
	// started or not finished.
	StartedAt  time.Time
	FinishedAt time.Time
}

// NodeRun is what one card did in one run.
type NodeRun struct {
	// Calls is the audited tool calls attributed to the card, and DurationMS
	// their total time. ResponseChars is the size of what they answered.
	Calls         int   `json:"calls"`
	DurationMS    int64 `json:"duration_ms"`
	ResponseChars int   `json:"response_chars"`
	// Outputs, Rows and Bytes are what the card wrote, from the run's outputs.
	Outputs int `json:"outputs"`
	Rows    int `json:"rows"`
	// FailedCalls counts the card's calls that did not succeed, which a run
	// can outlive: a rate-limited call is refused and made again. The last
	// one's message is LastError.
	FailedCalls int    `json:"failed_calls"`
	LastError   string `json:"last_error,omitempty"`
	// Reached is true when the run made the card's call at least once.
	Reached bool `json:"reached"`
	// Failed is true on the card the run failed at, with Error its message.
	Failed bool   `json:"failed"`
	Error  string `json:"error,omitempty"`
}

// OtherCall is an audited call the diagram has no card for: a tool the source
// computes, a call a run made before call sites were recorded, or a call from
// a lambda.
type OtherCall struct {
	Tool       string   `json:"tool"`
	DurationMS int64    `json:"duration_ms"`
	Success    bool     `json:"success"`
	Error      string   `json:"error,omitempty"`
	CallSite   []string `json:"call_site,omitempty"`
}

// Overlay is one run drawn on its version's graph (#1907).
type Overlay struct {
	Nodes map[string]NodeRun `json:"nodes"`
	// Other lists the audited calls no card made, so the calls on the diagram
	// and Other always add up to the run's audited calls.
	Other []OtherCall `json:"other_calls"`
	// Calls is the run's audited tool calls, every one of them.
	Calls int `json:"calls"`
	// FailedNode is the card the run failed at, empty when it did not fail at
	// a platform call (or did not fail).
	FailedNode string `json:"failed_node,omitempty"`
	// StructureFailed is the Structure view's node or box the run failed at
	// (#1972): the fail() or the call its backtrace ends in, or else the
	// innermost helper expansion the failing line is in.
	StructureFailed string `json:"structure_failed,omitempty"`
	// Unplaced is true when the run made calls and none of them recorded
	// where in the script it was made (a run made before call sites were
	// recorded), so no call can be drawn on a card.
	Unplaced bool `json:"unplaced"`
	// Timeline is every audited call in the order it was made, for the
	// Timeline view (#1972), and RunMS the run's length.
	Timeline []TimedCall `json:"timeline"`
	RunMS    int64       `json:"run_ms"`
}

// TimedCall is one call placed in time: StartMS after the run started.
type TimedCall struct {
	StartMS       int64    `json:"start_ms"`
	DurationMS    int64    `json:"duration_ms"`
	Tool          string   `json:"tool"`
	Success       bool     `json:"success"`
	Error         string   `json:"error,omitempty"`
	ResponseChars int      `json:"response_chars"`
	CallSite      []string `json:"call_site,omitempty"`
	// Node is the card the call was attributed to, empty for an other call.
	Node string `json:"node,omitempty"`
	// Arguments is what the call was sent, in JSON, as its audit row
	// recorded it; ArgumentsTruncated is true when it was cut at its bound.
	Arguments          string `json:"arguments,omitempty"`
	ArgumentsTruncated bool   `json:"arguments_truncated,omitempty"`
}

// siteKey is a call site as a map key.
func siteKey(site []string) string { return strings.Join(site, ">") }

// Draw attributes a run's audited calls, outputs and failure to the cards
// of its version's graph.
func Draw(g scriptflow.Graph, calls []Call, facts RunFacts) Overlay {
	o := Overlay{Nodes: map[string]NodeRun{}, Other: []OtherCall{}, Calls: len(calls), Timeline: []TimedCall{}}
	bySite := map[string][]scriptflow.Node{}
	for _, n := range g.Nodes {
		if len(n.CallSite) > 0 {
			bySite[siteKey(n.CallSite)] = append(bySite[siteKey(n.CallSite)], n)
		}
	}
	var failedCalls []failedCall
	placed := false
	start := runStart(calls, facts)
	for _, c := range calls {
		placed = placed || len(c.CallSite) > 0
		id, ok := cardFor(bySite[siteKey(c.CallSite)], c.Tool)
		o.Timeline = append(o.Timeline, timed(c, start, id, ok && len(c.CallSite) > 0))
		if !ok || len(c.CallSite) == 0 {
			o.Other = append(o.Other, OtherCall{
				Tool: c.Tool, DurationMS: c.DurationMS, Success: c.Success, Error: c.Error, CallSite: c.CallSite,
			})
			continue
		}
		o.addCall(id, c)
		if !c.Success {
			failedCalls = append(failedCalls, failedCall{id: id, site: c.CallSite})
		}
	}
	for _, out := range facts.Outputs {
		if id, ok := cardFor(bySite[siteKey(out.CallSite)], ""); ok && len(out.CallSite) > 0 {
			o.addOutput(id, out)
		}
	}
	o.markTerminal(g, facts)
	o.markFailure(bySite, failedCalls, facts)
	o.StructureFailed = structureFailure(g.Structure, o.FailedNode, facts)
	o.Unplaced = len(calls) > 0 && !placed
	if !facts.FinishedAt.IsZero() && !start.IsZero() {
		o.RunMS = facts.FinishedAt.Sub(start).Milliseconds()
	}
	return o
}

// runStart is when the run started, or its first call when the record has no
// start.
func runStart(calls []Call, facts RunFacts) time.Time {
	if !facts.StartedAt.IsZero() || len(calls) == 0 {
		return facts.StartedAt
	}
	return calls[0].At
}

// timed places one call on the run's timeline.
func timed(c Call, start time.Time, id string, onCard bool) TimedCall {
	t := TimedCall{
		DurationMS: c.DurationMS, Tool: c.Tool, Success: c.Success, Error: c.Error,
		ResponseChars: c.ResponseChars, CallSite: c.CallSite,
		Arguments: c.Arguments, ArgumentsTruncated: c.ArgumentsTruncated,
	}
	if !start.IsZero() && !c.At.IsZero() {
		t.StartMS = max(0, c.At.Sub(start).Milliseconds())
	}
	if onCard {
		t.Node = id
	}
	return t
}

// structureFailure is the Structure view's node or box a failed run failed at:
// the node at the backtrace's call site (a fail() or a platform call), the
// step of the card the value graph named, or the innermost helper box whose
// expansion the failing line is inside.
func structureFailure(s scriptflow.Structure, failedCard string, facts RunFacts) string {
	if facts.Status != script.RunStatusFailed {
		return ""
	}
	site := scriptcallsite.FromBacktrace(facts.Error)
	if id := nodeAtSite(s, site); id != "" {
		return id
	}
	if id := stepOfCard(s, failedCard); id != "" {
		return id
	}
	return innermostBox(s, site)
}

// nodeAtSite is the node whose call site is site: a fail() or a platform call.
func nodeAtSite(s scriptflow.Structure, site []string) string {
	if len(site) == 0 {
		return ""
	}
	key := siteKey(site)
	for _, n := range s.Nodes {
		if len(n.CallSite) > 0 && siteKey(n.CallSite) == key {
			return n.ID
		}
	}
	return ""
}

// stepOfCard is the node that draws the value graph's card.
func stepOfCard(s scriptflow.Structure, card string) string {
	if card == "" {
		return ""
	}
	for _, n := range s.Nodes {
		if n.Step == card {
			return n.ID
		}
	}
	return ""
}

// innermostBox is the deepest helper expansion whose call site the failing
// site passes through: the helper whose own code failed.
func innermostBox(s scriptflow.Structure, site []string) string {
	best, depth := "", 0
	for _, b := range s.Boxes {
		if b.Kind == scriptflow.BoxFunction && len(b.CallSite) > depth && len(b.CallSite) < len(site) &&
			slices.Equal(b.CallSite, site[:len(b.CallSite)]) {
			best, depth = b.ID, len(b.CallSite)
		}
	}
	return best
}

// failedCall is a card's audited call that did not succeed, in call order.
type failedCall struct {
	id   string
	site []string
}

// cardFor picks the card a call at one site belongs to. An export and the
// table it registers share a site: the registration (manage_table) is the
// table's, everything else the export's.
func cardFor(cards []scriptflow.Node, tool string) (string, bool) {
	if len(cards) == 0 {
		return "", false
	}
	for _, n := range cards {
		if n.Kind == scriptflow.KindTable && tool == "manage_table" {
			return n.ID, true
		}
	}
	for _, n := range cards {
		if n.Kind != scriptflow.KindTable {
			return n.ID, true
		}
	}
	return cards[0].ID, true
}

func (o *Overlay) addCall(id string, c Call) {
	n := o.Nodes[id]
	n.Calls++
	n.DurationMS += c.DurationMS
	n.ResponseChars += c.ResponseChars
	n.Reached = true
	if !c.Success {
		n.FailedCalls++
		n.LastError = c.Error
	}
	o.Nodes[id] = n
}

func (o *Overlay) addOutput(id string, out script.RunOutput) {
	n := o.Nodes[id]
	n.Outputs++
	n.Rows += out.RowCount
	n.Reached = true
	o.Nodes[id] = n
}

// markTerminal marks the cards a run reaches without a call or an output: the
// state it saved and the result it handed back.
func (o *Overlay) markTerminal(g scriptflow.Graph, facts RunFacts) {
	for _, n := range g.Nodes {
		reached := (n.Kind == scriptflow.KindSaveState && facts.StateSaved) ||
			(n.Kind == scriptflow.KindResult && facts.HasResult) || n.Kind == scriptflow.KindState
		if reached {
			r := o.Nodes[n.ID]
			r.Reached = true
			o.Nodes[n.ID] = r
		}
	}
}

// markFailure names the card a failed run failed at: the call its backtrace
// ends in, when that call is a card's. Failing that, it is the card of the
// latest call that did not succeed made from the same function the backtrace
// failed in (#1933): a script that checks a status and calls fail() fails on
// the line after the call, whose card holds the failed call. A run that failed
// elsewhere (in the script's own code, or on a lost worker) names none; its
// error still says why.
func (o *Overlay) markFailure(bySite map[string][]scriptflow.Node, failedCalls []failedCall, facts RunFacts) {
	if facts.Status != script.RunStatusFailed {
		return
	}
	site := scriptcallsite.FromBacktrace(facts.Error)
	if len(site) == 0 {
		return
	}
	id, ok := cardFor(bySite[siteKey(site)], "")
	if !ok {
		id, ok = failedInSameFrame(failedCalls, site)
	}
	if !ok {
		return
	}
	n := o.Nodes[id]
	n.Failed, n.Reached = true, true
	n.Error = lastLine(facts.Error)
	o.Nodes[id] = n
	o.FailedNode = id
}

// failedInSameFrame is the card of the latest failed call whose call site
// shares every frame of the failure's but the last: the call was made from the
// function the run failed in, before it failed there.
func failedInSameFrame(failedCalls []failedCall, site []string) (string, bool) {
	enclosing := site[:len(site)-1]
	for _, fc := range slices.Backward(failedCalls) {
		if len(fc.site) == len(site) && slices.Equal(fc.site[:len(fc.site)-1], enclosing) {
			return fc.id, true
		}
	}
	return "", false
}

// lastLine is a backtrace's message: its last non-empty line.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
