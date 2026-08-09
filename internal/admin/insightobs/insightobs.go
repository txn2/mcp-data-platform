// Package insightobs observes the warehouse state behind a pending insight's
// entities so the review path can put a claim beside what the platform can see
// for itself.
//
// A captured insight carries the URNs it is about. When a query provider
// resolves one of those URNs to an available table, the reviewer deciding
// whether to certify the claim should not have to take the claim's word for the
// world it describes: the table it names, the connection it lives on, and the
// row count the engine currently reports belong beside the claim text.
//
// Every observation is advisory. A URN that does not resolve, a table that is
// not available, a provider that is absent or noop, and a provider that is slow
// all degrade to no observation at all — never to an error and never to a
// refused promotion.
package insightobs

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/tableavail"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	// base10 and bits64 are how a claim integer is parsed: written in decimal,
	// held in the same width as the row estimate it is compared against.
	base10 = 10
	bits64 = 64

	// maxClaimIntegers bounds how many integers are read out of one claim.
	// Claim text is human prose, not a data feed; a bound keeps the scan of a
	// pathological body finite.
	maxClaimIntegers = 64
)

// Observation is the warehouse state observed for one entity URN carried by an
// insight. It is only ever produced for a URN the provider resolved to an
// available table, so an Observation means "the platform can see this entity",
// not "the platform looked".
type Observation struct {
	URN           string    `json:"urn" example:"urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"`
	QueryTable    string    `json:"query_table,omitempty" example:"iceberg.retail.daily_sales"`
	Connection    string    `json:"connection,omitempty" example:"primary"`
	EstimatedRows *int64    `json:"estimated_rows,omitempty" example:"1200"`
	Conflict      *Conflict `json:"conflict,omitempty"`
}

// Conflict is the advisory marker raised when a claim states a number and the
// table currently estimates a different one. It never blocks a promotion:
// estimates are estimates, the claim may be about something else entirely, and
// the reviewer decides.
type Conflict struct {
	ClaimedRows  int64  `json:"claimed_rows" example:"1140"`
	ObservedRows int64  `json:"observed_rows" example:"1200"`
	Message      string `json:"message" example:"claim states 1140; the table currently estimates 1200"`
}

// Observer resolves insight entity URNs through a query provider, remembering
// each answer briefly so a polling review queue does not re-ask the warehouse
// the same question every refresh. The lookup and its memory are the shared
// tableavail.Cache; this type owns only what the review path adds on top of it,
// the pending-only scope and the claim-versus-estimate conflict marker.
type Observer struct {
	avail *tableavail.Cache
}

// New returns an Observer over p, or nil when no query provider is configured.
// A nil *Observer observes nothing, so a caller never needs a branch of its own
// for the no-provider deployment.
func New(p query.Provider) *Observer {
	if p == nil {
		return nil
	}
	return &Observer{avail: tableavail.New(p)}
}

// Annotate returns the observed entity state for each insight, index-aligned
// with insights. Entry i is nil unless insight i is pending and at least one of
// its entity URNs resolved to an available table: a decided insight is history,
// not a call to check the world again.
func (o *Observer) Annotate(ctx context.Context, insights []knowledge.Insight) [][]Observation {
	out := make([][]Observation, len(insights))
	if o == nil || len(insights) == 0 {
		return out
	}

	urns := pendingURNs(insights)
	if len(urns) == 0 {
		return out
	}

	available := o.avail.Resolve(ctx, urns)
	if len(available) == 0 {
		return out
	}

	for i, ins := range insights {
		if ins.Status != knowledge.StatusPending {
			continue
		}
		out[i] = observationsFor(ins, available)
	}
	return out
}

// pendingURNs collects the distinct entity URNs of the pending insights. The
// same entity is commonly claimed about repeatedly, and resolving it once per
// request is both cheaper and consistent across the rows of one page.
func pendingURNs(insights []knowledge.Insight) []string {
	sets := make([][]string, 0, len(insights))
	for _, ins := range insights {
		if ins.Status != knowledge.StatusPending {
			continue
		}
		sets = append(sets, ins.EntityURNs)
	}
	return tableavail.Distinct(sets...)
}

