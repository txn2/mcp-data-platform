package scripthash

import (
	"fmt"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// eval runs one expression with the module bound to hash.
func eval(t *testing.T, expr string) (starlark.Value, error) {
	t.Helper()
	thread := &starlark.Thread{Name: "test"}
	thread.SetMaxExecutionSteps(1000)
	v, err := starlark.EvalOptions(&syntax.FileOptions{}, thread, "test.star", expr, starlark.StringDict{Name: Module})
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	return v, nil
}

func TestDigests(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`hash.md5("")`, `"d41d8cd98f00b204e9800998ecf8427e"`},
		{`hash.sha1("abc")`, `"a9993e364706816aba3e25717850c26c9cd0d89d"`},
		{`hash.sha256("abc")`, `"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"`},
		{`hash.sha512("abc")[:16]`, `"ddaf35a193617aba"`},
		{`hash.md5(b"")`, `"d41d8cd98f00b204e9800998ecf8427e"`},
		// RFC 4231 test case 2.
		{`hash.hmac_sha256("Jefe", "what do ya want for nothing?")`, `"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"`},
		{`hash.hmac_sha1("key", "The quick brown fox jumps over the lazy dog")`, `"de7c9b85b8b78aa6bc8a7a36f70a90701c9db4d9"`},
		{`hash.hmac_sha512("key", "")[:16]`, `"84fa5aa0279bbc47"`},
		{`hash.crc32("The quick brown fox jumps over the lazy dog")`, `1095738169`},
		{`hash.fnv64("a")`, `12638187200555641996`},
		// The universe's hash(x), which the module took the name of.
		{`hash("a")`, `97`},
	}
	for _, c := range cases {
		got, err := eval(t, c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got.String() != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := map[string]string{
		`hash.md5(1)`:              "s must be a string or bytes, not int",
		`hash.md5()`:               "missing argument for s",
		`hash.hmac_sha256(1, "x")`: "key must be a string or bytes",
		`hash.hmac_sha256("k", 1)`: "s must be a string or bytes",
		`hash.hmac_sha256()`:       "in hash.hmac_sha256",
		`hash.crc32(None)`:         "s must be a string or bytes, not NoneType",
		`hash.crc32()`:             "in hash.crc32",
		`hash.fnv64([])`:           "not list",
		`hash.fnv64()`:             "in hash.fnv64",
		`hash.blake2`:              "no .blake2 field",
		`{hash: 1}`:                "unhashable",
	}
	for expr, want := range cases {
		_, err := eval(t, expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", expr, err, want)
		}
	}
}

// TestLargeValueCostsOneCall pins the reason the module exists: a megabyte
// hashes in one call, far inside a run's step budget.
func TestLargeValueCostsOneCall(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	thread.SetMaxExecutionSteps(100)
	env := starlark.StringDict{Name: Module, "page": starlark.String(strings.Repeat("x", 1<<20))}
	got, err := starlark.EvalOptions(&syntax.FileOptions{}, thread, "test.star", `hash.md5(page)`, env)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got.(starlark.String); !ok || len(string(s)) != 32 {
		t.Errorf("md5 of 1 MiB = %s", got)
	}
}

func TestModuleReadsAsAModule(t *testing.T) {
	if Module.Type() != "module" || Module.String() != "<module hash>" || Module.Name() != Name || !bool(Module.Truth()) {
		t.Errorf("module identity is %s %s %s", Module.Type(), Module.String(), Module.Name())
	}
	Module.Freeze()
	names := Module.AttrNames()
	if len(names) != len(Module.Members()) || names[0] != "crc32" {
		t.Errorf("AttrNames = %v", names)
	}
}
