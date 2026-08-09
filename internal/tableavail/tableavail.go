// Package tableavail resolves catalog entity URNs to the warehouse table behind
// them, remembering each answer briefly.
//
// Several surfaces need the same question answered: the insight review path asks
// what the platform can see for a pending claim's entities (#1219), and the
// delivery path asks whether a claim it is handing an agent could be settled by
// one query (#1220). Both are the same lookup against the same query provider,
// on read paths that repeat it — a polling review queue, a search that returns
// the same entity across many hits — so the answer is remembered for a few
// minutes rather than re-derived per read.
//
// Every answer is advisory. A URN that does not resolve, a table that is not
// available, a provider that is absent or noop, and a provider that is slow all
// degrade to no answer at all — never to an error and never to a refused read.
package tableavail

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

const (
	// defaultTimeout bounds the whole resolution pass for one request. Row
	// estimation runs COUNT(*) when the query provider is configured for it,
	// which is unbounded work against the warehouse; a read must not wait on it.
	// Whatever has resolved when the budget expires is what the caller gets.
	defaultTimeout = 5 * time.Second

	// DeliveryTimeout is the budget for a caller resolving on an interactive
	// delivery path rather than an admin read. It sits well under the default
	// per-provider search budget (knowledge.search_provider_timeout, 5s) because
	// what rides on it is an affordance on a payload that delivers with or
	// without it: a slow warehouse must cost the delivery its marker, never the
	// record itself and never the search arm's whole budget.
	DeliveryTimeout = 2 * time.Second

	// concurrency bounds how many URNs are resolved at once, so one page of
	// records cannot fan a burst of describe/count queries at the engine.
	concurrency = 8

	// defaultTTL is how long a lookup answers for. Read paths repeat (the review
	// queue polls, a session searches the same entity again), and without a
	// memory each repeat would re-run a page of COUNT(*) queries. An answer a few
	// minutes old is still what these callers need: it is read against a claim,
	// not watched as a live meter.
	defaultTTL = 5 * time.Minute

	// negativeTTL is how long "this entity is not available" answers for, well
	// short of defaultTTL. A positive answer is stable — the table exists and is
	// where it said it was — but a negative is not: a query adapter reports a
	// describe that failed for ANY reason as an unavailable answer with no error
	// (see pkg/query/trino.Adapter.availability), so a momentary warehouse blip is
	// indistinguishable from a table that is genuinely absent. Remembering that
	// for the positive TTL would blank the marker for minutes after a hiccup.
	negativeTTL = 30 * time.Second

	// cacheMax bounds the remembered lookups. This is a cache, not a store: over
	// the bound it is emptied rather than evicted precisely, since a miss costs
	// one lookup and nothing is lost.
	cacheMax = 512

	// maxLookupsPerRequest bounds how many entities one read may ask the
	// warehouse about, so a full page of records cannot turn into hundreds of
	// describe/count queries at once. The answers are remembered, so the next
	// read of the same page starts where this one stopped and the page fills in
	// over a refresh or two rather than in one burst.
	maxLookupsPerRequest = 64
)

// Options overrides a Cache's timing. The zero value selects the defaults; it
// exists so a caller with a different read shape (or a test with a fake clock)
// can set them without a second implementation of the cache itself.
type Options struct {
	// Timeout bounds one resolution pass. Zero selects defaultTimeout.
	Timeout time.Duration
	// TTL is how long a positive answer is remembered. Zero selects defaultTTL.
	TTL time.Duration
	// SkipRowEstimate asks the provider only where an entity is queryable, never
	// how many rows it holds, when the provider offers that cheaper path
	// (query.LocationResolver). Set it on any request-path caller that reads only
	// QueryTable/Connection: without it a provider configured for row estimation
	// runs a COUNT(*) per entity whose result the caller discards. Leave it false
	// in a caller that reads EstimatedRows.
	SkipRowEstimate bool
	// Now reads the clock. Nil selects time.Now.
	Now func() time.Time
}

