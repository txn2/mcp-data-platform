package scriptlib

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// libs is a Source over "<name>@<version>" keys, counting reads.
type libs struct {
	src   map[string]string
	err   error
	reads int
}

func (l *libs) LibrarySource(_ context.Context, name string, version int) (string, error) {
	l.reads++
	if l.err != nil {
		return "", l.err
	}
	s, ok := l.src[fmt.Sprintf("%s@%d", name, version)]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}

func TestParseModule(t *testing.T) {
	ref, err := ParseModule("lib:date-windows@12")
	require.NoError(t, err)
	assert.Equal(t, Ref{Name: "date-windows", Version: 12}, ref)
	assert.Equal(t, "lib:date-windows@12", ref.String())

	for _, bad := range []string{"date-windows@1", "lib:date-windows", "lib:Dates@1", "lib:x@0", "lib:x@-1", "lib:x@latest", "lib:@1"} {
		_, err := ParseModule(bad)
		assert.Error(t, err, bad)
	}
	_, err = ParseModule("oops")
	assert.ErrorContains(t, err, `a library is loaded as "lib:<name>@<version>"`)
}

func TestLoadsAndRefs(t *testing.T) {
	src := "load(\"lib:a@1\", \"f\")\nload(\"lib:b@2\", \"g\")\nload(\"lib:a@1\", \"h\")\nload(\"nope\", \"x\")\n\ndef main():\n    print(1)\n"
	file, err := scriptdialect.Options.Parse("s", src, 0)
	require.NoError(t, err)
	loads := Loads(file)
	require.Len(t, loads, 4)
	assert.Equal(t, 2, loads[1].Line)
	assert.Error(t, loads[3].Err)
	assert.Equal(t, []Ref{{"a", 1}, {"b", 2}}, Refs(src), "each version once, in source order, the malformed one dropped")
	assert.Equal(t, []Ref{}, Refs("def ("), "a source that does not parse loads nothing")
	assert.False(t, IsLibrary(file))
	assert.False(t, SourceIsLibrary(src))
	assert.True(t, SourceIsLibrary("def f():\n    return 1\n"))
	assert.False(t, SourceIsLibrary("def ("))
}

// run executes source's main with the loader over src.
func run(t *testing.T, src Source, source string) (starlark.StringDict, error) {
	t.Helper()
	thread := &starlark.Thread{Name: "test", Load: Loader(context.Background(), src, starlark.StringDict{})}
	return starlark.ExecFileOptions(scriptdialect.Options, thread, "script", source, nil) //nolint:wrapcheck // the interpreter's error is what the test reads
}

func TestLoader_RunsThePinnedVersionOnce(t *testing.T) {
	l := &libs{src: map[string]string{
		"scale@1": "def scale(n):\n    return n * 2\n",
		"scale@2": "def scale(n):\n    return n * 3\n",
		"twice@1": "load(\"lib:scale@1\", \"scale\")\n\ndef twice(n):\n    return scale(scale(n))\n",
	}}
	globals, err := run(t, l, "load(\"lib:scale@1\", \"scale\")\nload(\"lib:twice@1\", \"twice\")\nA = scale(21)\nB = twice(1)\n")
	require.NoError(t, err)
	assert.Equal(t, "42", globals["A"].String())
	assert.Equal(t, "4", globals["B"].String())
	assert.Equal(t, 2, l.reads, "scale@1 is read once though two modules load it")

	globals, err = run(t, l, "load(\"lib:scale@2\", \"scale\")\nA = scale(21)\n")
	require.NoError(t, err)
	assert.Equal(t, "63", globals["A"].String())
}

