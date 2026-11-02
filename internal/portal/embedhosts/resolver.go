package embedhosts

import (
	"context"
	"sync"
	"time"
)

// Section is the stored administrator list as the resolver reads and writes
// it. settingsjson.Section satisfies it through an adapter in the wiring.
type Section interface {
	// Read returns the stored settings and whether an administrator has
	// written them.
	Read(ctx context.Context) (Settings, bool, error)
	Write(ctx context.Context, s Settings, author string) error
}

// cacheFor is how long one replica serves a list before reading it again: a
// share page answers from it on every view, and another replica's write is
// in force here within this.
const cacheFor = 15 * time.Second

// Resolver answers the list in force: the administrator's, else the
// configuration file's, else Default.
type Resolver struct {
	section    Section
	configured []string
	now        func() time.Time

	mu      sync.Mutex
	hosts   []string
	fetched time.Time
}

// NewResolver builds a resolver over the stored section (nil reads none) and
// the configuration file's section.
func NewResolver(section Section, cfg Config) *Resolver {
	return &Resolver{section: section, configured: cfg.Configured(), now: time.Now}
}

// Hosts returns the list in force. A failed or invalid read serves the
// configured list rather than opening or closing every embed on a fault.
func (r *Resolver) Hosts(ctx context.Context) []string {
	if r == nil {
		return Defaults()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hosts != nil && r.now().Sub(r.fetched) < cacheFor {
		return append([]string(nil), r.hosts...)
	}
	r.hosts, r.fetched = r.read(ctx), r.now()
	return append([]string(nil), r.hosts...)
}

func (r *Resolver) read(ctx context.Context) []string {
	if r.section == nil {
		return r.configured
	}
	stored, written, err := r.section.Read(ctx)
	if err != nil || !written {
		return r.configured
	}
	hosts, err := Normalize(stored.Hosts)
	if err != nil {
		return r.configured
	}
	return hosts
}

// Configured is the list the configuration file gives, which an
// administrator's list replaces.
func (r *Resolver) Configured() []string {
	return append([]string(nil), r.configured...)
}

// Write stores an administrator's list, normalized, and serves it on this
// replica at once.
func (r *Resolver) Write(ctx context.Context, hosts []string, author string) ([]string, error) {
	clean, err := Normalize(hosts)
	if err != nil {
		return nil, err
	}
	if err := r.section.Write(ctx, Settings{Hosts: clean}, author); err != nil {
		return nil, err //nolint:wrapcheck // the section names the write
	}
	r.mu.Lock()
	r.hosts, r.fetched = clean, r.now()
	r.mu.Unlock()
	return append([]string(nil), clean...), nil
}
