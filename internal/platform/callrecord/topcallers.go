package callrecord

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// An automated caller is found by its volume: one principal under one persona
// writing most of the catalog. The operator needs to see that before the
// embedding index tells them, and needs to see it named, so the admin index
// page shows who wrote the catalog, largest share first, beside the action that
// stops it (#1980).
//
// Counting it groups the whole table, which on a catalog of a million records
// is not something to repeat on every refresh of an admin page. So the counts
// are kept for topCallersTTL and recounted after it, or as soon as a sweep has
// removed records. Whether each persona is a service account is not kept with
// them: it is read from the live rule on every answer, so the page shows a
// persona as marked the moment it is.

const (
	// topCallersLimit is how many principals and personas the answer lists.
	topCallersLimit = 10

	// topCallersTTL is how long one count is answered from before it is
	// recounted.
	topCallersTTL = 5 * time.Minute
)

// TopCallers is who wrote the catalog: the principals and personas holding the
// largest share of its records, largest first.
type TopCallers struct {
	// Total is every record in the catalog, the denominator of every share.
	Total int `json:"total" example:"1426996"`
	// Principals are the callers holding the most records. A caller is a
	// principal under one persona, since that is the unit an operator marks.
	Principals []CallerShare `json:"principals"`
	// Personas are the personas holding the most records.
	Personas []PersonaShare `json:"personas"`
	// CountedAt is when the counts were taken.
	CountedAt time.Time `json:"counted_at"`
}

// CallerShare is one principal's part of the catalog, under one persona.
type CallerShare struct {
	UserID    string `json:"user_id" example:"apikey:crm-sync"`
	UserEmail string `json:"user_email,omitempty" example:"crm-sync@example.com"`
	PersonaShare
}

// PersonaShare is one persona's part of the catalog.
type PersonaShare struct {
	Persona string `json:"persona" example:"integration"`
	Records int    `json:"records" example:"1419066"`
	// Share is Records over the catalog's total, between 0 and 1.
	Share float64 `json:"share" example:"0.994"`
	// ServiceAccount is whether the persona is marked as a service account
	// now: its new calls are not cataloged, and its records go on the next
	// sweep.
	ServiceAccount bool `json:"service_account"`
	// ExcludedByConfig is whether calls.exclude_personas names the persona,
	// which has the same effect and is changed in the config file instead.
	ExcludedByConfig bool `json:"excluded_by_config"`
}

// topCache holds the latest count. Its zero value holds nothing.
type topCache struct {
	mu    sync.Mutex
	value *TopCallers
	now   func() time.Time
	// generation counts forgets, so a count that was running when a sweep
	// removed rows is not kept as the current one.
	generation uint64
}

// clock is the cache's time source, overridable so a test can expire it
// without waiting.
func (c *topCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// forget drops the held count, so the next answer is a fresh one.
func (c *topCache) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = nil
	c.generation++
}

// held returns the count if it is younger than topCallersTTL, and the
// generation it was read at.
func (c *topCache) held() (count *TopCallers, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == nil || c.clock().Sub(c.value.CountedAt) >= topCallersTTL {
		return nil, c.generation
	}
	return c.value, c.generation
}

// keep stores a count made at generation, unless a forget came since.
func (c *topCache) keep(counted *TopCallers, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation == generation {
		c.value = counted
	}
}

const (
	// topPrincipalsQuery groups by principal and persona. The email is not a
	// grouping key: a principal's email is the same on every row it wrote, and
	// MAX picks it without splitting the group if it ever changed.
	topPrincipalsQuery = `
		SELECT user_id, MAX(user_email), persona, COUNT(*) AS n
		  FROM call_records
		 GROUP BY user_id, persona
		 ORDER BY n DESC, user_id, persona
		 LIMIT $1`

	// topPersonasQuery also yields the catalog's total, as a window over the
	// groups, which is computed before the limit and saves a third scan.
	topPersonasQuery = `
		SELECT persona, COUNT(*) AS n, SUM(COUNT(*)) OVER ()::bigint AS total
		  FROM call_records
		 GROUP BY persona
		 ORDER BY n DESC, persona
		 LIMIT $1`
)

