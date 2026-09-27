// Package flowrun draws one run of a script on the flow graph of the version it
// executed (#1907): which cards the run reached, the audited calls each made
// and how long they took, what each wrote, and the card the run failed at.
// Calls are attributed by their call site (internal/scriptcallsite), which the
// graph records on each card and the run records on each call.
package flowrun

import (
	"slices"
	"strings"

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
}

// siteKey is a call site as a map key.
func siteKey(site []string) string { return strings.Join(site, ">") }

// Draw attributes a run's audited calls, outputs and failure to the cards
// of its version's graph.
func Draw(g scriptflow.Graph, calls []Call, facts RunFacts) Overlay {
	o := Overlay{Nodes: map[string]NodeRun{}, Other: []OtherCall{}, Calls: len(calls)}
	bySite := map[string][]scriptflow.Node{}
	for _, n := range g.Nodes {
		if len(n.CallSite) > 0 {
			bySite[siteKey(n.CallSite)] = append(bySite[siteKey(n.CallSite)], n)
		}
	}
	var failedCalls []failedCall
	for _, c := range calls {
		id, ok := cardFor(bySite[siteKey(c.CallSite)], c.Tool)
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
	return o
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
