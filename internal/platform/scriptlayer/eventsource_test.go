package scriptlayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
)

// webhookSet is handler_set kind=webhook for daily.
func webhookSet(source string) manageScriptInput {
	return manageScriptInput{
		Command: cmdHandlerSet, Name: "daily", OwnerEmail: "jane@example.com", EventSource: &source,
		Kind: scripthandler.KindWebhook,
	}
}

// #2332, #2434: the webhook source a script runs for is a handler: the rule
// refuses a source the caller may not name, an administrator names any, and
// one script runs for several sources.
func TestWebhookHandlers_TheRuleDecidesWhichSource(t *testing.T) {
	h, w := scheduleHandle(t)

	res := call(t, h, authorCtx(), webhookSet("shop"))
	require.True(t, res.IsError, "the owner may not name a source that is not their form's")
	assert.Contains(t, resultText(res), "only an administrator")

	res = call(t, h, authorCtx(), webhookSet("form-daily"))
	require.False(t, res.IsError, resultText(res))
	form := resultFields(t, res)
	assert.Equal(t, "form-daily", form["event_source"])
	assert.Contains(t, form["message"], "Each event source form-daily stores enqueues one run of daily")

	res = call(t, h, adminCtx(), webhookSet("shop"))
	require.False(t, res.IsError, resultText(res))
	assert.NotEqual(t, form["handler"], resultFields(t, res)["handler"], "one script runs for several sources")
	assert.Len(t, w.webhooks.byID, 2)

	res = call(t, h, authorCtx(), webhookSet("form-daily"))
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, form["handler"], resultFields(t, res)["handler"], "a source the script runs for answers its handler")
	assert.Len(t, w.webhooks.byID, 2)

	listed := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdHandlerList, Name: "daily", Kind: scripthandler.KindWebhook,
	}))
	assert.EqualValues(t, 2, listed["count"])

	res = call(t, h, authorCtx(), manageScriptInput{
		Command: cmdHandlerSet, Name: "daily", Kind: scripthandler.KindWebhook,
	})
	require.True(t, res.IsError, "no event_source names no source")
	assert.Contains(t, resultText(res), "event_source is required")

	w.rule.broken = true
	res = call(t, h, authorCtx(), webhookSet("form-daily"))
	require.True(t, res.IsError)
	assert.Equal(t, "the handler could not be read or written; try again", resultText(res),
		"a source the rule could not read is the store's failure: its text stays in the log")
}

// #2434: a webhook handler an administrator added for a source the owner may
// not name stays off once it is turned off: the owner turns it off, and
// turning it on again is held to the rule adding one is.
func TestWebhookHandlers_TurningOneOnIsHeldToTheRule(t *testing.T) {
	h, w := scheduleHandle(t)
	shop, _ := resultFields(t, call(t, h, adminCtx(), webhookSet("shop")))["handler"].(string)
	form, _ := resultFields(t, call(t, h, authorCtx(), webhookSet("form-daily")))["handler"].(string)

	res := call(t, h, authorCtx(), byID(cmdHandlerDisable, shop))
	require.False(t, res.IsError, "turning a handler off is anyone's who may act on the script: %s", resultText(res))
	assert.False(t, w.webhooks.byID[shop].Enabled)

	res = call(t, h, authorCtx(), byID(cmdHandlerEnable, shop))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "only an administrator")
	assert.False(t, w.webhooks.byID[shop].Enabled)

	require.False(t, call(t, h, authorCtx(), byID(cmdHandlerDisable, form)).IsError)
	res = call(t, h, authorCtx(), byID(cmdHandlerEnable, form))
	require.False(t, res.IsError, "the owner turns their own form's source on: %s", resultText(res))
	assert.True(t, w.webhooks.byID[form].Enabled)

	res = call(t, h, adminCtx(), byID(cmdHandlerEnable, shop))
	require.False(t, res.IsError, resultText(res))
	assert.True(t, w.webhooks.byID[shop].Enabled, "an administrator turns any on")
}

// #2434: update's event_source was removed with no alias: a call sending it
// is refused naming handler_set, and the script is left alone.
func TestUpdate_EventSourceIsRefusedNamingItsReplacement(t *testing.T) {
	h, w := scheduleHandle(t)
	for _, source := range []string{"form-daily", ""} {
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "Changed", EventSource: &source})
		require.True(t, res.IsError, "%q", source)
		assert.Contains(t, resultText(res), "event_source on update was removed")
		assert.Contains(t, resultText(res), "handler_set kind=webhook event_source=<source>")
	}
	assert.Empty(t, w.webhooks.byID)
	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	assert.NotEqual(t, "Changed", got["display_name"], "a refused update changes nothing")
	assert.NotContains(t, got, "event_source", "a script no longer carries one source")
}

