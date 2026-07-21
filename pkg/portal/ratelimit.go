package portal

import "github.com/txn2/mcp-data-platform/pkg/portal/viewerlimit"

// RateLimitConfig aliases the public viewer rate limiter's config, which lives
// in pkg/portal/viewerlimit with the implementation. The alias keeps the
// portal's public API spelling stable for existing callers.
type RateLimitConfig = viewerlimit.Config