// TopCallers answers who wrote the catalog, from a count at most topCallersTTL
// old, with each persona's service-account mark read now. The count runs
// outside the cache's lock, so a sweep finishing meanwhile is not held behind
// it; two requests that find the count expired together may both count.
func (s *PostgresStore) TopCallers(ctx context.Context) (TopCallers, error) {
	held, generation := s.top.held()
	if held == nil {
		counted, err := s.countCallers(ctx)
		if err != nil {
			return TopCallers{}, err
		}
		counted.CountedAt = s.top.clock()
		held = &counted
		s.top.keep(held, generation)
	}
	return s.marked(*held), nil
}

// marked copies a count with every persona's marks read from the live rule.
func (s *PostgresStore) marked(counted TopCallers) TopCallers {
	out := counted
	out.Principals = make([]CallerShare, len(counted.Principals))
	for i, p := range counted.Principals {
		p.PersonaShare = s.markPersona(p.PersonaShare)
		out.Principals[i] = p
	}
	out.Personas = make([]PersonaShare, len(counted.Personas))
	for i, p := range counted.Personas {
		out.Personas[i] = s.markPersona(p)
	}
	return out
}

func (s *PostgresStore) markPersona(p PersonaShare) PersonaShare {
	p.ServiceAccount = s.excluded.ServiceAccount(p.Persona)
	p.ExcludedByConfig = s.excluded.Configured(p.Persona)
	return p
}

// countCallers runs the two grouping queries and computes each share.
func (s *PostgresStore) countCallers(ctx context.Context) (TopCallers, error) {
	personas, total, err := s.countPersonas(ctx)
	if err != nil {
		return TopCallers{}, err
	}
	principals, err := s.countPrincipals(ctx)
	if err != nil {
		return TopCallers{}, err
	}
	for i := range personas {
		personas[i].Share = share(personas[i].Records, total)
	}
	for i := range principals {
		principals[i].Share = share(principals[i].Records, total)
	}
	return TopCallers{Total: total, Principals: principals, Personas: personas}, nil
}

// countPersonas returns the personas holding the most records, and the total.
func (s *PostgresStore) countPersonas(ctx context.Context) ([]PersonaShare, int, error) {
	rows, err := s.db.QueryContext(ctx, topPersonasQuery, topCallersLimit)
	if err != nil {
		return nil, 0, fmt.Errorf("counting call records by persona: %w", err)
	}
	defer func() { _ = rows.Close() }()
	personas := []PersonaShare{}
	total := 0
	for rows.Next() {
		var p PersonaShare
		if err := rows.Scan(&p.Persona, &p.Records, &total); err != nil {
			return nil, 0, fmt.Errorf("scanning call records by persona: %w", err)
		}
		personas = append(personas, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("counting call records by persona: %w", err)
	}
	return personas, total, nil
}

// countPrincipals returns the callers holding the most records.
func (s *PostgresStore) countPrincipals(ctx context.Context) ([]CallerShare, error) {
	rows, err := s.db.QueryContext(ctx, topPrincipalsQuery, topCallersLimit)
	if err != nil {
		return nil, fmt.Errorf("counting call records by caller: %w", err)
	}
	defer func() { _ = rows.Close() }()
	principals := []CallerShare{}
	for rows.Next() {
		var c CallerShare
		if err := rows.Scan(&c.UserID, &c.UserEmail, &c.Persona, &c.Records); err != nil {
			return nil, fmt.Errorf("scanning call records by caller: %w", err)
		}
		principals = append(principals, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("counting call records by caller: %w", err)
	}
	return principals, nil
}

// share is part over whole, zero when there is no whole.
func share(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}
