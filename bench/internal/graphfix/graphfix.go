// Package graphfix is the seeded knowledge-page corpus and cell set for the
// graph-completion premise probe (#1241).
//
// The corpus is a small wiki of operations pages, the shape a live deployment
// of this platform holds (tens of pages, not a Wikipedia snapshot). Three
// completion cells ask for a complete operational document whose constraint
// set is spread across pages the prompt does not name, at reference depths
// 0-4 from the cell's entry page. The corpus renders in two arms from the one
// source: graph (references become fetchable tokens) and stripped (references
// become the prose fallback authored beside them), so the arm contrast is a
// machine-followable edge against a mention, with page meaning held constant.
//
// Two rules govern the content and both are load-bearing:
//
//   - Non-disclosure. No page, prompt, or fallback names the probe, the
//     study, or the act of following a reference. A plant that names itself
//     measures compliance with its own instruction (the instrument rule from
//     the knowledge-pollution disclosure defect,
//     bench/docs/findings-register.md).
//   - Signature discipline. Every constraint is graded by hard-token
//     signatures that live only on the pages that state it, never in a title
//     or summary (search renders title plus summary, so a signature there is
//     delivered without any read), and never in a prompt or gate query.
package graphfix

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// refPlaceholder marks a page reference in a fixture body:
// {{page:<key>|<fallback prose>}}. The graph arm rewrites it to
// mcp:knowledge_page:<id> once the target page exists (a reference can only
// be written after its target has an id); the stripped arm renders the
// fallback, so the stripped corpus mentions what the graph corpus links.
// The fallback is mandatory: a reference with nothing to degrade to could
// not be stripped and the arm contrast would silently lose that edge.
var refPlaceholder = regexp.MustCompile(`\{\{page:([a-z0-9-]+)\|([^{}]+)\}\}`)

// strayPlaceholder catches any {{...}} the full placeholder pattern did not
// consume: a typo'd key, a missing fallback, or a stray brace would otherwise
// plant as literal mustache text and the corpus would carry a dead edge.
var strayPlaceholder = regexp.MustCompile(`\{\{`)

// Page is one fixture knowledge page. Body carries {{page:<key>}} placeholders
// for its outbound references; the reference set is derived from the body rather
// than declared separately, so the two can never disagree.
type Page struct {
	Key     string
	Slug    string
	Title   string
	Summary string
	Body    string
	Tags    []string
}

