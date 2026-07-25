package pkseed

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Delivery metadata (RQ3, protocol 4.3). The platform does not surface any
// of this today: a knowledge search hit carries the note's text, who
// captured it, and its status, and no timestamp at all. That absence is
// precisely the platform decision the study is meant to settle (D2), so
// the treatment simulates the change rather than shipping it first: the
// enriched arm renders the fields into the delivered note as a demarcated
// block, which is what an enrichment payload would look like flattened
// into text.
//
// Every field is an estimator, never the quantity itself. A volatility
// class estimates how fast a fact of this kind goes stale; an observation
// age estimates how much time has passed for it to have done so; a
// re-observation cost estimates what checking would take. None of them
// state the staleness probability, which would collapse the agent's
// decision into arithmetic.

// Volatility classes as delivered, with the shelf-life gloss that makes a
// bare class name interpretable to a reader who has never seen the study's
// taxonomy.
var volatilityGloss = map[string]string{
	ClassPerishable: "perishable, typically valid for hours to days",
	ClassDurable:    "durable, changes only with a vendor release",
	ClassEternal:    "invariant, does not change",
}

// Metadata is the machine-derived epistemic block one delivery may carry.
// The zero value is the bare arm: no block is rendered.
type Metadata struct {
	// Enriched turns the block on. Off delivers the note's prose alone.
	Enriched bool `json:"enriched"`
	// AsOf is the day the belief was observed. The block reports it and
	// the elapsed days, because an age is what an agent can reason from
	// and a bare date makes it do arithmetic against a clock it may not
	// have.
	AsOf time.Time `json:"as_of"`
	// Now is the day the belief is delivered, fixed by the run rather than
	// read from a clock so a cell's delivered text is reproducible.
	Now time.Time `json:"now"`
	// RecheckCalls is what re-observing this belief's subject costs, in
	// calls. It is the `c` of the normative model.
	RecheckCalls int `json:"recheck_calls"`
}

// Block renders the metadata as it is delivered, or "" for the bare arm.
//
// It states what class the fact belongs to, when it was observed and how
// long ago, and what re-observation costs. It does not say what to
// conclude, and it does not say what to do.
func (m Metadata) Block(class string) string {
	if !m.Enriched {
		return ""
	}
	days := int(m.Now.Sub(m.AsOf).Hours() / 24)
	gloss, ok := volatilityGloss[class]
	if !ok {
		gloss = class
	}
	return "[knowledge metadata] volatility: " + gloss +
		"; observed " + m.AsOf.Format(dateOnly) + " (" + plural(days, "day") + " ago)" +
		"; re-observation cost: " + plural(m.RecheckCalls, "call") + "."
}

// dateOnly is the date form the block reports.
const dateOnly = "2006-01-02"

// plural renders a count with its unit.
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// Delivered is one seed as the agent receives it: the composed prose, plus
// the metadata block when the enriched arm is in force.
func Delivered(s Seed, m Metadata) string {
	block := m.Block(s.Class)
	if block == "" {
		return s.Text
	}
	return s.Text + "\n\n" + block
}

// ValidateMetadata checks the block is well-formed before a run delivers
// it: an age that runs backwards or a cost of zero would deliver an
// estimator that is not merely imprecise but wrong, and an agent reasoning
// correctly from it would reach the wrong answer.
func ValidateMetadata(m Metadata) error {
	if !m.Enriched {
		return nil
	}
	switch {
	case m.AsOf.IsZero() || m.Now.IsZero():
		return errors.New("pkseed: enriched delivery needs both an observation date and a delivery date")
	case m.Now.Before(m.AsOf):
		return fmt.Errorf("pkseed: delivery date %s precedes observation date %s",
			m.Now.Format(dateOnly), m.AsOf.Format(dateOnly))
	case m.RecheckCalls < 1:
		return fmt.Errorf("pkseed: re-observation cost must be at least one call, got %d", m.RecheckCalls)
	}
	return nil
}

// CaptureDate returns the observation date every belief's dated form
// reports, so a run's metadata and its prose agree on when the belief was
// observed. A block claiming one date while the prose claims another would
// be a confound wearing the costume of a treatment.
func CaptureDate() time.Time {
	d, err := time.Parse(dateOnly, captureDate)
	if err != nil {
		// captureDate is a constant in this package.
		panic("pkseed: captureDate is not a date: " + captureDate)
	}
	return d
}

// commandWords are the imperatives no delivered string may contain,
// shared by the seed and metadata audits.
var commandWords = []string{
	"verify", "re-verify", "recheck", "re-check", "check again", "confirm",
	"you should call", "make sure to call", "always call", "re-run",
}

// AuditDelivered reports the invariant violations in a delivered string:
// an imperative to perform the measured action, or a claim that the state
// is settled beyond the moment observed. It returns the offending
// substrings, empty when the string is clean. Exported so the same rule
// that gates the build also gates a run.
func AuditDelivered(text string) []string {
	var found []string
	lower := strings.ToLower(text)
	for _, w := range commandWords {
		if strings.Contains(lower, w) {
			found = append(found, w)
		}
	}
	for _, p := range permanenceWords {
		if strings.Contains(lower, p) {
			found = append(found, p)
		}
	}
	return found
}

// permanenceWords are the claims that would make a belief settle the
// present rather than describe a moment.
var permanenceWords = []string{
	"never", "will always", "permanent", "for good", "cannot ever",
	"no longer possible", "will not change", "there will be no",
	"any other period", "indefinitely", "forever",
}
