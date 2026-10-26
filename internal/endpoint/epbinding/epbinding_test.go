package epbinding

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

var now = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

func lookup() *script.Script { return &script.Script{ID: "s1", Name: "lookup"} }

func TestBuild_NewBindingTakesTheDefaults(t *testing.T) {
	b, err := Build(lookup(), nil, Request{Name: "order-lookup", AuthMode: AuthAuthenticated, Actor: "admin@example.com"}, now)
	require.NoError(t, err)
	assert.Equal(t, "s1", b.ScriptID)
	assert.Equal(t, []string{"GET"}, b.Methods)
	assert.Equal(t, DefaultTimeoutSeconds, b.TimeoutSeconds)
	assert.Equal(t, DefaultMaxMemoryBytes, b.MaxMemoryBytes)
	assert.Equal(t, DefaultMaxBodyBytes, b.MaxBodyBytes)
	assert.Equal(t, DefaultRateLimit, b.RateLimit)
	assert.Equal(t, DefaultCallerRateLimit, b.CallerRateLimit)
	assert.Equal(t, []string{}, b.AllowedOrigins)
	assert.Equal(t, []string{}, b.Callers, "a new authenticated endpoint admits no one until its callers are named")
	assert.Equal(t, []string{}, b.CheckedMethods)
	assert.True(t, b.Enabled)
	assert.Equal(t, "admin@example.com", b.CreatedBy)
	assert.Equal(t, "/endpoints/order-lookup", b.URLPath())
}

func TestBuild_KeepsWhatTheRequestDoesNotName(t *testing.T) {
	prev, err := Build(lookup(), nil, Request{
		Name: "orders", AuthMode: AuthAuthenticated, Methods: []string{"post", "GET", "post"}, Path: new("/{id}/"),
		TimeoutSeconds: 20, AllowedOrigins: []string{"https://forms.example.com"}, Actor: "a@example.com",
	}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{"POST", "GET"}, prev.Methods, "upper-cased and de-duplicated in order")
	assert.Equal(t, "/{id}", prev.Path, "a trailing slash is dropped")
	prev.ID = "e1"

	prev.Callers = []string{"persona:analyst"}
	b, err := Build(lookup(), prev, Request{Enabled: new(false), Actor: "b@example.com"}, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, "e1", b.ID)
	assert.Equal(t, "orders", b.Name)
	assert.Equal(t, 20, b.TimeoutSeconds)
	assert.Equal(t, []string{"https://forms.example.com"}, b.AllowedOrigins)
	assert.Equal(t, []string{"persona:analyst"}, b.Callers)
	assert.False(t, b.Enabled)
	assert.Equal(t, "a@example.com", b.CreatedBy)
	assert.Equal(t, "b@example.com", b.UpdatedBy)
}

func TestBuild_RefusesALibrary(t *testing.T) {
	_, err := Build(&script.Script{ID: "l", Library: true}, nil, Request{Name: "x", AuthMode: AuthAuthenticated}, now)
	require.ErrorContains(t, err, "a library")
}

// A change of mode clears what the old mode named, so a binding never carries
// a callers list it does not use or a check it does not require.
func TestBuild_AChangeOfModeClearsTheOldModesFields(t *testing.T) {
	authed, err := Build(lookup(), nil, Request{
		Name: "x", AuthMode: AuthAuthenticated, Methods: []string{"GET", "POST"}, Callers: []string{" persona:analyst ", "persona:analyst", "api_key:etl"},
	}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{"persona:analyst", "api_key:etl"}, authed.Callers, "trimmed and de-duplicated in order")

	checked, err := Build(lookup(), authed, Request{AuthMode: AuthBotCheck, BotCheck: " default ", CheckedMethods: []string{"post"}}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{}, checked.Callers)
	assert.Equal(t, "default", checked.BotCheck)
	assert.Equal(t, []string{"POST"}, checked.CheckedMethods)

	same, err := Build(lookup(), checked, Request{AuthMode: AuthBotCheck}, now)
	require.NoError(t, err)
	assert.Equal(t, "default", same.BotCheck, "naming the mode it has keeps its check")

	public, err := Build(lookup(), checked, Request{AuthMode: AuthPublic}, now)
	require.NoError(t, err)
	assert.Empty(t, public.BotCheck)
	assert.Equal(t, []string{}, public.CheckedMethods)

	_, err = Build(lookup(), public, Request{Callers: []string{"*"}}, now)
	require.ErrorContains(t, err, "a public endpoint names none")
	_, err = Build(lookup(), nil, Request{Name: "y", AuthMode: AuthBotCheck}, now)
	require.ErrorContains(t, err, "names the bot check it requires")
}

