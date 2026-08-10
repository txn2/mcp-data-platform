package graphfix

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// normalize collapses runs of whitespace to single spaces before signature
// matching. Fixture bodies are hard-wrapped, and a document's line breaks are
// the model's own; a multi-word signature must match either regardless of
// where the lines break.
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// CompletionCell is one completion task: a request for a complete operational
// document whose constraint set is spread across pages the prompt does not
// name. The prompt carries no page names and no constraint values; the entry
// intro exists only for the no-search arms, which cannot discover an entry
// point on their own.
type CompletionCell struct {
	// ID is the archive key, e.g. "gc-billing-change".
	ID string `json:"id"`
	// Prompt is the operator task, identical in every arm.
	Prompt string `json:"prompt"`
	// EntryIntro opens the no-search prompt: "<EntryIntro> <entry reference>."
	// precedes Prompt, because an episode that cannot search has no other way
	// to hold any reference at all. It names the entry page's subject, never
	// its content.
	EntryIntro string `json:"entry_intro"`
	// EntryKey is the fixture page the cell starts from.
	EntryKey string `json:"entry_key"`
	// GateQueries are the fixture-gate phrasings, prompt-derived first. Bound
	// by the same non-disclosure rule as the pages and swept against every
	// gate limit, because the agent chooses its own phrasing and its own
	// limit (the first attempt's gate ran one query at the tool default and
	// was defeated by the episodes' first calls).
	GateQueries []string `json:"gate_queries"`
	// Constraints are the graded facts the complete document must carry.
	Constraints []Constraint `json:"constraints"`
}

// Constraint is one graded fact of a cell's document.
type Constraint struct {
	// ID is the archive key, e.g. "bc-notice".
	ID string `json:"id"`
	// Desc says what the fact is, for the archive and the summary tables.
	Desc string `json:"desc"`
	// Pages are the fixture pages whose bodies state the fact. Reading any of
	// them grounds a covered constraint; a signature matching a page outside
	// this set is a validation failure, so coverage can never be credited to
	// a page the episode did not need.
	Pages []string `json:"pages"`
	// Patterns are case-insensitive regexes over the final document. Any
	// match covers the constraint. Signatures are hard tokens (numbers, class
	// names, route names) a grounded document necessarily carries; paraphrase
	// slips past them, which undercounts identically in every arm.
	Patterns []string `json:"patterns"`
	// Discontinuity marks a constraint whose connection to the task is
	// institutional rather than topical (#1250): its source pages share no
	// vocabulary or embedding neighborhood with the task, so search cannot
	// rank them from any task-derived query and an authored edge is the only
	// discovery route. The claim is certified twice before any run — an
	// authoring-time embedding-distance check and the live sweep gate
	// requiring the source pages absent from every swept result list — never
	// assumed from this flag alone.
	Discontinuity bool `json:"discontinuity,omitempty"`
}

// Entry reports whether the constraint is stated on the cell's entry page.
// Entry constraints are a within-episode control (both arms read the entry);
// every kill condition is written on the off-entry constraints. Derived, not
// declared, so the flag can never disagree with the corpus.
func (c CompletionCell) Entry(k Constraint) bool {
	return slices.Contains(k.Pages, c.EntryKey)
}

// OffEntry returns the constraints not stated on the entry page: the spread
// mass the probe exists to measure.
func (c CompletionCell) OffEntry() []Constraint {
	out := make([]Constraint, 0, len(c.Constraints))
	for _, k := range c.Constraints {
		if !c.Entry(k) {
			out = append(out, k)
		}
	}
	return out
}

// Discontinuities returns the cell's discontinuity constraints.
func (c CompletionCell) Discontinuities() []Constraint {
	out := make([]Constraint, 0, len(c.Constraints))
	for _, k := range c.Constraints {
		if k.Discontinuity {
			out = append(out, k)
		}
	}
	return out
}

