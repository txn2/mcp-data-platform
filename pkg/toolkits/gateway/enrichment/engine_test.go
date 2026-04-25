package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSource is a fake Source whose responses and errors are programmable.
type stubSource struct {
	name       string
	operations []string
	respond    func(op string, params map[string]any) (any, error)
}

func (s *stubSource) Name() string         { return s.name }
func (s *stubSource) Operations() []string { return s.operations }
func (s *stubSource) Execute(_ context.Context, op string, p map[string]any) (any, error) {
	return s.respond(op, p)
}

// stubMemoryStore is an in-memory Store for engine tests.
type stubMemoryStore struct {
	rules   []Rule
	listErr error
}

func (s *stubMemoryStore) List(_ context.Context, connection, tool string, enabledOnly bool) ([]Rule, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]Rule, 0, len(s.rules))
	for _, r := range s.rules {
		if connection != "" && r.ConnectionName != connection {
			continue
		}
		if tool != "" && r.ToolName != tool {
			continue
		}
		if enabledOnly && !r.Enabled {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
func (*stubMemoryStore) Get(_ context.Context, _ string) (*Rule, error) { return nil, ErrRuleNotFound }
func (*stubMemoryStore) Create(_ context.Context, r Rule) (Rule, error) { return r, nil }
func (*stubMemoryStore) Update(_ context.Context, r Rule) (Rule, error) { return r, nil }
func (*stubMemoryStore) Delete(_ context.Context, _ string) error       { return nil }

func TestApply_NoEngineReturnsResponseUnchanged(t *testing.T) {
	var e *Engine
	got := e.Apply(context.Background(), CallContext{}, "hi")
	assert.Equal(t, "hi", got.Response)
}

func TestApply_EmptyStoreReturnsResponseUnchanged(t *testing.T) {
	e := NewEngine(&stubMemoryStore{}, NewSourceRegistry())
	got := e.Apply(context.Background(), CallContext{Connection: "crm", ToolName: "x"}, "hi")
	assert.Equal(t, "hi", got.Response)
	assert.Empty(t, got.Warnings)
}

func TestApply_AlwaysFires_SourceMergesIntoResponse(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "crm__get",
		EnrichAction:  Action{Source: SourceTrino, Operation: "query"},
		MergeStrategy: Merge{Kind: MergePath, Path: "warehouse_signals"},
		Enabled:       true,
	}}}
	src := &stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(_ string, _ map[string]any) (any, error) {
			return map[string]any{"lifetime_value": 1234}, nil
		},
	}
	reg := NewSourceRegistry()
	reg.Register(src)
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "crm__get"},
		map[string]any{"email": "x@x.com"})

	got, ok := res.Response.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "x@x.com", got["email"])
	assert.Equal(t, map[string]any{"lifetime_value": 1234}, got["warehouse_signals"])
	require.Len(t, res.Fired, 1)
	assert.Equal(t, "r1", res.Fired[0].RuleID)
	assert.False(t, res.Fired[0].Skipped)
}

func TestApply_PredicateResponseContainsSkipsWhenMissing(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		WhenPredicate: Predicate{Kind: PredicateResponseContains, Paths: []string{"$.email"}},
		EnrichAction:  Action{Source: SourceTrino, Operation: "query"},
		Enabled:       true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return "shouldn't fire", nil },
	})
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"name": "no email here"})

	require.Len(t, res.Fired, 1)
	assert.True(t, res.Fired[0].Skipped)
	// Response unchanged.
	assert.Equal(t, map[string]any{"name": "no email here"}, res.Response)
}

func TestApply_PredicateResponseContainsFiresWhenPresent(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		WhenPredicate: Predicate{Kind: PredicateResponseContains, Paths: []string{"$.email"}},
		EnrichAction:  Action{Source: SourceTrino, Operation: "query"},
		Enabled:       true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return "fired", nil },
	})
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"email": "x@x.com"})

	require.Len(t, res.Fired, 1)
	assert.False(t, res.Fired[0].Skipped)
}

