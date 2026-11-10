package siterender

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
)

func TestSiteListsItsPages(t *testing.T) {
	c, idx := renderTree(t, map[string]string{
		"_layouts/base.html": baseLayout,
		"a.md":               "---\ntitle: A\ncategories: [Guides]\n---\nbody",
		"b.html":             "---\ntitle: B\npermalink: /bee/\n---\nbody",
		"gen.html":           "---\ntitle: Gen\npermalink: /x-{{ page.title }}/\n---\nbody",
		"list.html":          "---\ntitle: L\n---\n{% for p in site.pages %}{{ p.title }}={{ p.url }};{% endfor %}{% for t in site.categories %}{{ t.name }}{% endfor %}",
	})
	got := c.files["list.html"]
	assert.Contains(t, got, "A=/a/;")
	assert.Contains(t, got, "B=/bee/;")
	assert.Contains(t, got, "Guides")
	for _, e := range idx.Entries {
		if e.Output == "list.html" {
			assert.Contains(t, e.Inputs, inputPages)
		}
	}
}

func TestSizeOf(t *testing.T) {
	assert.Zero(t, sizeOf(nil))
	assert.EqualValues(t, 3, sizeOf("abc"))
	assert.EqualValues(t, 2, sizeOf([]byte("ab")))
	assert.EqualValues(t, 2*sliceElementBytes, sizeOf([]any{1, 2}))
	assert.Zero(t, sizeOf(42))
}

func TestIncludeArguments(t *testing.T) {
	for _, src := range []string{`{% include "a", b %}`, `{% include "a", b: | %}`, `{% include | %}`, `{% include 3 %}`} {
		_, err := renderText(t, Limits{}, src, map[string]any{}, memStore{})
		require.Error(t, err, src)
	}
	_, err := renderText(t, Limits{}, `{% include "a.html", b: nope | missing_filter %}`, map[string]any{}, memStore{"_includes/a.html": "x"})
	require.Error(t, err)
	_, err = renderText(t, Limits{}, `{% include "absent.html" %}`, map[string]any{}, memStore{})
	require.ErrorContains(t, err, "absent.html")
}

func TestRangeDetectionReadsStringsAsTheEngineDoes(t *testing.T) {
	assert.False(t, hasRange(`"a..b"`))
	assert.False(t, hasRange(`'a..b'`))
	assert.False(t, hasRange(`"a\"..b"`), "a backslash escapes a quote in double quotes")
	assert.True(t, hasRange(`'a\'..b`), "and does not in single quotes")
	assert.True(t, hasRange(`(1..2)`))
	assert.False(t, hasRange(`a.b.c`))
}

type failingMarkdown struct{ goldmark.Markdown }

func (failingMarkdown) Convert([]byte, io.Writer, ...parser.ParseOption) error {
	return errors.New("cannot convert")
}

func TestMarkdownFailureIsReported(t *testing.T) {
	original := markdown
	markdown = failingMarkdown{}
	defer func() { markdown = original }()
	_, err := toHTML([]byte("x"))
	require.ErrorContains(t, err, "markdown")
}

type failingSink struct {
	*capture
	put, copyErr, keep error
}

func (s failingSink) Put(p string, b []byte) error {
	if s.put != nil {
		return s.put
	}
	return s.capture.Put(p, b)
}

func (s failingSink) Copy(p string) error {
	if s.copyErr != nil {
		return s.copyErr
	}
	return s.capture.Copy(p)
}

func (s failingSink) Keep(p string) error {
	if s.keep != nil {
		return s.keep
	}
	return s.capture.Keep(p)
}

