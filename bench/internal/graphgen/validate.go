package graphgen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// Generator invariants, on top of the graphfix battery. The battery proves
// signature discipline by exhaustive scan; these prove the construction that
// makes the scan pass at any scale, so a violation is caught as the rule it
// breaks rather than as a downstream collision.

// validate runs the generator's own invariants over a generated corpus.
func (r *Result) validate(core map[string]bool) error {
	for _, check := range []func(map[string]bool) error{
		r.validateClosures, r.validateReserved, r.validateMintPlacement,
	} {
		if err := check(core); err != nil {
			return err
		}
	}
	return nil
}

// validateClosures proves the scale axis clean: every cell's reference
// closure is exactly a subset of the core, every one of the cell's
// constraint source pages is inside it, and no two cells' closures overlap.
// A filler page inside a closure would grow the ground truth with the
// haystack; a shared page across cells would let one cell's episode read for
// another's.
func (r *Result) validateClosures(core map[string]bool) error {
	seen := map[string]string{}
	for _, cell := range r.Corpus.Cells {
		closure := r.Corpus.Closure(cell)
		for _, key := range closure {
			if !core[key] {
				return fmt.Errorf("graphgen: cell %q closure reaches filler page %q; the ground truth must not scale with the haystack", cell.ID, key)
			}
			if owner, taken := seen[key]; taken {
				return fmt.Errorf("graphgen: page %q is in the closure of both %q and %q", key, owner, cell.ID)
			}
			seen[key] = cell.ID
		}
		if err := constraintPagesWithin(cell, closure); err != nil {
			return err
		}
	}
	return nil
}

// constraintPagesWithin requires every source page of every constraint to be
// in the cell's closure: the study grades completeness against the closure,
// so a constraint page outside it would be gradable but unreachable.
func constraintPagesWithin(cell graphfix.CompletionCell, closure []string) error {
	inClosure := map[string]bool{}
	for _, key := range closure {
		inClosure[key] = true
	}
	for _, k := range cell.Constraints {
		for _, key := range k.Pages {
			if !inClosure[key] {
				return fmt.Errorf("graphgen: cell %q constraint %q names page %q outside the entry's closure", cell.ID, k.ID, key)
			}
		}
	}
	return nil
}

// reservedWordPattern matches any reserved name word as a word.
var reservedWordPattern = regexp.MustCompile(`\b(` + strings.Join(reservedNames, "|") + `)\b`)

// validateReserved proves the namespace half of the by-construction
// guarantee: outside minted-token placements, the corpus contains no digit,
// nothing shaped like a class code, and no reserved name word. Under this
// rule a minted signature cannot occur anywhere the mint did not put it, at
// any scale, which is what the probe's exhaustive scan could only observe
// after the fact.
func (r *Result) validateReserved(map[string]bool) error {
	allowed := r.tokensByPage()
	for _, p := range r.Corpus.Pages {
		for _, text := range []string{p.Title, p.Summary, p.Body, p.StrippedBody()} {
			scrubbed := scrub(text, allowed[p.Key])
			switch {
			case digitPattern.MatchString(scrubbed):
				return fmt.Errorf("graphgen: page %q carries a digit outside its minted tokens", p.Key)
			case classPattern.MatchString(scrubbed):
				return fmt.Errorf("graphgen: page %q carries a class-code shape outside its minted tokens", p.Key)
			case reservedWordPattern.MatchString(strings.ToLower(scrubbed)):
				return fmt.Errorf("graphgen: page %q carries a reserved name word outside its minted tokens", p.Key)
			}
		}
	}
	return r.validateCellTextReserved()
}

// validateCellTextReserved holds the harness-facing strings to the same
// namespace rule: prompts, intros and gate queries may not carry a digit, a
// class-code shape, or a reserved word, so they can never hand an episode a
// signature. (The battery already proves the weaker per-pattern form.)
func (r *Result) validateCellTextReserved() error {
	for _, cell := range r.Corpus.Cells {
		text := cell.Prompt + "\n" + cell.EntryIntro + "\n" + strings.Join(cell.GateQueries, "\n")
		switch {
		case digitPattern.MatchString(text):
			return fmt.Errorf("graphgen: cell %q prompt, intro or queries carry a digit", cell.ID)
		case classPattern.MatchString(text):
			return fmt.Errorf("graphgen: cell %q prompt, intro or queries carry a class-code shape", cell.ID)
		case reservedWordPattern.MatchString(strings.ToLower(text)):
			return fmt.Errorf("graphgen: cell %q prompt, intro or queries carry a reserved name word", cell.ID)
		}
	}
	return nil
}

// tokensByPage maps each page key to the minted literals it may carry.
func (r *Result) tokensByPage() map[string][]string {
	out := map[string][]string{}
	for _, mint := range r.Mints {
		for _, key := range mint.Pages {
			out[key] = append(out[key], mint.Token)
		}
	}
	return out
}

// scrub removes every occurrence of the allowed tokens from a text.
func scrub(text string, tokens []string) string {
	for _, token := range tokens {
		text = strings.ReplaceAll(text, token, " ")
	}
	return text
}

// validateMintPlacement proves the placement half of the guarantee: every
// minted token actually appears on every page it declares (in the stripped
// rendering, the stricter one), and the mint's page set matches the page set
// of every constraint graded by its pattern, so the registry and the cells
// can never drift apart.
func (r *Result) validateMintPlacement(map[string]bool) error {
	for _, mint := range r.Mints {
		for _, key := range mint.Pages {
			page, ok := r.Corpus.ByKey(key)
			if !ok {
				return fmt.Errorf("graphgen: mint %q declares undefined page %q", mint.Token, key)
			}
			if !strings.Contains(normalizeText(page.StrippedBody()), mint.Token) {
				return fmt.Errorf("graphgen: mint %q is not on the body of its declared page %q", mint.Token, key)
			}
		}
	}
	return r.validateMintConstraints()
}

// validateMintConstraints cross-checks constraints against the registry.
func (r *Result) validateMintConstraints() error {
	byPattern := map[string]Mint{}
	for _, mint := range r.Mints {
		byPattern[mint.Pattern] = mint
	}
	for _, cell := range r.Corpus.Cells {
		for _, k := range cell.Constraints {
			for _, pattern := range k.Patterns {
				mint, minted := byPattern[pattern]
				if !minted {
					continue // entry-control patterns may be plain prose tokens
				}
				if fmt.Sprint(mint.Pages) != fmt.Sprint(k.Pages) {
					return fmt.Errorf("graphgen: cell %q constraint %q pages %v disagree with mint %q pages %v",
						cell.ID, k.ID, k.Pages, mint.Token, mint.Pages)
				}
			}
		}
	}
	return nil
}

// normalizeText collapses whitespace, mirroring the battery's matcher.
func normalizeText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
