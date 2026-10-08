package observability

import (
	"context"
	"time"
)

// The platform-state sample the scrape callback in metrics_domain.go reads
// (#1898): dependency_up, mcp_platform_connections, mcp_platform_personas and
// search_index_last_indexed_age_seconds.

// DependencySample is one dependency's last probe.
type DependencySample struct {
	Dependency string
	Up         bool
}

// ConnectionStateCount is how many connections of a kind are in a state.
type ConnectionStateCount struct {
	Kind  string
	State string
	Count int64
}

// IndexAgeSample is how long ago a search source last finished indexing.
type IndexAgeSample struct {
	Kind string
	Age  time.Duration
}

// PlatformStateSample is what the platform-state sampler reports on a scrape.
type PlatformStateSample struct {
	Dependencies []DependencySample
	Connections  []ConnectionStateCount
	// Personas is the number of registered personas; PersonasKnown is false
	// when the sampler has no registry to count.
	Personas      int64
	PersonasKnown bool
	IndexAges     []IndexAgeSample
}

// PlatformStateSampler returns the platform's state. It is called on each
// scrape with a bounded context and must answer from a cache: a probe of a
// slow dependency must never hold a scrape.
type PlatformStateSampler func(ctx context.Context) PlatformStateSample
