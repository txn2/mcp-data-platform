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
