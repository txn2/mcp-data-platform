package scripttoken

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// opener opens every value it is handed by reading "enc:" off it, for the
// persona "credit" and the administrator.
type opener struct{ got DecryptRequest }

func (o *opener) Decrypt(_ context.Context, req DecryptRequest) (values map[string]any, opened []string, err error) {
	o.got = req
	if !req.Admin && req.Persona != "credit" {
		return nil, nil, errors.New("persona " + req.Persona + " may not decrypt")
	}
	stored, _ := req.Payload["values"].(map[string]any)
	values = maps.Clone(stored)
	if values == nil {
		values = map[string]any{}
	}
	values["ssn"] = "123-45-6789"
	return values, []string{"ssn"}, nil
}

func execDecrypt(t *testing.T, h Host, src string) (starlark.Value, error) {
	t.Helper()
	env := starlark.StringDict{"platform": &starlarkstruct.Module{Name: "platform", Members: starlark.StringDict{
		"decrypt": starlark.NewBuiltin(CapabilityDecrypt, h.Decrypt),
	}}}
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t.star", src, env)
	if err != nil {
		return nil, err //nolint:wrapcheck // the test reads the binding's error
	}
	return globals["result"], nil
}

const decryptSrc = `result = platform.decrypt("apply", {"values": {"name": "Ana", "ssn": "enc:x"}, "sensitive": ["ssn"]})`

// #2179: a run whose persona the form admits decrypts, as its author's
// persona; the recording keeps the answer with the opened value redacted.
func TestDecrypt_ARunDecryptsAsItsAuthorsPersona(t *testing.T) {
	op := &opener{}
	iss := issuer(nil)
	iss.SetDecrypter(op)
	var recorded map[string]any
	h := Host{
		Ctx: context.Background(), Live: iss.Bind(Binding{ScriptID: "scr_1"}, []string{"credit"}),
		Record: func(tool string, _, out map[string]any, err error) {
			assert.Equal(t, CapabilityDecrypt, tool)
			require.NoError(t, err)
			recorded = out
		},
	}
	v, err := execDecrypt(t, h, decryptSrc)
	require.NoError(t, err)
	d, ok := v.(*starlark.Dict)
	require.True(t, ok)
	ssn, _, _ := d.Get(starlark.String("ssn"))
	assert.Equal(t, starlark.String("123-45-6789"), ssn)
	assert.Equal(t, "credit", op.got.Persona)
	assert.Equal(t, "apply", op.got.Form)
	assert.Equal(t, "scr_1", op.got.At.ScriptID)
	assert.Equal(t, map[string]any{"values": map[string]any{"name": "Ana", "ssn": Redacted}, "opened": []string{"ssn"}}, recorded,
		"no recording holds the decrypted value")
}

func TestDecrypt_Refusals(t *testing.T) {
	iss := issuer(nil)
	iss.SetDecrypter(&opener{})
	_, err := execDecrypt(t, Host{Ctx: context.Background(), Live: iss.Bind(Binding{ScriptID: "s"}, []string{"sales"})}, decryptSrc)
	require.ErrorContains(t, err, "persona sales may not decrypt")

	bare := issuer(nil)
	_, err = execDecrypt(t, Host{Ctx: context.Background(), Live: bare.Bind(Binding{ScriptID: "s"}, nil)}, decryptSrc)
	require.ErrorContains(t, err, "stores no form submissions")

	_, err = execDecrypt(t, Host{Live: &fakeCaller{}}, `result = platform.decrypt("", {})`)
	require.ErrorContains(t, err, "form is empty")
	_, err = execDecrypt(t, Host{Live: &fakeCaller{}}, `result = platform.decrypt("apply")`)
	require.ErrorContains(t, err, "missing argument")
	_, err = execDecrypt(t, Host{Live: &fakeCaller{}}, `result = platform.decrypt("apply", {1: 2})`)
	require.ErrorContains(t, err, "payload")
	_, err = execDecrypt(t, Host{Live: &fakeCaller{}}, `result = platform.decrypt("apply", "not json")`)
	require.ErrorContains(t, err, "not a JSON object")
	_, err = execDecrypt(t, Host{Live: &fakeCaller{}}, `result = platform.decrypt("apply", 5)`)
	require.ErrorContains(t, err, "not a int")
	_, err = execDecrypt(t, Host{Live: &fakeCaller{out: map[string]any{"values": map[string]any{"f": func() {}}}}}, decryptSrc)
	require.ErrorContains(t, err, "decrypt")

	var nilIssuer *Issuer
	nilIssuer.SetDecrypter(&opener{})
}

// A test answers a decrypt from its declared answers and records nothing.
func TestDecrypt_ATestAnswersFromTheReplay(t *testing.T) {
	replay := &fakeCaller{out: map[string]any{"values": map[string]any{"ssn": "declared"}, "opened": []any{"ssn"}}}
	recorded := false
	v, err := execDecrypt(t, Host{
		Ctx: context.Background(), Testing: true, Replay: replay,
		Record: func(string, map[string]any, map[string]any, error) { recorded = true },
	}, decryptSrc)
	require.NoError(t, err)
	d, ok := v.(*starlark.Dict)
	require.True(t, ok)
	ssn, _, _ := d.Get(starlark.String("ssn"))
	assert.Equal(t, starlark.String("declared"), ssn)
	assert.False(t, recorded)
}

func TestRedact(t *testing.T) {
	assert.Nil(t, redact(nil))
	assert.Equal(t, map[string]any{"values": map[string]any{"a": Redacted, "b": 1}, "opened": []string{"a", "c"}},
		redact(map[string]any{"values": map[string]any{"a": "x", "b": 1}, "opened": []any{"a", "c", 3}}))
	assert.Nil(t, names(5))
}

// The payload column as JSON text decrypts as its dict does.
func TestDecrypt_APayloadAsJSONText(t *testing.T) {
	op := &opener{}
	iss := issuer(nil)
	iss.SetDecrypter(op)
	h := Host{Ctx: context.Background(), Live: iss.Bind(Binding{ScriptID: "s"}, []string{"credit"})}
	_, err := execDecrypt(t, h, `result = platform.decrypt("apply", '{"values": {"ssn": "enc:x"}, "sensitive": ["ssn"]}')`)
	require.NoError(t, err)
	assert.Equal(t, []any{"ssn"}, op.got.Payload["sensitive"])
}