// Cache resolves entity URNs through a query provider, remembering each answer
// for a TTL so repeated reads of the same entity cost one lookup.
//
// A nil *Cache resolves nothing, so a caller never needs a branch of its own for
// the no-provider deployment.
type Cache struct {
	provider query.Provider
	// locations is the provider's cheap answer path when it has one: where an
	// entity is queryable, without the COUNT(*) that fills EstimatedRows. Set
	// only when the caller asked for it (Options.SkipRowEstimate), since a caller
	// that reads EstimatedRows must not be quietly served an answer without one.
	locations query.LocationResolver
	timeout   time.Duration
	ttl       time.Duration
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]entry
	// inflight deduplicates concurrent lookups of the same URN. Without it every
	// session that misses on the same cold entity at the same moment sends its
	// own describe, so an expiring entry turns N sessions into N warehouse
	// queries for one table.
	inflight map[string]*lookup
}

// lookup is one in-flight resolution that later arrivals wait on rather than
// duplicate.
type lookup struct {
	done  chan struct{}
	avail *query.TableAvailability
	// answered distinguishes "the provider said this is not available" from "the
	// lookup produced no answer at all", which must not be remembered.
	answered bool
}

// entry remembers one lookup. A nil avail is a remembered "the provider does not
// report this entity as available", which is worth remembering for exactly as
// long as a positive answer.
type entry struct {
	avail   *query.TableAvailability
	expires time.Time
}

// New returns a Cache over p, or nil when no query provider is configured. A
// nil *Cache resolves nothing, so a caller's only branch is whether to wire it.
//
// A provider that answers but reports nothing available (the platform's noop
// fallback) is deliberately NOT refused here: "can this provider resolve
// anything" is not a question a provider can be asked, only answered by asking
// it, and a cache over such a provider costs one remembered "no" per entity.
func New(p query.Provider) *Cache { return NewWithOptions(p, Options{}) }

// NewWithOptions returns a Cache over p with opts applied, or nil when no query
// provider is configured.
func NewWithOptions(p query.Provider, opts Options) *Cache {
	if p == nil {
		return nil
	}
	c := &Cache{
		provider: p,
		timeout:  defaultTimeout,
		ttl:      defaultTTL,
		now:      time.Now,
		cache:    make(map[string]entry),
		inflight: make(map[string]*lookup),
	}
	if opts.Timeout > 0 {
		c.timeout = opts.Timeout
	}
	if opts.TTL > 0 {
		c.ttl = opts.TTL
	}
	if opts.Now != nil {
		c.now = opts.Now
	}
	if opts.SkipRowEstimate {
		if lr, ok := p.(query.LocationResolver); ok {
			c.locations = lr
		}
	}
	return c
}

// Resolve returns the available tables among urns. A URN absent from the result
// is one the provider does not report as available, could not answer for, or was
// not reached within the pass's budget — all indistinguishable to the caller by
// design, since every one of them means "no answer to show".
func (c *Cache) Resolve(ctx context.Context, urns []string) map[string]*query.TableAvailability {
	if c == nil || len(urns) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	out := make(map[string]*query.TableAvailability, len(urns))
	unanswered := c.remembered(urns, out)
	if len(unanswered) == 0 {
		return out
	}
	if len(unanswered) > maxLookupsPerRequest {
		unanswered = unanswered[:maxLookupsPerRequest]
	}

	answers := c.ask(ctx, unanswered)
	c.remember(answers)
	for urn, avail := range answers {
		if avail != nil {
			out[urn] = avail
		}
	}
	return out
}

// Verifiables returns the queryable identity of each URN that resolved, keyed by
// URN. It is the delivery-side projection of Resolve: a consumer holding a claim
// needs the table and connection to check it against, not the row estimate the
// review path compares numbers with.
func (c *Cache) Verifiables(ctx context.Context, urns []string) map[string]query.Verifiable {
	resolved := c.Resolve(ctx, urns)
	if len(resolved) == 0 {
		return nil
	}
	out := make(map[string]query.Verifiable, len(resolved))
	for urn, avail := range resolved {
		// A resolved entity with no table name would name no query to run, which
		// is the same as not resolving at all.
		if avail.QueryTable == "" {
			continue
		}
		out[urn] = query.Verifiable{URN: urn, QueryTable: avail.QueryTable, Connection: avail.Connection}
	}
	return out
}

// Distinct returns the non-empty URNs of urnSets with duplicates removed, in
// first-seen order. Callers resolving a page of records share it so one entity
// claimed by many records is looked up once, and so the whole page costs one
// pass rather than one per record.
func Distinct(urnSets ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, urns := range urnSets {
		for _, urn := range urns {
			if urn == "" {
				continue
			}
			if _, dup := seen[urn]; dup {
				continue
			}
			seen[urn] = struct{}{}
			out = append(out, urn)
		}
	}
	return out
}