// DiscontinuityPages returns the union of the cell's discontinuity
// constraints' source pages: the pages both certification gates are read
// against.
func (c CompletionCell) DiscontinuityPages() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range c.Discontinuities() {
		for _, key := range k.Pages {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Depths returns every corpus page's reference distance from the cell's entry
// page (breadth-first over declared references), omitting unreachable pages.
func (c Corpus) Depths(cell CompletionCell) map[string]int {
	depths := map[string]int{cell.EntryKey: 0}
	queue := []string{cell.EntryKey}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		page, ok := c.ByKey(key)
		if !ok {
			continue
		}
		for _, ref := range page.Refs() {
			if _, seen := depths[ref]; !seen {
				depths[ref] = depths[key] + 1
				queue = append(queue, ref)
			}
		}
	}
	return depths
}

// Closure returns the reference closure from a cell's entry page in
// deterministic order (key ascending): every page reachable over declared
// references, the entry included. This is the study's per-cell ground truth
// (#1250): unlike a ranked result list, the closure is enumerable and
// terminating, so "complete" is decidable against it.
func (c Corpus) Closure(cell CompletionCell) []string {
	depths := c.Depths(cell)
	out := make([]string, 0, len(depths))
	for key := range depths {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// ConstraintByID returns one constraint of this cell.
func (c CompletionCell) ConstraintByID(id string) (Constraint, bool) {
	for _, k := range c.Constraints {
		if k.ID == id {
			return k, true
		}
	}
	return Constraint{}, false
}

// CompiledPatterns compiles a constraint's signatures case-insensitively.
// Validate has already proven each compiles, so a failure here is a
// programming error and is skipped rather than half-graded.
func (k Constraint) CompiledPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(k.Patterns))
	for _, p := range k.Patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}

// validateCells checks cell shape: identities, non-empty fields, real pages,
// and that every constraint has at least one source page reachable from the
// entry over declared references, because a constraint no graph walk can
// reach is a constraint only search could deliver and the arm contrast would
// be broken for it.
func (c Corpus) validateCells() error {
	ids := map[string]bool{}
	for _, cell := range c.Cells {
		if err := c.validateCellShape(cell, ids); err != nil {
			return err
		}
		depths := c.Depths(cell)
		kids := map[string]bool{}
		for _, k := range cell.Constraints {
			if err := c.validateConstraint(cell, k, kids, depths); err != nil {
				return err
			}
		}
		if len(cell.OffEntry()) < 4 {
			return fmt.Errorf("graphfix: cell %q holds %d off-entry constraints; the spread mass is the subject and needs at least 4", cell.ID, len(cell.OffEntry()))
		}
	}
	return nil
}

// validateCellShape checks one cell's identity and required fields.
func (c Corpus) validateCellShape(cell CompletionCell, ids map[string]bool) error {
	switch {
	case ids[cell.ID]:
		return fmt.Errorf("graphfix: duplicate cell id %q", cell.ID)
	case cell.Prompt == "" || cell.EntryIntro == "" || cell.EntryKey == "":
		return fmt.Errorf("graphfix: cell %q has an empty required field", cell.ID)
	case len(cell.GateQueries) < 3:
		return fmt.Errorf("graphfix: cell %q authors %d gate queries; the sweep needs at least 3", cell.ID, len(cell.GateQueries))
	case len(cell.Constraints) == 0:
		return fmt.Errorf("graphfix: cell %q has no constraints", cell.ID)
	}
	ids[cell.ID] = true
	if _, ok := c.ByKey(cell.EntryKey); !ok {
		return fmt.Errorf("graphfix: cell %q names undefined entry page %q", cell.ID, cell.EntryKey)
	}
	return nil
}

// validateConstraint checks one constraint's identity, pages and
// reachability. A discontinuity constraint must additionally be off-entry
// with every source page off-entry too: the entry page is handed to the
// no-search arms and read by construction, so a "discontinuity" stated there
// would be discoverable without either search or an edge and the label would
// claim a separation the design does not have.
func (c Corpus) validateConstraint(cell CompletionCell, k Constraint, ids map[string]bool, depths map[string]int) error {
	switch {
	case ids[k.ID]:
		return fmt.Errorf("graphfix: cell %q duplicates constraint id %q", cell.ID, k.ID)
	case k.Desc == "" || len(k.Pages) == 0 || len(k.Patterns) == 0:
		return fmt.Errorf("graphfix: cell %q constraint %q has an empty required field", cell.ID, k.ID)
	case k.Discontinuity && cell.Entry(k):
		return fmt.Errorf("graphfix: cell %q discontinuity constraint %q is stated on the entry page, which every arm reads by construction", cell.ID, k.ID)
	}
	ids[k.ID] = true
	return c.validateConstraintPages(cell, k, depths)
}

// validateConstraintPages checks a constraint's source pages exist and that
// at least one is reachable from the cell's entry.
func (c Corpus) validateConstraintPages(cell CompletionCell, k Constraint, depths map[string]int) error {
	reachable := false
	for _, key := range k.Pages {
		if _, ok := c.ByKey(key); !ok {
			return fmt.Errorf("graphfix: cell %q constraint %q names undefined page %q", cell.ID, k.ID, key)
		}
		if _, ok := depths[key]; ok {
			reachable = true
		}
	}
	if !reachable {
		return fmt.Errorf("graphfix: cell %q constraint %q has no source page reachable from entry %q", cell.ID, k.ID, cell.EntryKey)
	}
	return nil
}

// validateSignatures enforces the signature discipline every reading depends
// on. For each constraint pattern:
//
//   - it compiles;
//   - it matches the stripped body of at least one declared source page (the
//     stripped rendering is the strictest: a signature that only exists
//     inside a reference token is not content);
//   - it matches no page outside the declared set, in either rendering, so a
//     covered constraint can be attributed to its sources and only they can
//     ground it;
//   - for off-entry constraints it matches no title or summary anywhere,
//     because search renders title plus summary and would deliver the
//     signature without a read (entry constraints are exempt: both arms read
//     the entry by construction and no kill condition is written on them);
//   - it matches none of the cell's own prompt, entry intro, or gate
//     queries, so an episode can never be handed a signature by the harness.
func (c Corpus) validateSignatures() error {
	for _, cell := range c.Cells {
		for _, k := range cell.Constraints {
			for _, raw := range k.Patterns {
				re, err := regexp.Compile("(?i)" + raw)
				if err != nil {
					return fmt.Errorf("graphfix: cell %q constraint %q pattern %q does not compile: %w", cell.ID, k.ID, raw, err)
				}
				if err := c.checkSignature(cell, k, raw, re); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkSignature applies the signature rules for one compiled pattern.
func (c Corpus) checkSignature(cell CompletionCell, k Constraint, raw string, re *regexp.Regexp) error {
	if err := c.checkSignatureHolders(cell, k, raw, re); err != nil {
		return err
	}
	matches := func(text string) bool { return re.MatchString(normalize(text)) }
	if matches(cell.Prompt) || matches(cell.EntryIntro) || slices.ContainsFunc(cell.GateQueries, matches) {
		return fmt.Errorf("graphfix: cell %q constraint %q pattern %q appears in the cell's own prompt, intro or gate queries", cell.ID, k.ID, raw)
	}
	return nil
}

// checkSignatureHolders checks where one pattern lives in the corpus.
func (c Corpus) checkSignatureHolders(cell CompletionCell, k Constraint, raw string, re *regexp.Regexp) error {
	matchedDeclared := false
	for _, p := range c.Pages {
		declared, err := checkSignatureOnPage(cell, k, raw, re, p)
		if err != nil {
			return err
		}
		matchedDeclared = matchedDeclared || declared
	}
	if !matchedDeclared {
		return fmt.Errorf("graphfix: cell %q constraint %q pattern %q matches none of its declared pages %v", cell.ID, k.ID, raw, k.Pages)
	}
	return nil
}

// checkSignatureOnPage applies the holder rules for one pattern on one page,
// reporting whether the pattern matched a declared source page's body.
func checkSignatureOnPage(cell CompletionCell, k Constraint, raw string, re *regexp.Regexp, p Page) (bool, error) {
	declared := slices.Contains(k.Pages, p.Key)
	stripped := normalize(p.StrippedBody())
	inBody := re.MatchString(stripped) || re.MatchString(normalize(p.Body))
	if !declared && inBody {
		return false, fmt.Errorf("graphfix: cell %q constraint %q pattern %q also matches undeclared page %q", cell.ID, k.ID, raw, p.Key)
	}
	if !cell.Entry(k) && (re.MatchString(normalize(p.Title)) || re.MatchString(normalize(p.Summary))) {
		return false, fmt.Errorf("graphfix: cell %q off-entry constraint %q pattern %q appears in page %q title or summary, which search delivers without a read", cell.ID, k.ID, raw, p.Key)
	}
	return declared && re.MatchString(stripped), nil
}

// Covered reports whether the constraint's signature appears in a document,
// and which pattern matched. Matching is whitespace-normalized on both sides.
func (k Constraint) Covered(doc string) (bool, string) {
	doc = normalize(doc)
	for i, re := range k.CompiledPatterns() {
		if re.MatchString(doc) {
			return true, k.Patterns[i]
		}
	}
	return false, ""
}

// SignatureLeaks returns the constraints whose signatures appear in a search
// hit's rendered text. Used by the fixture gate: a signature search delivers
// is a fact no read was needed for, and the cell cannot run until the leak is
// authored away.
func (c CompletionCell) SignatureLeaks(hitText string) []string {
	var out []string
	for _, k := range c.Constraints {
		if c.Entry(k) {
			continue
		}
		if ok, _ := k.Covered(hitText); ok {
			out = append(out, k.ID)
		}
	}
	return out
}

// AllConstraintPages returns the union of the cell's constraint source pages.
func (c CompletionCell) AllConstraintPages() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range c.Constraints {
		for _, key := range k.Pages {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	slices.Sort(out)
	return out
}
