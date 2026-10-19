package scriptdialect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

const handlerSource = `
def main():
    fail("main must not run when a handler is named")

def handle(req):
    return {"echo": req, "status": 200}
`

func execHandler(t *testing.T, source string, h *Handler) error {
	t.Helper()
	_, err := Exec(&starlark.Thread{Name: "test"}, "endpoint", source, starlark.StringDict{}, Hooks{Handler: h})
	return err //nolint:wrapcheck // the test reads Exec's own error
}

func TestExecCallsTheHandlerWithItsArgumentsInPlaceOfMain(t *testing.T) {
	var got starlark.Value
	err := execHandler(t, handlerSource, &Handler{
		Name:     "handle",
		Args:     starlark.Tuple{starlark.String("ping")},
		Returned: func(v starlark.Value) error { got = v; return nil },
	})
	require.NoError(t, err)
	d, ok := got.(*starlark.Dict)
	require.True(t, ok, "the handler's return is handed back: %v", got)
	echo, _, _ := d.Get(starlark.String("echo"))
	assert.Equal(t, starlark.String("ping"), echo)
}

func TestExecRefusesAHandlerTheScriptDoesNotDefine(t *testing.T) {
	err := execHandler(t, "def main():\n    pass\n", &Handler{Name: "handle", Args: starlark.Tuple{starlark.None}})
	require.EqualError(t, err, "the script defines no function handle")
}

func TestExecFailsWhenReturnedRefusesTheValue(t *testing.T) {
	err := execHandler(t, handlerSource, &Handler{
		Name: "handle", Args: starlark.Tuple{starlark.None},
		Returned: func(starlark.Value) error { return assert.AnError },
	})
	require.ErrorIs(t, err, assert.AnError)
}

// A Program compiles its source once: a later execution reuses the first
// compilation, which is what lets a cached version skip parsing.
func TestProgramCompilesOnceAndIsReused(t *testing.T) {
	prog := &Program{}
	var first, second starlark.Value
	require.NoError(t, execHandler(t, "def handle(req):\n    return 1\n", &Handler{
		Name: "handle", Args: starlark.Tuple{starlark.None}, Program: prog,
		Returned: func(v starlark.Value) error { first = v; return nil },
	}))
	require.NoError(t, execHandler(t, "def handle(req):\n    return 2\n", &Handler{
		Name: "handle", Args: starlark.Tuple{starlark.None}, Program: prog,
		Returned: func(v starlark.Value) error { second = v; return nil },
	}))
	assert.Equal(t, starlark.MakeInt(1), first)
	assert.Equal(t, starlark.MakeInt(1), second, "the second execution ran the first compilation")
}

func TestProgramKeepsItsCompileError(t *testing.T) {
	prog := &Program{}
	for range 2 {
		err := execHandler(t, "def handle(req:\n", &Handler{Name: "handle", Program: prog})
		require.Error(t, err)
	}
}

// A cached program still formats with the platform's % and ends main through
// the hook a caller measures with, as the uncached path does.
func TestProgramBindsFormattingAndMainEnd(t *testing.T) {
	ended := 0
	var got starlark.Value
	_, err := Exec(&starlark.Thread{Name: "test"}, "endpoint",
		"def main():\n    return 1\n\ndef handle(req):\n    return \"%05.1f\" % 3.14159\n",
		starlark.StringDict{}, Hooks{
			AtMainEnd: func(*starlark.Thread) error { ended++; return nil },
			Handler: &Handler{
				Name: "handle", Args: starlark.Tuple{starlark.None}, Program: &Program{},
				Returned: func(v starlark.Value) error { got = v; return nil },
			},
		})
	require.NoError(t, err)
	assert.Equal(t, starlark.String("003.1"), got)
	assert.Zero(t, ended, "main is not called when a handler is named")
}

func TestExecWithoutAMainEndHookStillReturnsMainsValue(t *testing.T) {
	var got starlark.Value
	_, err := Exec(&starlark.Thread{Name: "test"}, "s", "def main():\n    return 7\n\ndef t():\n    return main()\n",
		starlark.StringDict{}, Hooks{Handler: &Handler{
			Name: "t", Returned: func(v starlark.Value) error { got = v; return nil },
		}})
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt(7), got)
}
