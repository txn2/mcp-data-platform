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
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	// observeTimeout bounds the whole observation pass for one request. Row
	// estimation runs COUNT(*) when the query provider is configured for it,
	// which is unbounded work against the warehouse; an admin list must not
	// wait on it. Whatever has resolved when the budget expires is what the
	// reviewer sees.
	observeTimeout = 5 * time.Second

	// observeConcurrency bounds how many URNs are resolved at once, so a page
	// of insights cannot fan a burst of describe/count queries at the engine.
	observeConcurrency = 8

	// base10 and bits64 are how a claim integer is parsed: written in decimal,
	// held in the same width as the row estimate it is compared against.
	base10 = 10
	bits64 = 64

	// maxClaimIntegers bounds how many integers are read out of one claim.
	// Claim text is human prose, not a data feed; a bound keeps the scan of a
	// pathological body finite.
	maxClaimIntegers = 64

	// observeTTL is how long a lookup answers for. The review queue polls, and
	// without a memory an open browser tab would re-run a page of COUNT(*)
	// queries every refresh. A row count a few minutes old is still what the
	// reviewer needs: it is read against a claim, not watched as a live meter.
	observeTTL = 5 * time.Minute

	// observeCacheMax bounds the remembered lookups. This is a cache, not a
	// store: over the bound it is emptied rather than evicted precisely, since
	// a miss costs one lookup and nothing is lost.
	observeCacheMax = 512

	// maxLookupsPerRequest bounds how many entities one read may ask the
	// warehouse about, so a full page of insights cannot turn into hundreds of
	// describe/count queries at once. The answers are remembered, so the next
	// read of the same page starts where this one stopped and the page fills in
	// over a refresh or two rather than in one burst.
	maxLookupsPerRequest = 64
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
// the same question every refresh.
type Observer struct {
	provider query.Provider
	timeout  time.Duration
	ttl      time.Duration
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// cacheEntry remembers one lookup. A nil avail is a remembered "the provider
// does not report this entity as available", which is worth remembering for
// exactly as long as a positive answer.
type cacheEntry struct {
	avail   *query.TableAvailability
	expires time.Time
}

// New returns an Observer over p, or nil when no query provider is configured.
// A nil *Observer observes nothing, so a caller never needs a branch of its own
// for the no-provider deployment.
func New(p query.Provider) *Observer {
	if p == nil {
		return nil
	}
	return &Observer{
		provider: p,
		timeout:  observeTimeout,
		ttl:      observeTTL,
		now:      time.Now,
		cache:    make(map[string]cacheEntry),
	}
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

	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	available := o.resolve(ctx, urns)
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
	seen := make(map[string]struct{})
	urns := make([]string, 0, len(insights))
	for _, ins := range insights {
		if ins.Status != knowledge.StatusPending {
			continue
		}
		for _, urn := range ins.EntityURNs {
			if urn == "" {
				continue
			}
			if _, dup := seen[urn]; dup {
				continue
			}
			seen[urn] = struct{}{}
			urns = append(urns, urn)
		}
	}
	return urns
}

// resolve returns the available tables among urns, asking the provider only
// about the ones no recent lookup has already answered.
func (o *Observer) resolve(ctx context.Context, urns []string) map[string]*query.TableAvailability {
	out := make(map[string]*query.TableAvailability, len(urns))
	unanswered := o.remembered(urns, out)
	if len(unanswered) == 0 {
		return out
	}

	if len(unanswered) > maxLookupsPerRequest {
		unanswered = unanswered[:maxLookupsPerRequest]
	}

	answers := o.ask(ctx, unanswered)
	o.remember(answers)
	for urn, avail := range answers {
		if avail != nil {
			out[urn] = avail
		}
	}
	return out
}

// remembered fills out from the cache and returns the URNs still to ask about.
func (o *Observer) remembered(urns []string, out map[string]*query.TableAvailability) []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := o.now()
	unanswered := make([]string, 0, len(urns))
	for _, urn := range urns {
		entry, ok := o.cache[urn]
		if !ok || now.After(entry.expires) {
			unanswered = append(unanswered, urn)
			continue
		}
		if entry.avail != nil {
			out[urn] = entry.avail
		}
	}
	return unanswered
}

// ask looks each URN up through the provider. Lookups are independent, so they
// run concurrently under a bound. An entry with a nil value is an answer — the
// provider does not report that entity as available — while a URN missing from
// the result got no answer at all (an error, or the budget ran out), and is
// left for the next request rather than remembered as absent.
func (o *Observer) ask(ctx context.Context, urns []string) map[string]*query.TableAvailability {
	var mu sync.Mutex
	answers := make(map[string]*query.TableAvailability, len(urns))

	g := new(errgroup.Group)
	g.SetLimit(observeConcurrency)
	for _, urn := range urns {
		g.Go(func() error {
			// The budget is shared by the whole pass: once it is spent, the
			// queued lookups are dropped rather than started.
			if ctx.Err() != nil {
				return nil
			}
			avail, err := o.provider.GetTableAvailability(ctx, urn)
			if err != nil {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			if avail == nil || !avail.Available {
				answers[urn] = nil
				return nil
			}
			answers[urn] = avail
			return nil
		})
	}
	// Every task returns nil: one entity the provider cannot see must not
	// cancel the lookups of the others, so a failure is an absent observation.
	_ = g.Wait()
	return answers
}

// remember stores the answers for observeTTL, emptying the cache rather than
// growing past its bound.
func (o *Observer) remember(answers map[string]*query.TableAvailability) {
	o.mu.Lock()
	defer o.mu.Unlock()

	expires := o.now().Add(o.ttl)
	if len(o.cache)+len(answers) > observeCacheMax {
		o.cache = make(map[string]cacheEntry, len(answers))
	}
	for urn, avail := range answers {
		o.cache[urn] = cacheEntry{avail: avail, expires: expires}
	}
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
