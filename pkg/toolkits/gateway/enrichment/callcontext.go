package enrichment

// CallContext is everything an enrichment rule needs to evaluate its
// predicate and resolve its bindings against a single proxied tool call.
type CallContext struct {
	Connection string
	ToolName   string
	Args       any
	User       UserSnapshot
}

// UserSnapshot is the user-attribution slice exposed to enrichment bindings.
type UserSnapshot struct {
	ID    string
	Email string
}