// remembered fills out from the cache and returns the URNs still to ask about.
func (c *Cache) remembered(urns []string, out map[string]*query.TableAvailability) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	unanswered := make([]string, 0, len(urns))
	for _, urn := range urns {
		e, ok := c.cache[urn]
		if !ok || now.After(e.expires) {
			unanswered = append(unanswered, urn)
			continue
		}
		if e.avail != nil {
			out[urn] = e.avail
		}
	}
	return unanswered
}

// ask looks each URN up through the provider. Lookups are independent, so they
// run concurrently under a bound. An entry with a nil value is an answer — the
// provider does not report that entity as available — while a URN missing from
// the result got no answer at all (an error, or the budget ran out), and is left
// for the next request rather than remembered as absent.
func (c *Cache) ask(ctx context.Context, urns []string) map[string]*query.TableAvailability {
	var mu sync.Mutex
	answers := make(map[string]*query.TableAvailability, len(urns))

	g := new(errgroup.Group)
	g.SetLimit(concurrency)
	for _, urn := range urns {
		g.Go(func() error {
			// The budget is shared by the whole pass: once it is spent, the
			// queued lookups are dropped rather than started.
			if ctx.Err() != nil {
				return nil
			}
			avail, answered := c.lookupOnce(ctx, urn)
			if !answered {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			answers[urn] = avail
			return nil
		})
	}
	// Every task returns nil: one entity the provider cannot see must not cancel
	// the lookups of the others, so a failure is an absent answer.
	_ = g.Wait()
	return answers
}

// lookupOnce resolves one URN, sharing a single provider call with any
// concurrent caller asking about the same entity. answered reports whether the
// provider produced an answer at all; a nil avail with answered true means "the
// provider does not report this entity as available".
func (c *Cache) lookupOnce(ctx context.Context, urn string) (avail *query.TableAvailability, answered bool) {
	c.mu.Lock()
	if l, ok := c.inflight[urn]; ok {
		c.mu.Unlock()
		select {
		case <-l.done:
			return l.avail, l.answered
		case <-ctx.Done():
			// This caller's budget ran out; the owner still finishes and remembers
			// the answer for whoever asks next.
			return nil, false
		}
	}
	l := &lookup{done: make(chan struct{})}
	c.inflight[urn] = l
	c.mu.Unlock()

	l.avail, l.answered = c.resolveOne(ctx, urn)

	c.mu.Lock()
	delete(c.inflight, urn)
	c.mu.Unlock()
	close(l.done)
	return l.avail, l.answered
}

// resolveOne performs the provider call behind one lookup, through the cheap
// location path when the caller asked for it and the provider offers one.
//
// An answer computed after the budget expired is discarded rather than reported:
// a query adapter turns a canceled describe into an ordinary "not available"
// answer with no error, so keeping it would remember a warehouse that was merely
// slow as one that does not hold the table.
func (c *Cache) resolveOne(ctx context.Context, urn string) (*query.TableAvailability, bool) {
	var (
		avail *query.TableAvailability
		err   error
	)
	if c.locations != nil {
		avail, err = c.locations.ResolveLocation(ctx, urn)
	} else {
		avail, err = c.provider.GetTableAvailability(ctx, urn)
	}
	if err != nil || ctx.Err() != nil {
		return nil, false
	}
	if avail == nil || !avail.Available {
		return nil, true
	}
	return avail, true
}

// remember stores the answers, emptying the cache rather than growing past its
// bound. A positive answer holds for the full TTL; a negative one holds only for
// negativeTTL, since a provider reports a transiently failed lookup and a
// genuinely absent table in exactly the same shape.
func (c *Cache) remember(answers map[string]*query.TableAvailability) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if len(c.cache)+len(answers) > cacheMax {
		c.cache = make(map[string]entry, len(answers))
	}
	for urn, avail := range answers {
		ttl := c.ttl
		if avail == nil {
			ttl = min(negativeTTL, c.ttl)
		}
		c.cache[urn] = entry{avail: avail, expires: now.Add(ttl)}
	}
}