// #2441: get reports what starts a script to its owner and an administrator,
// [] for none, and nothing of it to anyone else.
func TestGet_ReportsTheScriptsHandlersToItsOwner(t *testing.T) {
	h, w := scheduleHandle(t)
	handlers := func(ctx context.Context, owner string) (any, bool) {
		fields := resultFields(t, call(t, h, ctx, manageScriptInput{Command: cmdGet, Name: "daily", OwnerEmail: owner}))
		v, ok := fields["handlers"]
		return v, ok
	}

	none, ok := handlers(authorCtx(), "")
	require.True(t, ok)
	assert.Equal(t, []any{}, none, "no handler is [] and never null")

	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	call(t, h, authorCtx(), webhookSet("form-daily"))
	mine, ok := handlers(authorCtx(), "")
	require.True(t, ok)
	rows, _ := mine.([]any)
	require.Len(t, rows, 2)
	kinds := make([]any, 0, len(rows))
	for _, r := range rows {
		row, ok := r.(map[string]any)
		require.True(t, ok)
		kinds = append(kinds, row["kind"])
	}
	assert.ElementsMatch(t, []any{scripthandler.KindSchedule, scripthandler.KindWebhook}, kinds)

	admins, ok := handlers(adminCtx(), "jane@example.com")
	require.True(t, ok)
	assert.Len(t, admins, 2)

	_, ok = handlers(callerCtx("bob@example.com", "analyst"), "jane@example.com")
	assert.False(t, ok, "another person reads the script's definition, not what starts it")

	w.viewErr = context.DeadlineExceeded
	failed, ok := handlers(authorCtx(), "")
	require.True(t, ok)
	assert.Equal(t, []any{}, failed, "a handler read that fails leaves the script's own read standing")

	bare, _ := newHandle()
	createDaily(t, bare)
	fields := resultFields(t, call(t, bare, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	assert.Equal(t, []any{}, fields["handlers"], "a deployment keeping no handlers reports none")
}

// TestRemovedCommands_AreRefusedNamingTheirReplacement: every command the
// handler commands replaced is refused with no alias, naming what replaces
// it (#2441, #2434), even inside a run, before anything else is asked.
func TestRemovedCommands_AreRefusedNamingTheirReplacement(t *testing.T) {
	h, _ := scheduleHandle(t)
	want := map[string]string{
		"endpoint_set":     "handler_set with kind=endpoint",
		"endpoint_list":    "handler_list with kind=endpoint",
		"endpoint_enable":  "handler_enable with the handler's id",
		"endpoint_disable": "handler_disable with the handler's id",
		"schedule_set":     "handler_set with kind=schedule",
		"schedule_list":    "handler_list with kind=schedule",
		"schedule_enable":  "handler_enable with the handler's id",
		"schedule_disable": "handler_disable with the handler's id",
	}
	assert.Equal(t, want, removedCommands)
	for cmd, replacement := range want {
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmd, Name: "daily", Cron: "@daily"})
		require.True(t, res.IsError, cmd)
		assert.Equal(t, cmd+" was removed: use "+replacement+" (#2441)", resultText(res))
		_, live := h.commands()[cmd]
		assert.False(t, live, "%s is no alias", cmd)
	}
}

// TestHandlerCommandsAreInTheSchema: every handler command is one the tool
// advertises and dispatches, and the writing ones are refused in a run.
func TestHandlerCommandsAreInTheSchema(t *testing.T) {
	h, _ := newHandle()
	schema, _ := manageScriptSchema().(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	command, _ := props["command"].(map[string]any)
	enum, _ := command["enum"].([]string)
	for _, cmd := range handlerCommands {
		assert.Contains(t, enum, cmd)
		_, ok := h.commands()[cmd]
		assert.True(t, ok, "%s is dispatched", cmd)
	}
	for removed := range removedCommands {
		assert.NotContains(t, enum, removed)
	}
	for _, write := range []string{cmdHandlerSet, cmdHandlerEnable, cmdHandlerDisable, cmdHandlerDelete} {
		assert.True(t, scriptWritingCommands[write], write)
	}
	for _, read := range []string{cmdHandlerList, cmdHandlerGet} {
		assert.False(t, scriptWritingCommands[read], read)
	}
	kind, _ := props["kind"].(map[string]any)
	assert.Equal(t, scripthandler.Kinds(), kind["enum"])
	assert.Contains(t, props, "handler")
}
