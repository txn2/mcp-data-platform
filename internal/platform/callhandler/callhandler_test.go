package callhandler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func handler(kind string) *Handler {
	h := &Handler{Kind: kind, Connection: "billing-api", Enabled: true}
	h.Normalize()
	return h
}

// TestNormalize fills each kind's defaults and cleans its lists.
func TestNormalize(t *testing.T) {
	h := &Handler{
		Kind: KindPolicy, Connection: " billing ", Operations: []string{" a ", "a", ""}, Users: []string{"Jo@Example.com", "jo@example.com"},
		Conditions: []Condition{{In: " BODY ", Name: " id "}},
	}
	h.Normalize()
	assert.Equal(t, "billing", h.Connection)
	assert.Equal(t, []string{"a"}, h.Operations)
	assert.Equal(t, []string{"jo@example.com"}, h.Users)
	assert.Equal(t, FailHold, h.OnFailure)
	assert.Equal(t, DefaultTimeoutMS, h.TimeoutMS)
	assert.Equal(t, Condition{In: InBody, Name: "id"}, h.Conditions[0])
	assert.NotNil(t, h.Args)
	assert.NotNil(t, h.Writes)
	assert.Equal(t, FailCall, handler(KindTransform).OnFailure)
	assert.Empty(t, handler(KindWatch).OnFailure)
}

// TestValidate refuses each thing a kind could not run.
func TestValidate(t *testing.T) {
	for _, kind := range []string{KindPolicy, KindTransform, KindWatch} {
		require.NoError(t, handler(kind).Validate(), kind)
	}
	cases := []struct {
		name string
		edit func(*Handler)
		kind string
	}{
		{"not a kind", func(h *Handler) { h.Kind = "rule" }, KindPolicy},
		{"no connection", func(h *Handler) { h.Connection = "" }, KindPolicy},
		{"bad connection pattern", func(h *Handler) { h.Connection = "[" }, KindPolicy},
		{"bad operation pattern", func(h *Handler) { h.Operations = []string{"GET ["} }, KindPolicy},
		{"too many operations", func(h *Handler) { h.Operations = make([]string, maxOperations+1) }, KindPolicy},
		{"too many people", func(h *Handler) { h.Users = make([]string, maxPeople+1) }, KindPolicy},
		{"too many conditions", func(h *Handler) { h.Conditions = make([]Condition, maxConditions+1) }, KindPolicy},
		{"a condition reading nowhere", func(h *Handler) { h.Conditions = []Condition{{In: "header", Name: "x"}} }, KindPolicy},
		{"a condition with no name", func(h *Handler) { h.Conditions = []Condition{{In: InPath}} }, KindPolicy},
		{"a time limit too long", func(h *Handler) { h.TimeoutMS = MaxTimeoutMS + 1 }, KindPolicy},
		{"a policy failing as a transform", func(h *Handler) { h.OnFailure = FailCall }, KindPolicy},
		{"a policy protecting", func(h *Handler) { h.Protected = []string{"a.b.c.d"} }, KindPolicy},
		{"a transform failing as a policy", func(h *Handler) { h.OnFailure = FailHold }, KindTransform},
		{"original with protected", func(h *Handler) { h.OnFailure, h.Protected = FailOriginal, []string{"body.x"} }, KindTransform},
		{"too many protected", func(h *Handler) { h.Protected = make([]string, maxProtected+1) }, KindTransform},
		{"a transform counting items", func(h *Handler) { h.ItemsPath = "x" }, KindTransform},
		{"a watch failing", func(h *Handler) { h.OnFailure = FailHold }, KindWatch},
		{"a watch protecting", func(h *Handler) { h.Protected = []string{"body.x"} }, KindWatch},
	}
	for _, tc := range cases {
		h := handler(tc.kind)
		tc.edit(h)
		err := h.Validate()
		require.ErrorIs(t, err, ErrInvalid, tc.name)
	}
}

func call() *Call {
	return &Call{
		Tool: "api_invoke_endpoint", Connection: "billing-api", Operation: "addSubscribers", Method: "PUT",
		Path: "/lists/7/subscribers", PathParams: map[string]string{"id": "7"}, Query: map[string]any{"q": "x"},
		Body: map[string]any{"subscribers": []any{1, 2, 3}, "account": map[string]any{"id": 123.0}, "flag": true},
		User: "jo@example.com", Persona: "marketing",
	}
}