func TestChecks(t *testing.T) {
	every := Binding{AuthMode: AuthBotCheck, BotCheck: "default", Methods: []string{"GET", "POST"}}
	assert.True(t, every.Checks("GET"))
	assert.True(t, every.Checks("POST"))
	posts := every
	posts.CheckedMethods = []string{"POST"}
	assert.False(t, posts.Checks("GET"))
	assert.False(t, posts.Checks("HEAD"))
	assert.True(t, posts.Checks("POST"))
	reads := every
	reads.CheckedMethods = []string{"GET"}
	assert.True(t, reads.Checks("HEAD"), "HEAD is checked where GET is")
	assert.False(t, Binding{AuthMode: AuthPublic}.Checks("POST"))
}

func TestAdmits(t *testing.T) {
	analyst := Caller{Persona: "analyst", APIKey: "etl"}
	assert.False(t, Binding{Callers: []string{}}.Admits(analyst), "an empty list admits no one")
	assert.True(t, Binding{Callers: []string{"*"}}.Admits(Caller{}))
	assert.True(t, Binding{Callers: []string{"persona:analyst"}}.Admits(analyst))
	assert.False(t, Binding{Callers: []string{"persona:admin"}}.Admits(analyst))
	assert.True(t, Binding{Callers: []string{"api_key:etl"}}.Admits(analyst))
	assert.False(t, Binding{Callers: []string{"api_key:etl"}}.Admits(Caller{Persona: "analyst"}), "a browser session carries no key")
	assert.False(t, Binding{Callers: []string{"persona:"}}.Admits(Caller{}), "an unmapped caller matches no persona")
	assert.True(t, Binding{Callers: []string{}}.Admits(Caller{Admin: true, Persona: "admin"}), "the administrator is unrestricted")
}

