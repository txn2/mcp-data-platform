package graphgen

import (
	"fmt"
	"regexp"
	"strings"
)

// The probe validated signature uniqueness by exhaustive scan over 42 pages.
// At thousands of pages a scan alone proves nothing about the next
// generation, and its failure mode is a collision discovered after authoring.
// The generator instead guarantees uniqueness by construction and keeps the
// scan as a verification that must pass trivially:
//
//   - every signature is a minted literal drawn from a reserved namespace: a
//     class code (two reserved letters, a dash, a unique number), a unique
//     quantity (a number the corpus uses exactly once, with its unit), or a
//     reserved name word;
//   - all non-minted prose is digit-free and never uses a reserved word, so
//     a minted token cannot occur anywhere the generator did not place it;
//   - the mint registry hands each token out exactly once and records the
//     pages allowed to carry it.
//
// The graphfix signature battery then re-scans the whole corpus; under the
// construction it can only fail on a generator bug, which is exactly what a
// verification is for.

// classPattern is the shape of a minted class code. The filler validator
// rejects any non-minted text matching it, which keeps the namespace
// reserved.
var classPattern = regexp.MustCompile(`[A-Z]{2}-[0-9]+`)

// digitPattern finds any digit; non-minted prose must not contain one.
var digitPattern = regexp.MustCompile(`[0-9]`)

// reservedNames are the name-token pool: words minted as route, register or
// rota names. They are ordinary enough to read naturally and rare enough to
// stay out of template prose; the filler validator enforces the second part.
var reservedNames = []string{
	"garnet", "feldspar", "cinnabar", "obsidian", "malachite", "porphyry",
	"gypsum", "basalt", "dolomite", "travertine",
}

// Mint is one minted signature token.
type Mint struct {
	// Token is the literal as it appears in page prose, e.g. "RB-7" or
	// "23 minutes" or "garnet".
	Token string `json:"token"`
	// Pattern is the grading regex for the token: the quoted literal with a
	// guard against digit-run substring matches ("83" must not match "183").
	Pattern string `json:"pattern"`
	// Pages are the corpus keys allowed to carry the token.
	Pages []string `json:"pages"`
}

// minter hands out signature tokens, each exactly once.
type minter struct {
	mints  []Mint
	tokens map[string]bool
	// numbers holds every minted digit run, so two quantity mints can never
	// share one and a quantity can never equal a class code's number.
	numbers map[string]bool
	names   int
}

// newMinter returns an empty registry.
func newMinter() *minter {
	return &minter{tokens: map[string]bool{}, numbers: map[string]bool{}}
}

// record registers one minted token or panics on reuse. Minting happens at
// corpus construction from fixed literals, so a duplicate is a programming
// error in the corpus definition, not a runtime condition.
func (m *minter) record(token, pattern string, pages []string) string {
	if m.tokens[token] {
		panic(fmt.Sprintf("graphgen: token %q minted twice", token))
	}
	m.tokens[token] = true
	for _, run := range digitRuns(token) {
		if m.numbers[run] {
			panic(fmt.Sprintf("graphgen: digit run %q minted twice (token %q)", run, token))
		}
		m.numbers[run] = true
	}
	m.mints = append(m.mints, Mint{Token: token, Pattern: pattern, Pages: pages})
	return token
}

// class mints a class code, e.g. "RB-7", usable on the named pages.
func (m *minter) class(prefix string, n int, pages ...string) string {
	token := fmt.Sprintf("%s-%d", strings.ToUpper(prefix), n)
	return m.record(token, guardDigits(token), pages)
}

// quantity mints a unique number with its unit, e.g. "23 minutes". The
// pattern tolerates a hyphen where the space is ("23-minute" in a document
// still evidences the value) and guards both digit boundaries.
func (m *minter) quantity(n int, unit string, pages ...string) string {
	token := fmt.Sprintf("%d %s", n, unit)
	pattern := fmt.Sprintf(`(^|[^0-9])%d[- ]%s`, n, regexp.QuoteMeta(strings.TrimSuffix(unit, "s")))
	return m.record(token, pattern, pages)
}

// name mints a reserved name word, e.g. "garnet", usable on the named pages.
func (m *minter) name(pages ...string) string {
	if m.names >= len(reservedNames) {
		panic("graphgen: reserved name pool exhausted")
	}
	token := reservedNames[m.names]
	m.names++
	return m.record(token, regexp.QuoteMeta(token), pages)
}

// guardDigits wraps a token whose tail is a digit run so a longer number
// cannot satisfy it by substring: "RB-7" must not match "RB-71".
func guardDigits(token string) string {
	return regexp.QuoteMeta(token) + `([^0-9]|$)`
}

// digitRuns returns the maximal digit runs of a string.
func digitRuns(s string) []string {
	return regexp.MustCompile(`[0-9]+`).FindAllString(s, -1)
}
