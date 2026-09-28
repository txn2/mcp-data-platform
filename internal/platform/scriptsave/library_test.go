package scriptsave

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// libraries is a scriptlib.Source over a map of "<name>@<version>" to source.
type libraries map[string]string

func (l libraries) LibrarySource(_ context.Context, name string, version int) (string, error) {
	src, ok := l[fmt.Sprintf("%s@%d", name, version)]
	if !ok {
		return "", scriptlib.ErrNotFound
	}
	return src, nil
}

const doubler = `"""Doubles numbers."""

def double(n):
    """Doubles n."""
    return n * 2

def test_double():
    """Doubles two."""
    assert.eq(double(2), 4)
`

const usesDoubler = `load("lib:doubler@1", "double")

def main():
    """Reports a doubled number."""
    platform.result(double(21))

def test_main():
    """Reports 42."""
    main()
    assert.eq(testing.outputs().result, 42)
`

func TestALibraryIsSavedWithItsTests(t *testing.T) {
	res := (&Gate{Libraries: libraries{}}).Check(context.Background(), Request{Name: "doubler", Source: doubler, Caller: jane})
	require.False(t, res.Refused(), res.Refusal)
	require.NotNil(t, res.Tests)
	assert.Equal(t, 1, res.Tests.Passed)
}

func TestAScriptLoadingALibraryRunsItsPinnedCodeInItsTests(t *testing.T) {
	g := &Gate{Libraries: libraries{"doubler@1": doubler}}
	res := g.Check(context.Background(), Request{Name: "answer", Source: usesDoubler, Caller: jane})
	require.False(t, res.Refused(), res.Refusal)

	// Version 2 triples; the script pinned to @1 still doubles, so its test
	// still sees 42.
	g.Libraries = libraries{"doubler@1": doubler, "doubler@2": "def double(n):\n    \"\"\"Triples.\"\"\"\n    return n * 3\n"}
	res = g.Check(context.Background(), Request{Name: "answer", Source: usesDoubler, Caller: jane})
	require.False(t, res.Refused(), res.Refusal)
}

func TestALoadOfAMissingLibraryOrVersionIsRefused(t *testing.T) {
	g := &Gate{Libraries: libraries{"doubler@1": doubler}}
	for _, module := range []string{"lib:doubler@2", "lib:nothing@1"} {
		src := fmt.Sprintf("load(%q, \"double\")\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    platform.result(double(1))\n", module)
		res := g.Check(context.Background(), Request{Name: "answer", Source: src, Caller: jane})
		require.True(t, res.Refused(), module)
		assert.Contains(t, res.Refusal, "line 1: library "+module+" does not exist")
		assert.Nil(t, res.Tests, "refused before the tests run")
	}
}

func TestALoadCycleIsRefused(t *testing.T) {
	// b@1 loads a@1, and a@1 loads b@1: the stored pair can only arise
	// outside the gate, but a save reaching it is refused naming the cycle.
	g := &Gate{Libraries: libraries{
		"a@1": "load(\"lib:b@1\", \"f\")\n",
		"b@1": "load(\"lib:a@1\", \"g\")\n\ndef f():\n    \"\"\"Doc.\"\"\"\n    return 1\n",
	}}
	src := "load(\"lib:b@1\", \"f\")\n\ndef main():\n    \"\"\"Doc.\"\"\"\n    platform.result(f())\n"
	res := g.Check(context.Background(), Request{Name: "answer", Source: src, Caller: jane})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "load cycle: lib:b@1 loads lib:a@1 loads lib:b@1")

	self := "load(\"lib:a@2\", \"f\")\n\ndef g():\n    \"\"\"Doc.\"\"\"\n    return f()\n"
	res = g.Check(context.Background(), Request{Name: "a", Source: self, Caller: jane, Existing: &script.Script{Name: "a", Library: true, Source: "def g():\n    return 1\n"}})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "a library cannot load itself")
}

func TestAScriptAndALibraryDoNotTurnIntoEachOther(t *testing.T) {
	g := &Gate{Libraries: libraries{}}
	res := g.Check(context.Background(), Request{
		Name: "answer", Source: doubler, Caller: jane,
		Existing: &script.Script{Name: "answer", Source: usesDoubler},
	})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "would make it a library")

	res = g.Check(context.Background(), Request{
		Name: "doubler", Source: usesDoubler, Caller: jane,
		Existing: &script.Script{Name: "doubler", Library: true, Source: doubler},
	})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "this is a library")
}

func TestANewLibraryNeedsAnUnusedName(t *testing.T) {
	g := &Gate{Libraries: libraries{"doubler@1": doubler}}
	res := g.Check(context.Background(), Request{Name: "doubler", Source: doubler, Caller: jane})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, `a library named "doubler" already exists`)

	// Its owner's next version is not a new library.
	res = g.Check(context.Background(), Request{
		Name: "doubler", Source: doubler, Caller: jane,
		Existing: &script.Script{Name: "doubler", Library: true, Source: doubler},
	})
	assert.False(t, res.Refused(), res.Refusal)
}

func TestALibraryNamingPlatformIsRefusedNamingTheLine(t *testing.T) {
	src := "def rows():\n    \"\"\"Reads.\"\"\"\n    return platform.query(\"SELECT 1\")\n"
	res := (&Gate{}).Check(context.Background(), Request{Name: "reader", Source: src, Caller: jane})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "line 3: the source defines no main(), so it is a library, and a library may not name platform (library-effect)")
}