func TestARenderStopsWhenItCannotGoOn(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")
	files := map[string]string{"_layouts/base.html": baseLayout, "a.html": "---\n---\na", "b.png": "x"}

	t.Run("a file cannot be stored", func(t *testing.T) {
		_, err := Render(ctx, tree(files), Options{}, failingSink{capture: newCapture(), put: boom})
		require.ErrorIs(t, err, boom)
	})
	t.Run("a file cannot be copied", func(t *testing.T) {
		_, err := Render(ctx, tree(files), Options{}, failingSink{capture: newCapture(), copyErr: boom})
		require.ErrorIs(t, err, boom)
	})
	t.Run("an output cannot be kept", func(t *testing.T) {
		_, idx := renderTree(t, files)
		_, err := Render(ctx, tree(files), Options{Previous: idx}, failingSink{capture: newCapture(), keep: boom})
		require.ErrorIs(t, err, boom)
	})
	t.Run("the render is canceled", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := Render(canceled, tree(files), Options{}, newCapture())
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("the output runs out", func(t *testing.T) {
		big := map[string]string{"_layouts/base.html": baseLayout, "a.html": "---\n---\n" + strings.Repeat("x", 100), "b.html": "---\n---\nb"}
		_, err := Render(ctx, tree(big), Options{Limits: Limits{TotalBytes: 10}}, newCapture())
		var b *BudgetError
		require.ErrorAs(t, err, &b)
		assert.Contains(t, b.Resource, "output")
	})
	t.Run("the steps run out with nothing else to report", func(t *testing.T) {
		r := &run{ctx: ctx, limits: Limits{Steps: 1}.withDefaults(), meter: newMeter(ctx, Limits{Steps: 1}.withDefaults())}
		r.meter.steps = 5
		var b *BudgetError
		require.ErrorAs(t, r.exhaustion(), &b)
		assert.Equal(t, "steps", b.Resource)
	})
}

func TestFailuresAreCapped(t *testing.T) {
	files := map[string]string{"_layouts/base.html": baseLayout}
	for i := range 10 {
		files[fmt.Sprintf("p%d.html", i)] = "---\nlayout: nope\n---\nx"
	}
	_, err := Render(context.Background(), tree(files), Options{Limits: Limits{Failures: 3}}, newCapture())
	var re *RenderError
	require.ErrorAs(t, err, &re)
	assert.Len(t, re.Failures, 3)
	assert.True(t, re.Truncated)
	assert.Contains(t, re.Error(), "and more")
}

func TestPermalinkAndPathRules(t *testing.T) {
	_, err := Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout, "_data/x/a.json": "{}", "p.html": "---\nfor_each: x\n---\nb",
	}), Options{}, newCapture())
	require.ErrorContains(t, err, "needs a permalink")

	output, err := outputOf("/")
	require.NoError(t, err)
	assert.Equal(t, indexFile, output)
	output, err = outputOf("/feed.xml")
	require.NoError(t, err)
	assert.Equal(t, "feed.xml", output)
	assert.Equal(t, "index.html", defaultOutput("index.html"))
	assert.Equal(t, "docs/index.html", defaultOutput("docs/index.md"))

	_, err = staticOutput(&page{src: "a.html", meta: sortedMap{"permalink": "/../x"}})
	require.Error(t, err)
}

func TestReadFailures(t *testing.T) {
	r := &run{src: fstest.MapFS{"big": {Data: []byte("0123456789")}}, limits: Limits{TemplateBytes: 2}.withDefaults()}
	_, err := r.readFile("big")
	require.ErrorContains(t, err, "more than")
	_, err = r.readFile("missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = r.pageBody("missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	r.src = unreadableFS{}
	_, err = r.readFile("a")
	require.ErrorContains(t, err, "reading a")
}

// unreadableFS has files that exist and cannot be read.
type unreadableFS struct{}

func (unreadableFS) Open(string) (fs.File, error) { return nil, errors.New("no") }

func (unreadableFS) Stat(string) (fs.FileInfo, error) {
	return fstest.MapFS{"a": {}}.Stat("a") //nolint:wrapcheck // a test's fixture
}

func TestOnlyRecordFilesAreRecords(t *testing.T) {
	assert.True(t, isRecord("a.yml"))
	assert.True(t, isRecord("a.markdown"))
	assert.False(t, isRecord("a.txt"))
	c, _ := renderTree(t, map[string]string{
		"_layouts/base.html": baseLayout, "_data/x/a.json": `{"n":1}`, "_data/x/notes.txt": "skip me",
		"p.html": "---\n---\n{{ site.data.x | size }}",
	})
	assert.Equal(t, "1", c.files["p.html"])
}