func TestLoader_Refusals(t *testing.T) {
	cases := map[string]struct {
		src  Source
		load string
		want string
	}{
		"malformed":      {&libs{}, "x", "names no library"},
		"missing":        {&libs{src: map[string]string{}}, "lib:x@1", "lib:x@1 does not exist"},
		"no store":       {nil, "lib:x@1", "no library store is available"},
		"store failure":  {&libs{err: errors.New("down")}, "lib:x@1", "reading lib:x@1: down"},
		"does not parse": {&libs{src: map[string]string{"x@1": "def ("}}, "lib:x@1", "parsing lib:x@1"},
		"does not resolve": {
			&libs{src: map[string]string{"x@1": "def f():\n    return platform\n"}}, "lib:x@1", "resolving lib:x@1",
		},
		"fails as it loads": {&libs{src: map[string]string{"x@1": "X = 1 // 0\n"}}, "lib:x@1", "division by zero"},
		"a cycle": {&libs{src: map[string]string{
			"a@1": "load(\"lib:b@1\", \"g\")\ndef f():\n    return 1\n",
			"b@1": "load(\"lib:a@1\", \"f\")\ndef g():\n    return 1\n",
		}}, "lib:a@1", "a load cycle is not allowed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := run(t, tc.src, fmt.Sprintf("load(%q, \"f\")\n", tc.load))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLoader_RefusesAChainPastMaxDepth(t *testing.T) {
	src := map[string]string{}
	for i := 0; i <= MaxDepth; i++ {
		src[fmt.Sprintf("l%d@1", i)] = fmt.Sprintf("load(\"lib:l%d@1\", \"f\")\n", i+1)
	}
	src[fmt.Sprintf("l%d@1", MaxDepth+1)] = "def f():\n    return 1\n"
	_, err := run(t, &libs{src: src}, "load(\"lib:l0@1\", \"f\")\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("more than %d libraries deep", MaxDepth))
}

func TestCheck(t *testing.T) {
	l := &libs{src: map[string]string{
		"a@1": "def f():\n    return 1\n",
		"b@1": "load(\"lib:a@1\", \"f\")\ndef g():\n    return f()\n",
		"c@1": "load(\"lib:d@1\", \"f\")\n",
		"d@1": "load(\"lib:c@1\", \"f\")\n",
		"e@1": "def (",
		"m@1": "load(\"lib:gone@1\", \"f\")\n",
	}}
	ctx := context.Background()
	assert.Empty(t, Check(ctx, l, "load(\"lib:b@1\", \"g\")\nload(\"lib:a@1\", \"f\")\n", ""))
	assert.Nil(t, Check(ctx, l, "def (", ""), "a source that does not parse is the validator's to report")

	cases := map[string]struct {
		source, self, want string
		src                Source
	}{
		"missing version": {source: "load(\"lib:a@2\", \"f\")\n", want: "lib:a@2 does not exist"},
		"missing deep":    {source: "load(\"lib:m@1\", \"f\")\n", want: "lib:gone@1 does not exist"},
		"malformed":       {source: "load(\"a\", \"f\")\n", want: "names no library"},
		"itself":          {source: "load(\"lib:a@1\", \"f\")\n", self: "a", want: "a library cannot load itself"},
		"cycle":           {source: "load(\"lib:c@1\", \"f\")\n", want: "load cycle: lib:c@1 loads lib:d@1 loads lib:c@1"},
		"does not parse":  {source: "load(\"lib:e@1\", \"f\")\n", want: "lib:e@1 does not parse"},
		"no store":        {source: "load(\"lib:a@1\", \"f\")\n", want: "cannot be checked", src: nilSource{}},
		"a failing store": {source: "load(\"lib:a@1\", \"f\")\n", want: "reading lib:a@1: down", src: &libs{err: errors.New("down")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := Source(l)
			if tc.src != nil {
				src = tc.src
			}
			if _, isNil := tc.src.(nilSource); isNil {
				src = nil
			}
			problems := Check(ctx, src, tc.source, tc.self)
			require.Len(t, problems, 1)
			assert.Equal(t, 1, problems[0].Line)
			assert.Contains(t, problems[0].Message, tc.want)
		})
	}
}

// nilSource marks a case that checks with no Source at all.
type nilSource struct{}

func (nilSource) LibrarySource(context.Context, string, int) (string, error) { return "", nil }

func TestCheck_RefusesAChainPastMaxDepth(t *testing.T) {
	src := map[string]string{}
	for i := 0; i <= MaxDepth; i++ {
		src[fmt.Sprintf("l%d@1", i)] = fmt.Sprintf("load(\"lib:l%d@1\", \"f\")\n", i+1)
	}
	problems := Check(context.Background(), &libs{src: src}, "load(\"lib:l0@1\", \"f\")\n", "")
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, fmt.Sprintf("goes more than %d libraries deep", MaxDepth))
}
