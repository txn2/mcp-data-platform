package portal

import "github.com/txn2/mcp-data-platform/internal/portal/viewerlimit"

// RateLimitConfig aliases the public viewer rate limiter's config, which lives
// in internal/portal/viewerlimit with the implementation. The alias keeps the
// portal's public API spelling stable for existing callers.
type RateLimitConfig = viewerlimit.Config