// TestMatches is the match the platform checks: connection, who, operation
// and conditions.
func TestMatches(t *testing.T) {
	c := call()
	h := handler(KindPolicy)
	assert.True(t, h.Matches(c), "no operations and no people is every call")
	h.Enabled = false
	assert.False(t, h.Matches(c), "an off handler matches nothing")
	h.Enabled = true

	h.Connection = "billing-*"
	assert.True(t, h.Matches(c))
	h.Connection = "crm"
	assert.False(t, h.Matches(c))
	h.Connection = "billing-api"

	h.Personas = []string{"marketing"}
	assert.True(t, h.Matches(c))
	h.Personas, h.Users = []string{"support"}, []string{"jo@example.com"}
	assert.True(t, h.Matches(c), "a person named")
	h.Users = []string{"sam@example.com"}
	assert.False(t, h.Matches(c))
	h.Personas, h.Users = nil, nil

	for pattern, want := range map[string]bool{
		"*": true, "addSubscribers": true, "add*": true, "api_invoke_endpoint": true, "deleteList": false,
		"PUT /lists/*/subscribers": true, "PUT *": true, "* *": true, "POST *": false, "PUT /other": false,
		"PUT addSubscribers": false,
	} {
		h.Operations = []string{pattern}
		assert.Equal(t, want, h.Matches(c), pattern)
	}
	h.Operations = nil

	for _, tc := range []struct {
		cond Condition
		want bool
	}{
		{Condition{In: InPath, Name: "id", Equals: "7"}, true},
		{Condition{In: InPath, Name: "id", Equals: "8"}, false},
		{Condition{In: InPath, Name: "missing", Equals: "7"}, false},
		{Condition{In: InBody, Name: "account.id", Equals: "123"}, true},
		{Condition{In: InBody, Name: "flag", Equals: "true"}, true},
		{Condition{In: InBody, Name: "account.none", Equals: "x"}, false},
		{Condition{In: InQuery, Name: "q", Equals: "x"}, true},
	} {
		h.Conditions = []Condition{tc.cond}
		assert.Equal(t, tc.want, h.Matches(c), "%+v", tc.cond)
	}
}

// TestCallReadings are what a policy is told of a call: its items, whether
// it writes, and a value at a path.
func TestCallReadings(t *testing.T) {
	c := call()
	assert.Equal(t, 3, ItemCount(c.Body, "subscribers"))
	assert.Equal(t, 1, ItemCount(c.Body, ""))
	assert.Equal(t, 1, ItemCount(c.Body, "account"), "not a list is one item")
	assert.Nil(t, Lookup("not a map", "a"))
	assert.Equal(t, "x", scalar([]any{"x"}))
	assert.Equal(t, "[x y]", scalar([]any{"x", "y"}))
	assert.Equal(t, "2", scalar(2))

	h := handler(KindPolicy)
	assert.True(t, h.IsWrite(c), "PUT writes")
	h.Writes = map[string]bool{"addSubscribers": false}
	assert.False(t, h.IsWrite(c), "the handler says this operation reads")
	c.Method = "GET"
	h.Writes = nil
	assert.False(t, h.IsWrite(c))
	assert.True(t, IsKind(KindWatch))
	assert.False(t, IsKind("endpoint"))
	assert.True(t, ConnectionMatches("*", "anything"))
}

// TestContracts refuse a source a kind could not run.
func TestContracts(t *testing.T) {
	require.NoError(t, CheckPolicy("def decide(call):\n    return call\n"))
	require.NoError(t, CheckTransform("def transform(call, result):\n    return result\n"))
	require.NoError(t, CheckWatch("def main():\n    pass\n"))
	err := CheckPolicy("def decide():\n    pass\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decide")
	require.Error(t, CheckWatch("def main(:"), "a source that does not parse")
	for _, k := range []string{KindPolicy, KindTransform, KindWatch} {
		assert.NotEmpty(t, Contract(k))
	}
}

// TestPatch changes only what it names.
func TestPatch(t *testing.T) {
	h := handler(KindTransform)
	h.Operations = []string{"a"}
	conn, ops, order := "crm", []string{"b"}, 3
	Patch{Connection: &conn, Operations: &ops, Order: &order, Args: map[string]any{"x": 1}}.Apply(h)
	assert.Equal(t, "crm", h.Connection)
	assert.Equal(t, []string{"b"}, h.Operations)
	assert.Equal(t, 3, h.Order)
	assert.Equal(t, map[string]any{"x": 1}, h.Args)
	Patch{}.Apply(h)
	assert.Equal(t, "crm", h.Connection, "an empty patch changes nothing")
}

// TestWithin marks a handler's own run.
func TestWithin(t *testing.T) {
	ctx := context.Background()
	assert.False(t, Within(ctx))
	assert.True(t, Within(WithinHandler(ctx)))
}
