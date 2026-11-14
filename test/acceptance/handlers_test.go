//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
)

// The handler commands every criterion that gives a script an endpoint, a
// schedule or a webhook source calls (#2441, #2434): manage_script handler_set
// with a kind and that kind's settings, and handler_list, handler_get,
// handler_enable, handler_disable and handler_delete by the handler's id.

// handlerSet is handler_set as c: the kind and its settings on the script
// name, failing the test on a refusal. It returns the handler as handler_set
// reports it, its id under "handler".
func (c *client) handlerSet(name, kind string, args map[string]any) map[string]any {
	c.t.Helper()
	out, text := c.handlerSetRaw(name, kind, args)
	if out == nil {
		c.t.Fatalf("handler_set kind=%s on %s was refused: %s", kind, name, text)
	}
	return out
}

// handlerSetRaw is handler_set as c, returning the handler or, refused, nil
// and the refusal.
func (c *client) handlerSetRaw(name, kind string, args map[string]any) (map[string]any, string) {
	c.t.Helper()
	params := map[string]any{"command": "handler_set", "name": name, "kind": kind}
	for k, v := range args {
		params[k] = v
	}
	res, text, err := c.callRaw("manage_script", params)
	if err != nil {
		c.t.Fatalf("handler_set kind=%s on %s: %v", kind, name, err)
	}
	if res.IsError {
		return nil, text
	}
	return c.call("manage_script", map[string]any{"command": "handler_get", "handler": idOf(c.parse(text))}), ""
}

// scheduleSet gives the script name the schedule cron in zone with args,
// returning it. A criterion run again on a stack that kept the script finds
// the schedule it set before and gets that one, as schedule_set replaced it.
func (c *client) scheduleSet(name, cron, zone string, args map[string]any) map[string]any {
	c.t.Helper()
	settings := map[string]any{"cron": cron, "timezone": zone}
	if args != nil {
		settings["args"] = args
	}
	out, text := c.handlerSetRaw(name, "schedule", settings)
	if out != nil {
		return out
	}
	if strings.Contains(text, "already has schedule handler") {
		for _, h := range c.handlersOf(name, "schedule", "") {
			if h["cron"] == cron {
				return h
			}
		}
	}
	c.t.Fatalf("handler_set kind=schedule %q on %s was refused: %s", cron, name, text)
	return nil
}

// handlerOff turns a handler off, ignoring a refusal: a cleanup.
func (c *client) handlerOff(handler any) {
	id, _ := handler.(string)
	if id == "" {
		return
	}
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "handler_disable", "handler": id})
}

// schedulesOff turns every schedule of the script name off, ignoring a
// refusal: a cleanup.
func (c *client) schedulesOff(name string) {
	res, text, err := c.callRaw("manage_script", map[string]any{"command": "handler_list", "name": name, "kind": "schedule"})
	if err != nil || res.IsError {
		return
	}
	for _, h := range listOf(c.parse(text), "handlers") {
		c.handlerOff(h["handler"])
	}
}

// handlersOf is the script name's handlers of kind, each described in full,
// owner naming another person's script.
func (c *client) handlersOf(name, kind, owner string) []map[string]any {
	c.t.Helper()
	params := map[string]any{"command": "handler_list", "name": name}
	if kind != "" {
		params["kind"] = kind
	}
	if owner != "" {
		params["owner_email"] = owner
	}
	return listOf(c.call("manage_script", params), "handlers")
}

// endpointOf is the script name's endpoint as a criterion reads it: its one
// endpoint handler described in full, bound true, or bound false when it has
// none.
func (c *client) endpointOf(name, owner string) map[string]any {
	c.t.Helper()
	held := c.handlersOf(name, "endpoint", owner)
	if len(held) == 0 {
		return map[string]any{"name": name, "bound": false}
	}
	held[0]["bound"] = true
	return held[0]
}

// endpointEnable turns the script name's endpoint on or off, returning the
// handler as handler_enable reports it.
func (c *client) endpointEnable(name string, enabled bool) map[string]any {
	c.t.Helper()
	command := "handler_disable"
	if enabled {
		command = "handler_enable"
	}
	return c.call("manage_script", map[string]any{"command": command, "handler": c.endpointOf(name, "")["handler"]})
}

// webhookSet runs the script name for the webhook source, returning its
// handler.
func (c *client) webhookSet(name, source string) map[string]any {
	c.t.Helper()
	return c.handlerSet(name, "webhook", map[string]any{"event_source": source})
}

// webhookOff deletes the script name's webhook handler for source, ignoring
// a refusal: a cleanup, and how a script stops running for a source.
func (c *client) webhookOff(name, source string) {
	res, text, err := c.callRaw("manage_script", map[string]any{"command": "handler_list", "name": name, "kind": "webhook"})
	if err != nil || res.IsError {
		return
	}
	for _, h := range listOf(c.parse(text), "handlers") {
		if h["event_source"] == source {
			_, _, _ = c.callRaw("manage_script", map[string]any{"command": "handler_delete", "handler": h["handler"]})
		}
	}
}

// webhookSources is the webhook sources the script name runs for.
func (c *client) webhookSources(name string) []string {
	c.t.Helper()
	var out []string
	for _, h := range c.handlersOf(name, "webhook", "") {
		if s, ok := h["event_source"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// parse is a tool result's JSON object, failing the test when it is not one.
func (c *client) parse(text string) map[string]any {
	c.t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.t.Fatalf("a result is not a JSON object: %v\n%s", err, text)
	}
	return out
}

// idOf is a handler's id.
func idOf(h map[string]any) string {
	id, _ := h["handler"].(string)
	return id
}

// listOf is the list of objects under key.
func listOf(m map[string]any, key string) []map[string]any {
	items, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

// portalEndpointSet sets the script's endpoint through the portal: its
// endpoint handler changed with PUT /portal/handlers/{id} when it has one,
// added with POST /portal/scripts/{id}/handlers when it has none.
func (c *client) portalEndpointSet(scriptID string, body map[string]any) (int, map[string]any) {
	c.t.Helper()
	if status, page := c.portalEndpointOf(scriptID); status == http.StatusOK && page["bound"] == true {
		return c.rest(http.MethodPut, "/api/v1/portal/handlers/"+idOf(page), jsonBody(c.t, body))
	}
	withKind := map[string]any{"kind": "endpoint"}
	for k, v := range body {
		withKind[k] = v
	}
	return c.rest(http.MethodPost, "/api/v1/portal/scripts/"+scriptID+"/handlers", jsonBody(c.t, withKind))
}

// portalEndpointOf is the script's endpoint as its page reads it: its one
// endpoint handler, bound true, or bound false with what adding one offers.
func (c *client) portalEndpointOf(scriptID string) (int, map[string]any) {
	c.t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/handlers", http.NoBody)
	if status != http.StatusOK {
		return status, out
	}
	for _, h := range listOf(out, "handlers") {
		if h["kind"] == "endpoint" {
			h["bound"] = true
			return status, h
		}
	}
	page, _ := out["endpoint"].(map[string]any)
	if page == nil {
		page = map[string]any{}
	}
	page["bound"] = false
	return status, page
}

// succeeded reports a 2xx answer: an add answers 201, a change 200.
func succeeded(status int) bool { return status/100 == 2 }