func TestValidate_RefusesEachBadField(t *testing.T) {
	good := func() Binding {
		return Binding{
			Name: "ok", Methods: []string{"GET"}, AuthMode: AuthAuthenticated, Callers: []string{"*"}, TimeoutSeconds: 1,
			MaxMemoryBytes: MinMaxMemoryBytes, MaxBodyBytes: 1, RateLimit: 10, CallerRateLimit: 10,
		}
	}
	require.NoError(t, good().Validate())
	cases := map[string]struct {
		edit func(*Binding)
		want string
	}{
		"no name":            {func(b *Binding) { b.Name = "" }, "needs a name"},
		"upper name":         {func(b *Binding) { b.Name = "Orders" }, "lowercase"},
		"long name":          {func(b *Binding) { b.Name = strings.Repeat("a", 64) }, "lowercase"},
		"no methods":         {func(b *Binding) { b.Methods = nil }, "at least one method"},
		"bad method":         {func(b *Binding) { b.Methods = []string{"TRACE"} }, "not one an endpoint answers"},
		"relative path":      {func(b *Binding) { b.Path = "x" }, "must start with /"},
		"deep path":          {func(b *Binding) { b.Path = strings.Repeat("/a", 9) }, "more than 8 segments"},
		"long segment":       {func(b *Binding) { b.Path = "/" + strings.Repeat("a", 65) }, "longer than"},
		"repeated parameter": {func(b *Binding) { b.Path = "/{id}/{id}" }, "twice"},
		"partial parameter":  {func(b *Binding) { b.Path = "/x{id}" }, "whole {parameter}"},
		"empty segment":      {func(b *Binding) { b.Path = "/a//b" }, "whole {parameter}"},
		"no auth":            {func(b *Binding) { b.AuthMode = "" }, "needs an auth mode"},
		"bad auth":           {func(b *Binding) { b.AuthMode = "anyone" }, "is not one of authenticated, public, bot_check"},
		"signed_in is gone":  {func(b *Binding) { b.AuthMode = "signed_in" }, "is not one of"},
		"bad caller":         {func(b *Binding) { b.Callers = []string{"analyst"} }, `must be "*"`},
		"empty caller name":  {func(b *Binding) { b.Callers = []string{"persona:"} }, `must be "*"`},
		"star with others":   {func(b *Binding) { b.Callers = []string{"*", "persona:a"} }, "named alone"},
		"too many callers":   {func(b *Binding) { b.Callers = slices.Repeat([]string{"persona:a"}, 51) }, "at most 50"},
		"check on authed":    {func(b *Binding) { b.BotCheck = "default" }, "names neither"},
		"long check name": {func(b *Binding) {
			b.AuthMode, b.Callers, b.BotCheck = AuthBotCheck, nil, strings.Repeat("a", 64)
		}, "longer than 63"},
		"checked unanswered": {func(b *Binding) {
			b.AuthMode, b.Callers, b.BotCheck, b.CheckedMethods = AuthBotCheck, nil, "default", []string{"POST"}
		}, "is not one the endpoint answers"},
		"timeout":          {func(b *Binding) { b.TimeoutSeconds = 61 }, "timeout_seconds"},
		"memory":           {func(b *Binding) { b.MaxMemoryBytes = 1 }, "max_memory_bytes"},
		"body":             {func(b *Binding) { b.MaxBodyBytes = MaxMaxBodyBytes + 1 }, "max_body_bytes"},
		"rate":             {func(b *Binding) { b.RateLimit = 0 }, "rate_limit must"},
		"caller rate":      {func(b *Binding) { b.CallerRateLimit = 11 }, "caller_rate_limit"},
		"too many origins": {func(b *Binding) { b.AllowedOrigins = make([]string, 21) }, "at most 20"},
		"origin with path": {func(b *Binding) { b.AllowedOrigins = []string{"https://a.example.com/x"} }, "scheme and host"},
		"origin no scheme": {func(b *Binding) { b.AllowedOrigins = []string{"a.example.com"} }, "scheme and host"},
		"origin wildcard":  {func(b *Binding) { b.AllowedOrigins = []string{"*"} }, "scheme and host"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := good()
			tc.edit(&b)
			require.ErrorContains(t, b.Validate(), tc.want)
		})
	}
}

func TestMatch(t *testing.T) {
	bare := Binding{}
	params, ok := bare.Match("")
	assert.True(t, ok)
	assert.Empty(t, params)
	_, ok = bare.Match("/x")
	assert.False(t, ok, "a binding with no path answers the name alone")

	b := Binding{Path: "/orders/{id}"}
	params, ok = b.Match("/orders/a%20b/")
	require.True(t, ok)
	assert.Equal(t, map[string]string{"id": "a b"}, params)
	for _, rest := range []string{"", "/orders", "/orders/1/x", "/order/1", "orders/1", "/orders/%zz", "/orders/"} {
		_, ok := b.Match(rest)
		assert.False(t, ok, rest)
	}
}

func TestAnswers(t *testing.T) {
	b := Binding{Methods: []string{"GET", "POST"}}
	assert.True(t, b.Answers("GET"))
	assert.True(t, b.Answers("HEAD"), "HEAD is answered wherever GET is")
	assert.True(t, b.Answers("POST"))
	assert.False(t, b.Answers("DELETE"))
	assert.False(t, Binding{Methods: []string{"POST"}}.Answers("HEAD"))
}

func TestCallable(t *testing.T) {
	require.NoError(t, Callable("def handle(req):\n    return {}\n"))
	require.ErrorContains(t, Callable("def main():\n    pass\n"), "defines no handle(req)")
	require.ErrorContains(t, Callable("def handle():\n    return {}\n"), "defines no handle(req)")
	require.ErrorContains(t, Callable("def form_spec(req):\n    return {}\n"), "nor both form handlers")
	require.NoError(t, Callable("def form_spec(req):\n    return {}\n\ndef form_submit(req, values):\n    return None\n"),
		"a form is callable without handle(req)")
	require.ErrorContains(t, Callable("def handle(:\n"), "does not parse")
}