// observationsFor builds the observations for one insight, in the order the
// insight carries its URNs, skipping the ones that did not resolve. An entity
// named twice by the same insight is observed once: the second copy would say
// the same thing about the same table.
func observationsFor(ins knowledge.Insight, available map[string]*query.TableAvailability) []Observation {
	var obs []Observation
	seen := make(map[string]struct{}, len(ins.EntityURNs))
	for _, urn := range ins.EntityURNs {
		avail, ok := available[urn]
		if !ok {
			continue
		}
		if _, dup := seen[urn]; dup {
			continue
		}
		seen[urn] = struct{}{}
		obs = append(obs, Observation{
			URN:           urn,
			QueryTable:    avail.QueryTable,
			Connection:    avail.Connection,
			EstimatedRows: avail.EstimatedRows,
			Conflict:      conflictFor(ins.InsightText, avail.EstimatedRows),
		})
	}
	return obs
}

// conflictFor reports the advisory mismatch between a number stated in the
// claim and the row count the table currently estimates.
//
// A claim may state several numbers. The one nearest the estimate is the most
// charitable reading of the claim as a row count: if even that number disagrees
// with the warehouse, every reading of the claim does. Nothing is reported when
// the provider gave no estimate, when the claim states no number, or when any
// number in the claim matches the estimate.
func conflictFor(claim string, observed *int64) *Conflict {
	// A negative estimate is not a row count, and admitting one would make the
	// distance arithmetic below representable only by overflowing.
	if observed == nil || *observed < 0 {
		return nil
	}
	claimed, ok := nearestClaimedInteger(claim, *observed)
	if !ok || claimed == *observed {
		return nil
	}
	return &Conflict{
		ClaimedRows:  claimed,
		ObservedRows: *observed,
		Message: fmt.Sprintf("claim states %d; the table currently estimates %d",
			claimed, *observed),
	}
}

// integerPattern matches a bare run of digits or a comma-grouped one ("1,140").
// A run touching a letter is not matched (\b), so identifiers like "v2" and
// "s3" are not read as claims about a count.
var integerPattern = regexp.MustCompile(`\b\d{1,3}(?:,\d{3})+\b|\b\d+\b`)

// nearestClaimedInteger returns the integer in claim closest to observed, and
// whether the claim stated one at all.
func nearestClaimedInteger(claim string, observed int64) (int64, bool) {
	var nearest int64
	var found bool
	for _, n := range claimIntegers(claim) {
		if !found || absDiff(n, observed) < absDiff(nearest, observed) {
			nearest, found = n, true
		}
	}
	return nearest, found
}

// claimIntegers reads the integers stated in claim, dropping the digit runs
// that are part of a decimal ("0.15" states no integer count).
func claimIntegers(claim string) []int64 {
	spans := integerPattern.FindAllStringIndex(claim, maxClaimIntegers)
	out := make([]int64, 0, len(spans))
	for _, span := range spans {
		if partOfDecimal(claim, span[0], span[1]) {
			continue
		}
		n, err := strconv.ParseInt(strings.ReplaceAll(claim[span[0]:span[1]], ",", ""), base10, bits64)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// partOfDecimal reports whether the digit run at [start,end) is one side of a
// decimal point.
func partOfDecimal(s string, start, end int) bool {
	if start >= 2 && s[start-1] == '.' && isDigit(s[start-2]) {
		return true
	}
	return end+1 < len(s) && s[end] == '.' && isDigit(s[end+1])
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// absDiff is the distance between two non-negative int64s, which is all this
// package compares: claim integers are unsigned by construction (the pattern
// matches no sign) and a negative estimate is refused before it gets here, so
// the subtraction cannot overflow.
func absDiff(a, b int64) int64 {
	if a < b {
		return b - a
	}
	return a - b
}
