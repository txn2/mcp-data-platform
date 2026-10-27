package scriptlayer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

type eventSources map[string]whsource.Source

func (e eventSources) Get(_ context.Context, name string) (whsource.Source, error) {
	if s, ok := e[name]; ok {
		return s, nil
	}
	return whsource.Source{}, whsource.ErrNotFound
}

// #2332: update sets the webhook source a script runs for, get reports it,
// an update that does not send it leaves it alone, and the rule refuses a
// source the caller may not name.
func TestEventSourceIsSetWithUpdate(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	h.eventRule = &sourcerule.Rule{
		Sources: eventSources{
			"shop":       {Name: "shop"},
			"form-daily": {Name: "form-daily", Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "x"}}},
		},
		FormOwner: func(context.Context, string) (string, error) { return "jane@example.com", nil },
	}
	get := func() any {
		return resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))["event_source"]
	}

	shop := "shop"
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", EventSource: &shop})
	require.True(t, res.IsError, "the owner may not name a source that is not their form's")
	assert.Contains(t, resultText(res), "only an administrator")

	form := "form-daily"
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", EventSource: &form})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "form-daily", get())

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "Daily"})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "form-daily", get(), "an update that does not send event_source leaves it alone")

	res = call(t, h, adminCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com", EventSource: &shop})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "shop", get(), "an administrator names any source")

	none := ""
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", EventSource: &none})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "", get())

	h.eventRule.FormOwner = func(context.Context, string) (string, error) { return "", errors.New("db down") }
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", EventSource: &form})
	require.True(t, res.IsError)

	h.eventRule = nil
	res = call(t, h, adminCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", OwnerEmail: "jane@example.com", EventSource: &shop})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no webhook sources")
}
