package eprun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// #2517: the application an endpoint belongs to is read with the request's
// stamp, so a page origin can refuse an endpoint of another application.
func TestResolve_TheTargetNamesTheApplicationItsStampNames(t *testing.T) {
	bindings := newBindings(&epbinding.Binding{Name: "shop", ScriptID: "s1", Enabled: true})
	scripts := &memScripts{sc: &script.Script{ID: "s1", Name: "shop"}, versions: map[int]*script.Version{}}
	scripts.save("def handle(req):\n    return {}\n")
	r := &Runner{Bindings: bindings, Scripts: scripts}

	stamp := bindings.stamps["shop"]
	stamp.Application = "app_shop"
	bindings.stamps["shop"] = stamp

	target, err := r.Resolve(context.Background(), "shop")
	require.NoError(t, err)
	assert.Equal(t, "app_shop", target.ApplicationID())
}

func TestRequestValue_CarriesTheNonceOnlyWhenSet(t *testing.T) {
	tests := []struct {
		name  string
		nonce string
		want  starlark.Value
	}{
		{name: "a page request reads its nonce", nonce: "abc123", want: starlark.String("abc123")},
		{name: "any other request has no such key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := requestValue(Request{Method: "GET", Path: "/", CSPNonce: tt.nonce})
			require.NoError(t, err)
			dict, ok := v.(*starlark.Dict)
			require.True(t, ok)

			got, found, err := dict.Get(starlark.String("csp_nonce"))
			require.NoError(t, err)
			assert.Equal(t, tt.want != nil, found)
			if tt.want != nil {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
