package scriptlib

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// files is a Source that also answers res: modules, counting reads.
type files struct {
	libs
	text  map[string]string
	reads int
}

func (f *files) ResourceText(_ context.Context, module string) (string, error) {
	f.reads++
	t, ok := f.text[module]
	if !ok {
		return "", errors.New("no such file")
	}
	return t, nil
}

const pageModule = "res:global/flows/sync/page.js"

// TestLoader_BindsAFilesText is #2113: a page script kept as its own file is
// loaded as one string constant, read once however many loads name it.
func TestLoader_BindsAFilesText(t *testing.T) {
	f := &files{text: map[string]string{pageModule: "return document.title;\n"}}
	globals, err := run(t, f, "load(\""+pageModule+"\", PAGE_JS = \"text\")\nload(\""+pageModule+"\", AGAIN = \"text\")\nN = len(PAGE_JS)\nSAME = AGAIN\n")
	require.NoError(t, err)
	assert.Equal(t, "23", globals["N"].String())
	assert.Equal(t, `"return document.title;\n"`, globals["SAME"].String())
	assert.Equal(t, 1, f.reads)
}

func TestLoader_RefusesWhatAFileLoadCannotBe(t *testing.T) {
	_, err := run(t, &libs{}, "load(\""+pageModule+"\", PAGE_JS = \"text\")\n")
	assert.ErrorContains(t, err, "no managed-resource library is available")

	_, err = run(t, &files{}, "load(\""+pageModule+"\", PAGE_JS = \"text\")\n")
	assert.ErrorContains(t, err, "no such file")

	inLib := &files{libs: libs{src: map[string]string{"pages@1": "load(\"" + pageModule + "\", P = \"text\")\n"}}}
	_, err = run(t, inLib, "load(\"lib:pages@1\", \"P\")\n")
	assert.ErrorContains(t, err, "a library is pure code and loads no resource")
}

func TestResourceLoadsAreReadStatically(t *testing.T) {
	src := "load(\"" + pageModule + "\", PAGE_JS = \"text\")\nload(\"res:global/x\", A = \"text\")\n" +
		"load(\"" + pageModule + "\", B = \"body\")\nload(\"lib:a@1\", \"f\")\n\ndef main():\n    print(1)\n"
	file, err := scriptdialect.Options.Parse("s", src, 0)
	require.NoError(t, err)
	loads := Loads(file)
	require.Len(t, loads, 4)
	assert.Equal(t, pageModule, loads[0].Resource)
	require.NoError(t, loads[0].Err)
	assert.ErrorContains(t, loads[1].Err, "names no file")
	assert.ErrorContains(t, loads[2].Err, `write NAME = "text", not "body"`)
	assert.Equal(t, []Ref{{"a", 1}}, Refs(src), "a file load is not a library")
	assert.Equal(t, []string{pageModule}, ResourceModules(src))
	assert.Equal(t, []string{}, ResourceModules("def ("))
}

func TestCheck_AFileLoadIsTheScriptsAlone(t *testing.T) {
	l := &libs{src: map[string]string{"pages@1": "load(\"" + pageModule + "\", P = \"text\")\n"}}
	assert.Empty(t, Check(context.Background(), l, "load(\""+pageModule+"\", P = \"text\")\n\ndef main():\n    print(P)\n", ""))

	problems := Check(context.Background(), l, "load(\"lib:pages@1\", \"P\")\n\ndef main():\n    print(P)\n", "")
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "a library is pure code and loads no resource")

	problems = Check(context.Background(), l, "load(\""+pageModule+"\", P = \"text\")\n", "pages")
	require.Len(t, problems, 1, "a library being saved may not load one either")
}