// Refs returns the fixture keys this page references, in first-appearance order.
func (p Page) Refs() []string {
	matches := refPlaceholder.FindAllStringSubmatch(p.Body, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

// ResolveBody renders the body for one arm. In the graph arm every
// {{page:<key>|<fallback>}} becomes a platform reference using ids, and a key
// with no id is an error: planting a page whose reference is unresolved would
// seed a corpus with a broken edge. In the stripped arm every placeholder
// becomes its authored prose fallback and ids are not consulted, so the
// stripped corpus mentions its neighbors without a single fetchable edge.
func (p Page) ResolveBody(ids map[string]string, stripped bool) (string, error) {
	var missing []string
	out := refPlaceholder.ReplaceAllStringFunc(p.Body, func(m string) string {
		sub := refPlaceholder.FindStringSubmatch(m)
		if stripped {
			return sub[2]
		}
		id, ok := ids[sub[1]]
		if !ok || id == "" {
			missing = append(missing, sub[1])
			return m
		}
		return "mcp:knowledge_page:" + id
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("graphfix: page %q references unplanted page(s) %s", p.Key, strings.Join(missing, ", "))
	}
	return out, nil
}

// StrippedBody is the stripped-arm rendering: fallbacks in place of edges.
func (p Page) StrippedBody() string {
	out, _ := p.ResolveBody(nil, true)
	return out
}

// Cell is one depth condition of the retired lookup-shaped probe. The type is
// retained because every archived lookup run's results.json decodes through
// it (`-mode reread` re-derives those readings offline); the lookup cells
// themselves were removed as an instrument defect and no new run uses this
// shape.
type Cell struct {
	// ID is the archive key, e.g. "gt-d2-billing".
	ID string
	// Depth is the number of references between the entry page and the page
	// holding the answer. Depth 0 is the control: the entry page answers it.
	Depth int
	// Prompt is the operator question the episode is given.
	Prompt string
	// GateQuery is the query the fixture gate runs through `search`. It is a
	// plausible rendering of the question in search terms and is bound by the
	// same non-disclosure rule as the pages.
	GateQuery string
	// Chain is the page keys from the entry page to the answer page inclusive,
	// so len(Chain) == Depth+1.
	Chain []string
	// Answer is the ground truth, a bare number.
	Answer float64
	// Unit names what the number counts, for the archive and the summary table.
	Unit string
	// Bridges are the tokens each hop introduces, in hop order. A bridge is the
	// key the next page is looked up by and appears nowhere in the prompt, which
	// is what makes the terminal page useless on its own.
	Bridges []string
}

// EntryKey is the page the cell's question is expected to surface.
func (c Cell) EntryKey() string { return c.Chain[0] }

// AnswerKey is the page holding the ground truth.
func (c Cell) AnswerKey() string { return c.Chain[len(c.Chain)-1] }

// DepthOf reports the position of a page key in this cell's chain, or -1 when
// the page is off the chain.
func (c Cell) DepthOf(key string) int {
	for i, k := range c.Chain {
		if k == key {
			return i
		}
	}
	return -1
}

// Pages returns the corpus in a deterministic order (fixture key ascending).
func Pages() []Page {
	out := make([]Page, len(corpus))
	copy(out, corpus)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// PageByKey returns one corpus page.
func PageByKey(key string) (Page, bool) {
	for _, p := range corpus {
		if p.Key == key {
			return p, true
		}
	}
	return Page{}, false
}

// CompletionCells returns the completion cells in fixture order.
func CompletionCells() []CompletionCell {
	out := make([]CompletionCell, len(completionCells))
	copy(out, completionCells)
	return out
}

// PlantOrder returns the corpus keys in an order that plants every page after
// the pages it references, so each body can be resolved when it is written.
// It fails on a cycle rather than emitting a partial order.
func PlantOrder() ([]string, error) {
	state := make(map[string]int, len(corpus)) // 0 unvisited, 1 in progress, 2 done
	order := make([]string, 0, len(corpus))
	var visit func(key string, trail []string) error
	visit = func(key string, trail []string) error {
		switch state[key] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("graphfix: reference cycle through %s", strings.Join(append(trail, key), " -> "))
		}
		page, ok := PageByKey(key)
		if !ok {
			return fmt.Errorf("graphfix: page %q referenced but not defined", key)
		}
		state[key] = 1
		for _, ref := range page.Refs() {
			if err := visit(ref, append(trail, key)); err != nil {
				return err
			}
		}
		state[key] = 2
		order = append(order, key)
		return nil
	}
	for _, p := range Pages() {
		if err := visit(p.Key, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Validate checks the invariants a run's interpretation depends on. It is
// called by the CLI before any plant and by the package's own test, so a
// fixture edit that breaks a cell fails at build time rather than after a paid
// run.
func Validate() error {
	for _, check := range []func() error{
		validatePages, validateCompletionCells, validateSignatures, validateDisclosure,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	_, err := PlantOrder()
	return err
}

// validatePages checks page identity and shape, then that every reference
// names a page the corpus defines.
func validatePages() error {
	seen := pageIdentities{
		keys: map[string]bool{}, slugs: map[string]bool{}, titles: map[string]bool{},
	}
	for _, p := range corpus {
		if err := seen.check(p); err != nil {
			return err
		}
	}
	for _, p := range corpus {
		for _, ref := range p.Refs() {
			if !seen.keys[ref] {
				return fmt.Errorf("graphfix: page %q references undefined page %q", p.Key, ref)
			}
		}
	}
	return nil
}

// pageIdentities accumulates the identity fields that must stay unique across
// the corpus.
type pageIdentities struct {
	keys, slugs, titles map[string]bool
}

// check validates one page's required fields and identity against the pages
// already seen, then records it.
func (s pageIdentities) check(p Page) error {
	if err := checkPageShape(p); err != nil {
		return err
	}
	for _, dup := range []struct {
		seen  map[string]bool
		field string
		value string
	}{
		{s.keys, "key", p.Key}, {s.slugs, "slug", p.Slug}, {s.titles, "title", p.Title},
	} {
		if dup.seen[dup.value] {
			return fmt.Errorf("graphfix: duplicate page %s %q", dup.field, dup.value)
		}
	}
	s.keys[p.Key], s.slugs[p.Slug], s.titles[p.Title] = true, true, true
	return nil
}

// checkPageShape validates one page's own fields and body.
func checkPageShape(p Page) error {
	switch {
	case p.Key == "" || p.Slug == "" || p.Title == "" || p.Summary == "" || p.Body == "":
		return fmt.Errorf("graphfix: page %q has an empty required field", p.Key)
	case len(p.Tags) == 0:
		return fmt.Errorf("graphfix: page %q has no tags", p.Key)
	case slices.Contains(p.Refs(), p.Key):
		return fmt.Errorf("graphfix: page %q references itself", p.Key)
	case strayPlaceholder.MatchString(refPlaceholder.ReplaceAllString(p.Body, "")):
		return fmt.Errorf("graphfix: page %q carries a malformed placeholder; every reference must be {{page:<key>|<fallback>}}", p.Key)
	}
	return nil
}

// disclosureTerms are words that would tell an episode it is being measured or
// tell it to walk the graph. A page containing one has stopped being an
// ordinary page. "chain" is checked as a word so the corpus can never grow a
// "follow the chain" instruction while ordinary compounds stay writable.
var disclosureTerms = []string{
	"probe", "benchmark", "study", "experiment", "fixture", "traversal", "depth",
	"follow the link", "follow the reference", "hop", "chain",
}

// validateDisclosure enforces the register's instrument rule: a plant must not
// name itself, and must not instruct the reader to do the thing being
// measured. Both arm renderings are scanned, so a fallback cannot smuggle in
// what a body may not say.
func validateDisclosure() error {
	for _, p := range corpus {
		text := strings.ToLower(p.Title + "\n" + p.Summary + "\n" + p.Body + "\n" + p.StrippedBody())
		for _, term := range disclosureTerms {
			if strings.Contains(text, term) {
				return fmt.Errorf("graphfix: page %q contains the disclosing term %q", p.Key, term)
			}
		}
	}
	for _, c := range completionCells {
		text := strings.ToLower(c.Prompt + "\n" + c.EntryIntro + "\n" + strings.Join(c.GateQueries, "\n"))
		for _, term := range disclosureTerms {
			if strings.Contains(text, term) {
				return fmt.Errorf("graphfix: cell %q contains the disclosing term %q", c.ID, term)
			}
		}
	}
	return nil
}
