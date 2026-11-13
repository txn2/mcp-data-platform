package epcmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
)

func TestPageAllowed(t *testing.T) {
	hookErr := errors.New("this script is a part of no application")
	page := &epbinding.Binding{ScriptID: "script_1", ResponseMode: epbinding.ResponsePage}
	api := &epbinding.Binding{ScriptID: "script_1", ResponseMode: epbinding.ResponseAPI}

	tests := []struct {
		name    string
		binding *epbinding.Binding
		hook    func(ctx context.Context, scriptID string) error
		want    error
		wantMsg string
	}{
		{name: "a nil hook refuses a page", binding: page, wantMsg: "serves no browser pages"},
		{name: "a nil hook leaves an api binding alone", binding: api},
		{name: "an api binding never asks the hook", binding: api, hook: func(context.Context, string) error { return hookErr }},
		{name: "no mode is not a page", binding: &epbinding.Binding{ScriptID: "script_1"}, hook: func(context.Context, string) error { return hookErr }},
		{name: "a hook error is returned", binding: page, hook: func(context.Context, string) error { return hookErr }, want: hookErr},
		{name: "a hook that allows it", binding: page, hook: func(context.Context, string) error { return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&Handler{PageAllowed: tt.hook}).pageAllowed(context.Background(), tt.binding)
			switch {
			case tt.want != nil:
				require.ErrorIs(t, err, tt.want)
			case tt.wantMsg != "":
				require.ErrorContains(t, err, tt.wantMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestPageAllowed_TheHookIsAskedAboutTheBindingsScript(t *testing.T) {
	var asked string
	h := &Handler{PageAllowed: func(_ context.Context, scriptID string) error {
		asked = scriptID
		return nil
	}}
	require.NoError(t, h.pageAllowed(context.Background(), &epbinding.Binding{ScriptID: "script_9", ResponseMode: epbinding.ResponsePage}))
	assert.Equal(t, "script_9", asked)
}

// TestSet_CarriesThePageListsAndTheAddressLimit is #2554 and #2556 through
// an endpoint handler: the three origin lists and ip_rate_limit are stored and
// reported, a limit on a deployment that trusts no proxy is warned of, and 0
// removes the limit.
func TestSet_CarriesThePageListsAndTheAddressLimit(t *testing.T) {
	s := newStore()
	h := handler(s)
	h.PageAllowed = func(context.Context, string) error { return nil }
	h.NoTrustedProxies = true
	limit := 30
	out, err := h.Set(context.Background(), setReq(Input{
		Endpoint: "shop", AuthMode: epbinding.AuthPublic, ResponseMode: epbinding.ResponsePage, Path: new("/{route...}"),
		PageFrameOrigins: []string{"https://js.stripe.com", "https://*.js.stripe.com"}, PageConnectOrigins: []string{"https://api.stripe.com"},
		PageFormOrigins: []string{"https://pay.example.test"}, IPRateLimit: &limit,
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{"https://js.stripe.com", "https://*.js.stripe.com"}, out["page_frame_origins"])
	assert.Equal(t, []string{"https://api.stripe.com"}, out["page_connect_origins"])
	assert.Equal(t, []string{"https://pay.example.test"}, out["page_form_origins"])
	assert.Equal(t, 30, out["ip_rate_limit"])
	assert.Equal(t, NoTrustedProxiesWarning, out["warning"])
	assert.Equal(t, "/{route...}", s.only(t, "s1").Path)

	h.NoTrustedProxies = false
	out, err = h.Set(context.Background(), setReq(Input{}))
	require.NoError(t, err)
	assert.NotContains(t, out, "warning", "trusted proxies are set: nothing to warn of")
	assert.Equal(t, 30, out["ip_rate_limit"], "an omitted limit is kept")

	off := 0
	out, err = h.Set(context.Background(), setReq(Input{IPRateLimit: &off}))
	require.NoError(t, err)
	assert.Equal(t, 0, out["ip_rate_limit"])

	_, err = h.Set(context.Background(), setReq(Input{ResponseMode: epbinding.ResponseAPI, PageFrameOrigins: []string{"https://js.stripe.com"}}))
	require.ErrorContains(t, err, "page_frame_origins applies to an endpoint with response_mode page")
}