func TestApply_BindingsResolvedFromResponse(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction: Action{
			Source: SourceTrino, Operation: "query",
			Parameters: map[string]any{
				"sql_template": "SELECT * FROM customers WHERE email = :email",
				"email":        "$.response.email",
				"region":       "$.user.id",
			},
		},
		Enabled: true,
	}}}
	var seen map[string]any
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(_ string, p map[string]any) (any, error) {
			seen = p
			return "ok", nil
		},
	})
	e := NewEngine(store, reg)

	_ = e.Apply(context.Background(),
		CallContext{
			Connection: "crm", ToolName: "x",
			User: UserSnapshot{ID: "u-1", Email: "u@x.com"},
		},
		map[string]any{"email": "x@x.com"})

	assert.Equal(t, "x@x.com", seen["email"])
	assert.Equal(t, "u-1", seen["region"])
	assert.Equal(t, "SELECT * FROM customers WHERE email = :email", seen["sql_template"])
}

func TestApply_BindingResolutionErrorAttachesWarning(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction: Action{
			Source: SourceTrino, Operation: "query",
			Parameters: map[string]any{"missing": "$.response.does_not_exist"},
		},
		Enabled: true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return "ok", nil },
	})
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"email": "x@x.com"})

	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "binding")
}

func TestApply_SourceMissingAttachesWarning(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction: Action{Source: "phantom", Operation: "query"},
		Enabled:      true,
	}}}
	e := NewEngine(store, NewSourceRegistry())

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"email": "x@x.com"})

	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "phantom")
}

func TestApply_OperationNotInAllowlistAttachesWarning(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction: Action{Source: SourceTrino, Operation: "drop_table"},
		Enabled:      true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return "shouldn't fire", nil },
	})
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"email": "x@x.com"})

	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "drop_table")
}

func TestApply_SourceErrorAttachesWarning(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction: Action{Source: SourceTrino, Operation: "query"},
		Enabled:      true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return nil, errors.New("upstream blew up") },
	})
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"email": "x@x.com"})

	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "upstream blew up")
}

func TestApply_StoreErrorAttachesWarning(t *testing.T) {
	store := &stubMemoryStore{listErr: errors.New("db down")}
	e := NewEngine(store, NewSourceRegistry())

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		"hi")

	assert.Equal(t, "hi", res.Response)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "db down")
}

func TestApply_NonMapResponseStillMergesViaUpstreamWrapper(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction:  Action{Source: SourceTrino, Operation: "query"},
		MergeStrategy: Merge{Kind: MergePath, Path: "extra"},
		Enabled:       true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return "more", nil },
	})
	e := NewEngine(store, reg)

	// Plain string response — engine wraps it under "upstream" so it can
	// still attach the enrichment value.
	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"}, "raw")
	got, ok := res.Response.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "raw", got["upstream"])
	assert.Equal(t, "more", got["extra"])
}

func TestApply_DefaultMergePathIsEnrichment(t *testing.T) {
	store := &stubMemoryStore{rules: []Rule{{
		ID: "r1", ConnectionName: "crm", ToolName: "x",
		EnrichAction: Action{Source: SourceTrino, Operation: "query"},
		Enabled:      true,
	}}}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{
		name: SourceTrino, operations: []string{"query"},
		respond: func(string, map[string]any) (any, error) { return "v", nil },
	})
	e := NewEngine(store, reg)

	res := e.Apply(context.Background(),
		CallContext{Connection: "crm", ToolName: "x"},
		map[string]any{"a": 1})
	got, ok := res.Response.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v", got["enrichment"])
}

func TestSourceRegistry_RegisterReplaces(t *testing.T) {
	reg := NewSourceRegistry()
	a := &stubSource{name: "x", operations: []string{"a"}, respond: nil}
	b := &stubSource{name: "x", operations: []string{"b"}, respond: nil}
	reg.Register(a)
	reg.Register(b)
	got, ok := reg.Get("x")
	require.True(t, ok)
	assert.Equal(t, []string{"b"}, got.Operations())
}

func TestPredicateMatches_UnsupportedKindReturnsFalse(t *testing.T) {
	got := predicateMatches(Predicate{Kind: "weird"}, map[string]any{})
	assert.False(t, got)
}

func TestErrSourceMissing_Sentinel(t *testing.T) {
	// Just exercise the sentinel for coverage.
	assert.NotEqual(t, "", ErrSourceMissing.Error())
}
