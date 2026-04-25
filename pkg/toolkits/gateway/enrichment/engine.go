package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Source is a read-only adapter that an enrichment rule can dispatch to.
// Implementations must enforce their own operation allowlist by returning
// an error for unrecognized operations.
type Source interface {
	Name() string
	Operations() []string
	Execute(ctx context.Context, operation string, params map[string]any) (any, error)
}

// SourceRegistry holds the dispatchable sources by name.
type SourceRegistry struct {
	sources map[string]Source
}

// NewSourceRegistry builds an empty registry.
func NewSourceRegistry() *SourceRegistry {
	return &SourceRegistry{sources: map[string]Source{}}
}

// Register adds a source, replacing any prior registration with the same name.
func (r *SourceRegistry) Register(s Source) {
	r.sources[s.Name()] = s
}

// Get returns the source registered under name, if any.
func (r *SourceRegistry) Get(name string) (Source, bool) {
	s, ok := r.sources[name]
	return s, ok
}

// Result captures the output of Engine.Apply: the (possibly augmented)
// response object plus any non-fatal warnings collected during evaluation.
type Result struct {
	Response any
	Warnings []string
	// Fired records, per rule, how the rule was handled. Used by the
	// dry-run endpoint and by audit correlation.
	Fired []FiredRule
}

// FiredRule is a per-rule outcome record exposed to the dry-run endpoint
// and audit middleware.
type FiredRule struct {
	RuleID   string
	Source   string
	Op       string
	Skipped  bool
	Error    string
	Duration time.Duration
}

// Engine pulls rules from a Store and dispatches their actions to the
// SourceRegistry, merging results into the proxied response.
type Engine struct {
	store   Store
	sources *SourceRegistry
}

// NewEngine wires an engine with the given store and source registry.
func NewEngine(store Store, sources *SourceRegistry) *Engine {
	return &Engine{store: store, sources: sources}
}

// Sources returns the engine's source registry. Used by the dry-run admin
// endpoint to share the engine's source bindings without re-registering.
func (e *Engine) Sources() *SourceRegistry {
	if e == nil {
		return nil
	}
	return e.sources
}

// Apply runs every enabled rule for the given (connection, tool) over the
// upstream response. Failed rules attach warnings to the Result; they
// never replace the response with an error. The original response is
// returned unmodified when no rule fires.
func (e *Engine) Apply(ctx context.Context, call CallContext, response any) Result {
	if e == nil || e.store == nil {
		return Result{Response: response}
	}
	rules, err := e.store.List(ctx, call.Connection, call.ToolName, true)
	if err != nil {
		slog.Warn("enrichment: failed to list rules",
			"connection", call.Connection,
			"tool", call.ToolName, "error", err)
		return Result{Response: response, Warnings: []string{"enrichment: list rules: " + err.Error()}}
	}

	out := Result{Response: response}
	for _, rule := range rules {
		fired := e.applyRule(ctx, rule, call, &out)
		out.Fired = append(out.Fired, fired)
	}
	return out
}

// applyRule evaluates a single rule and merges its result into out.
// Returns a FiredRule trace for the dry-run endpoint and audit correlation.
func (e *Engine) applyRule(ctx context.Context, rule Rule, call CallContext, out *Result) FiredRule {
	start := time.Now()
	trace := FiredRule{RuleID: rule.ID, Source: rule.EnrichAction.Source, Op: rule.EnrichAction.Operation}

	if !predicateMatches(rule.WhenPredicate, out.Response) {
		trace.Skipped = true
		trace.Duration = time.Since(start)
		return trace
	}

	source, ok := e.sources.Get(rule.EnrichAction.Source)
	if !ok {
		out.Warnings = append(out.Warnings, fmt.Sprintf("enrichment: source %q not registered (rule %s)", rule.EnrichAction.Source, rule.ID))
		trace.Error = "source not registered"
		trace.Duration = time.Since(start)
		return trace
	}
	if !slices.Contains(source.Operations(), rule.EnrichAction.Operation) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("enrichment: source %q rejected operation %q (rule %s)", rule.EnrichAction.Source, rule.EnrichAction.Operation, rule.ID))
		trace.Error = "operation not allowed"
		trace.Duration = time.Since(start)
		return trace
	}

	resolved, rerr := resolveParameters(rule.EnrichAction.Parameters, call, out.Response)
	if rerr != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("enrichment: resolve parameters (rule %s): %v", rule.ID, rerr))
		trace.Error = rerr.Error()
		trace.Duration = time.Since(start)
		return trace
	}

	value, ferr := source.Execute(ctx, rule.EnrichAction.Operation, resolved)
	trace.Duration = time.Since(start)
	if ferr != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("enrichment: execute (rule %s): %v", rule.ID, ferr))
		trace.Error = ferr.Error()
		return trace
	}

	out.Response = mergeInto(out.Response, value, rule.MergeStrategy)
	return trace
}

// predicateMatches returns true when the rule's predicate is satisfied.
func predicateMatches(p Predicate, response any) bool {
	switch p.Kind {
	case "", PredicateAlways:
		return true
	case PredicateResponseContains:
		for _, path := range p.Paths {
			if _, err := resolveJSONPath(path, response); err != nil {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// resolveParameters walks an action's parameters map. String values that
// start with "$." or "$[" are interpreted as JSONPath expressions over the
// evaluation context (args, response, user); all other values pass through.
func resolveParameters(params map[string]any, call CallContext, response any) (map[string]any, error) {
	if len(params) == 0 {
		return map[string]any{}, nil
	}
	evalCtx := map[string]any{
		"args":     call.Args,
		"response": response,
		"user": map[string]any{
			"id":    call.User.ID,
			"email": call.User.Email,
		},
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		s, isString := v.(string)
		if !isString || !looksLikeJSONPath(s) {
			out[k] = v
			continue
		}
		resolved, err := resolveJSONPath(s, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("binding %s: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// looksLikeJSONPath returns true for strings that begin with a path-marker.
func looksLikeJSONPath(s string) bool {
	return strings.HasPrefix(s, "$.") || strings.HasPrefix(s, "$[")
}

// mergeInto attaches an enrichment value into the response per the merge
// strategy. The default strategy attaches under "enrichment".
func mergeInto(response, value any, m Merge) any {
	path := m.Path
	if path == "" {
		path = "enrichment"
	}
	asMap, ok := response.(map[string]any)
	if !ok {
		return map[string]any{
			"upstream": response,
			path:       value,
		}
	}
	asMap[path] = value
	return asMap
}

// ErrSourceMissing is returned by Probe-style helpers when an action's
// configured source isn't registered with the engine.
var ErrSourceMissing = errors.New("enrichment: source not registered")
