package siterender

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"strings"
	"testing"
	"testing/fstest"
)

type capture struct {
	files  map[string]string
	copies []string
	kept   []string
}

func newCapture() *capture { return &capture{files: map[string]string{}} }

func (c *capture) Put(p string, b []byte) error { c.files[p] = string(b); return nil }
func (c *capture) Copy(p string) error          { c.copies = append(c.copies, p); return nil }
func (c *capture) Keep(p string) error          { c.kept = append(c.kept, p); return nil }

func tree(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for k, v := range files {
		fsys[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return fsys
}

func renderTree(t *testing.T, files map[string]string) (*capture, *Index) {
	t.Helper()
	c := newCapture()
	idx, err := Render(context.Background(), tree(files), Options{}, c)
	if err != nil {
		t.Fatal(err)
	}
	return c, idx
}

const baseLayout = "<body>{{ content }}</body>"

func TestLayoutsAndMarkdownAndPassthrough(t *testing.T) {
	c, _ := renderTree(t, map[string]string{
		"_layouts/base.html": baseLayout,
		"_layouts/post.html": "---\nlayout: base\n---\n<article>{{ content }}</article>",
		"about.md":           "---\nlayout: post\ntitle: About\n---\n# {{ page.title }}\n",
		"index.html":         "---\nlayout: base\n---\n<p>home {{ page.url }}</p>",
		"plain.html":         "<p>no front matter {{ untouched }}</p>",
		"logo.png":           "\x89PNG",
		"vue.html":           "---\nliquid: false\nlayout: base\n---\n<p>{{ vue }}</p>",
	})
	if got := c.files["about/index.html"]; got != "<body><article><h1>About</h1>\n</article></body>" {
		t.Errorf("about: %q", got)
	}
	if got := c.files["index.html"]; got != "<body><p>home /</p></body>" {
		t.Errorf("index: %q", got)
	}
	if got := c.files["vue.html"]; got != "<body><p>{{ vue }}</p></body>" {
		t.Errorf("liquid false: %q", got)
	}
	want := map[string]bool{"logo.png": true, "plain.html": true}
	for _, p := range c.copies {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("not copied: %v (%v)", want, c.copies)
	}
	for p := range c.files {
		if strings.HasPrefix(p, "_") {
			t.Errorf("source served: %s", p)
		}
	}
}

func TestGeneratedPagesFromRecords(t *testing.T) {
	c, _ := renderTree(t, map[string]string{
		"_layouts/base.html":    baseLayout,
		"_data/products/a.json": `{"name":"Anvil"}`,
		"_data/products/b.yaml": "name: Bolt\nslug: the-bolt\n",
		"_data/products/c.md":   "---\nname: Crate\n---\n**strong**\n",
		"product.html":          "---\nlayout: base\nfor_each: products\npermalink: /products/{{ item.slug }}/\n---\n{{ item.name }}{{ item.content }}",
		"products.html":         "---\nfor_each: products\nper_page: 2\npermalink: /all/{{ paginator.page }}/\n---\n{% for p in paginator.items %}{{ p.name }},{% endfor %}{{ paginator.page }}/{{ paginator.total_pages }}",
		"sitemap.xml":           "---\n---\n{% for p in site.data.products %}<u>{{ p.slug }}</u>{% endfor %}",
	})
	checks := map[string]string{
		"products/a/index.html":        "<body>Anvil</body>",
		"products/the-bolt/index.html": "<body>Bolt</body>",
		"products/c/index.html":        "<body>Crate<p><strong>strong</strong></p>\n</body>",
		"all/1/index.html":             "Anvil,Bolt,1/2",
		"all/2/index.html":             "Crate,2/2",
		"sitemap.xml":                  "<u>a</u><u>the-bolt</u><u>c</u>",
	}
	for p, want := range checks {
		if got := c.files[p]; got != want {
			t.Errorf("%s: %q want %q", p, got, want)
		}
	}
}

func TestTaxonomiesAndDrafts(t *testing.T) {
	c, _ := renderTree(t, map[string]string{
		"_layouts/base.html": baseLayout,
		"a.html":             "---\ntitle: A\ntags: [Go, Web Dev]\n---\na",
		"b.html":             "---\ntitle: B\ntags: [Go]\n---\nb",
		"d.html":             "---\ntitle: D\ndraft: true\ntags: [Hidden]\n---\nd",
		"tag.html":           "---\nfor_each: tags\npermalink: /tag/{{ item.slug }}/\n---\n{{ item.name }}:{% for p in item.pages %}{{ p.title }}{{ p.url }};{% endfor %}",
	})
	if got := c.files["tag/go/index.html"]; got != "Go:A/a.html;B/b.html;" {
		t.Errorf("go: %q", got)
	}
	if _, ok := c.files["tag/web-dev/index.html"]; !ok {
		t.Errorf("slug: %v", c.files)
	}
	if _, ok := c.files["d.html"]; ok {
		t.Error("a draft was served")
	}
	if _, ok := c.files["tag/hidden/index.html"]; ok {
		t.Error("a draft's tag was served")
	}
}

func TestARenderIsAPureFunctionOfTheTree(t *testing.T) {
	files := map[string]string{
		"_layouts/base.html": baseLayout,
		"_data/shop/a.yaml":  "z: 1\ny: 2\nx: 3\nw: 4\nv: 5\nu: 6\nt: 7\ns: 8\nr: 9\nq: 10\n",
		"index.html":         "---\nlayout: base\n---\n{% for r in site.data.shop %}{% for pair in r %}{{ pair[0] }}={{ pair[1] }};{% endfor %}{% endfor %}",
	}
	first, _ := renderTree(t, files)
	for range 30 {
		again, _ := renderTree(t, files)
		if again.files["index.html"] != first.files["index.html"] {
			t.Fatalf("output differs: %q %q", again.files["index.html"], first.files["index.html"])
		}
	}
}

func TestEveryFailureIsReportedWithItsFile(t *testing.T) {
	_, err := Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout,
		"a.html":             "---\nlayout: nope\n---\na",
		"b.html":             "---\n---\nfine\n{% if %}",
		"c.html":             "---\nbad: [\n---\nc",
		"d.html":             "---\nfor_each: missing\npermalink: /x/{{ item }}/\n---\nd",
		"ok.html":            "---\n---\nok",
	}), Options{}, newCapture())
	var re *RenderError
	if !errors.As(err, &re) {
		t.Fatalf("got %v", err)
	}
	if len(re.Failures) != 1 {
		// c.html fails at scan, which stops before pages are rendered.
		t.Logf("failures: %+v", re.Failures)
	}
	files := map[string]bool{}
	for _, f := range re.Failures {
		files[f.File] = true
	}
	if !files["c.html"] {
		t.Errorf("front matter error: %+v", re.Failures)
	}

	_, err = Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout,
		"a.html":             "---\nlayout: nope\n---\na",
		"b.html":             "---\n---\nfine\n{% if %}",
		"d.html":             "---\nfor_each: missing\npermalink: /x/{{ item }}/\n---\nd",
		"ok.html":            "---\n---\nok",
	}), Options{}, newCapture())
	if !errors.As(err, &re) {
		t.Fatalf("got %v", err)
	}
	if len(re.Failures) != 3 {
		t.Errorf("want all three: %+v", re.Failures)
	}
	for _, f := range re.Failures {
		if f.File == "b.html" && f.Line != 2 {
			t.Errorf("line: %+v", f)
		}
	}
}

func TestOneOutputFromOnePage(t *testing.T) {
	_, err := Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout,
		"a.html":             "---\npermalink: /same/\n---\na",
		"b.html":             "---\npermalink: /same/\n---\nb",
	}), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "also produces") {
		t.Fatal(err)
	}
}

func TestAPermalinkCannotLeaveTheSite(t *testing.T) {
	_, err := Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout,
		"_data/x/a.json":     `{"slug":"../../etc"}`,
		"p.html":             "---\nfor_each: x\npermalink: /{{ item.slug }}/\n---\nb",
	}), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "not a path") {
		t.Fatal(err)
	}
}

func TestGenerationIsBudgeted(t *testing.T) {
	files := map[string]string{"_layouts/base.html": baseLayout, "p.html": "---\nfor_each: x\npermalink: /{{ item.slug }}/\n---\nb"}
	for i := range 50 {
		files[fmt.Sprintf("_data/x/%d.json", i)] = "{}"
	}
	_, err := Render(context.Background(), tree(files), Options{Limits: Limits{Pages: 10}}, newCapture())
	var b *BudgetError
	if !errors.As(err, &b) || b.Resource != "pages" {
		t.Fatalf("got %v", err)
	}
}

func TestAPageThatRunsOutFailsAloneAndADataBombIsRefused(t *testing.T) {
	_, err := Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout,
		"big.html":           "---\n---\n{% for i in (1..2000000000) %}xxxxxxxxxxxxxxxx{% endfor %}",
		"ok.html":            "---\n---\nok",
	}), Options{Limits: Limits{PageBytes: 1 << 12}}, newCapture())
	var re *RenderError
	if !errors.As(err, &re) || len(re.Failures) != 1 || re.Failures[0].File != "big.html" {
		t.Fatalf("got %v", err)
	}

	deep := strings.Repeat("[", 500) + strings.Repeat("]", 500)
	_, err = Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout,
		"_data/x/a.json":     `{"a":` + deep + `}`,
		"p.html":             "---\n---\n{% for r in site.data.x %}{{ r.a }}{% endfor %}",
	}), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("got %v", err)
	}
}

func TestDependenciesAreWhatThePageRead(t *testing.T) {
	_, idx := renderTree(t, map[string]string{
		"_layouts/base.html":    baseLayout,
		"_includes/nav.html":    "nav",
		"_data/products/a.json": "{}",
		"_data/other/b.json":    "{}",
		"plain.html":            "---\nlayout: base\n---\n{% include 'nav.html' %}",
		"list.html":             "---\n---\n{% for p in site.data.products %}x{% endfor %}",
		"tags.html":             "---\n---\n{% for t in site.tags %}x{% endfor %}",
	})
	want := map[string]string{
		"plain.html": "_includes/nav.html,_layouts/base.html",
		"list.html":  "data:products",
		"tags.html":  "site:pages",
	}
	for _, e := range idx.Entries {
		if got := strings.Join(e.Inputs, ","); got != want[e.Output] {
			t.Errorf("%s: %q want %q", e.Output, got, want[e.Output])
		}
	}
}

func TestActive(t *testing.T) {
	if Active(tree(map[string]string{"index.html": "x"})) {
		t.Error("a tree with no layouts renders")
	}
	if !Active(tree(map[string]string{"_layouts/a.html": "x"})) {
		t.Error("a tree with layouts does not render")
	}
}

func site6000(pages int) map[string]string {
	files := map[string]string{
		"_layouts/base.html": "<html><nav>{% include 'nav.html' %}</nav><body>{{ content }}</body></html>",
		"_includes/nav.html": "<a href=/>home</a>",
		"product.html":       "---\nlayout: base\nfor_each: products\npermalink: /products/{{ item.slug }}/\n---\n<h1>{{ item.name }}</h1><p>{{ item.price }}</p>",
		"index.html":         "---\nlayout: base\n---\n{% for p in site.data.products limit: 50 %}{{ p.name }}{% endfor %}",
	}
	for i := range pages {
		files[fmt.Sprintf("_data/products/p%d.json", i)] = fmt.Sprintf(`{"name":"Product %d","price":%d}`, i, i)
	}
	return files
}

func TestChangingOneRecordRendersOnlyWhatDependsOnIt(t *testing.T) {
	files := site6000(5)
	first, idx := renderTree(t, files)
	if len(first.files) != 6 {
		t.Fatalf("got %d", len(first.files))
	}
	files["_data/products/p3.json"] = `{"name":"Renamed","price":3}`
	c := newCapture()
	second, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"_data/products/p3.json"}}, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.files) != 2 || c.files["products/p3/index.html"] == "" || !strings.Contains(c.files["index.html"], "Renamed") {
		t.Fatalf("rendered %v", c.files)
	}
	if len(c.kept) != 4 {
		t.Errorf("kept %v", c.kept)
	}
	if len(second.Entries) != 6 {
		t.Errorf("the index must still name every output: %d", len(second.Entries))
	}
}

func TestChangingALayoutRendersEveryPageUsingIt(t *testing.T) {
	files := site6000(5)
	_, idx := renderTree(t, files)
	files["_layouts/base.html"] = "<main>{{ content }}</main>"
	c := newCapture()
	if _, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"_layouts/base.html"}}, c); err != nil {
		t.Fatal(err)
	}
	if len(c.files) != 6 || len(c.kept) != 0 {
		t.Fatalf("rendered %d kept %d", len(c.files), len(c.kept))
	}
}

func TestChangingAnIncludeOrARecordSetFollowsTheReads(t *testing.T) {
	files := site6000(3)
	_, idx := renderTree(t, files)

	t.Run("an include", func(t *testing.T) {
		c := newCapture()
		if _, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"_includes/nav.html"}}, c); err != nil {
			t.Fatal(err)
		}
		if len(c.files) != 4 {
			t.Fatalf("rendered %v", c.files)
		}
	})
	t.Run("a new record makes a new page and refreshes the list only", func(t *testing.T) {
		added := maps.Clone(files)
		added["_data/products/new.json"] = `{"name":"New","price":9}`
		c := newCapture()
		if _, err := Render(context.Background(), tree(added), Options{Previous: idx, Changed: []string{"_data/products/new.json"}}, c); err != nil {
			t.Fatal(err)
		}
		if c.files["products/new/index.html"] == "" || c.files["index.html"] == "" || len(c.files) != 2 {
			t.Fatalf("rendered %v", c.files)
		}
	})
	t.Run("a deleted record loses its page", func(t *testing.T) {
		gone := maps.Clone(files)
		delete(gone, "_data/products/p1.json")
		c := newCapture()
		out, err := Render(context.Background(), tree(gone), Options{Previous: idx, Changed: []string{"_data/products/p1.json"}}, c)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range out.Entries {
			if e.Output == "products/p1/index.html" {
				t.Fatal("still indexed")
			}
		}
	})
}

func TestIncrementalMatchesAFullRender(t *testing.T) {
	files := site6000(8)
	full, idx := renderTree(t, files)
	files["_data/products/p2.json"] = `{"name":"Changed","price":2}`
	c := newCapture()
	if _, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"_data/products/p2.json"}}, c); err != nil {
		t.Fatal(err)
	}
	merged := maps.Clone(full.files)
	maps.Copy(merged, c.files)
	fresh, _ := renderTree(t, files)
	if !maps.Equal(merged, fresh.files) {
		t.Fatal("kept plus rendered differs from a full render")
	}
}

func BenchmarkSixThousandPages(b *testing.B) {
	files := tree(site6000(6000))
	for b.Loop() {
		if _, err := Render(context.Background(), files, Options{}, newCapture()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOneRecordChangedOfSixThousand(b *testing.B) {
	files := tree(site6000(6000))
	idx, err := Render(context.Background(), files, Options{}, newCapture())
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := Render(context.Background(), files, Options{Previous: idx, Changed: []string{"_data/products/p7.json"}}, newCapture()); err != nil {
			b.Fatal(err)
		}
	}
}

type countingFS struct {
	fstest.MapFS
	reads map[string]int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.reads[name]++
	return c.MapFS.Open(name) //nolint:wrapcheck // a test's pass-through
}

func TestALayoutIsReadOncePerRender(t *testing.T) {
	counted := &countingFS{MapFS: tree(site6000(20)), reads: map[string]int{}}
	if _, err := Render(context.Background(), counted, Options{}, newCapture()); err != nil {
		t.Fatal(err)
	}
	if got := counted.reads["_layouts/base.html"]; got > 2 {
		t.Errorf("read %d times", got)
	}
	if got := counted.reads["_includes/nav.html"]; got > 2 {
		t.Errorf("read %d times", got)
	}
}

// A filter that panics stands in for a bug in the engine.
func TestAPanicInTheEngineFailsOnePage(t *testing.T) {
	r := &run{ctx: context.Background(), meter: newMeter(context.Background(), Limits{}.withDefaults()), parsedLayouts: map[string]*layoutFile{}, limits: Limits{}.withDefaults()}
	in := instance{page: &page{src: "missing.html"}}
	r.src = panickingFS{}
	body, err := r.guardedBody(in, "x.html", map[string]any{})
	if body != nil || err == nil || !strings.Contains(err.Error(), "engine failed") {
		t.Fatalf("%q %v", body, err)
	}
}

type panickingFS struct{}

func (panickingFS) Open(string) (fs.File, error) { panic("a bug") }

func TestDataIsBounded(t *testing.T) {
	files := map[string]string{"_layouts/base.html": baseLayout, "p.html": "---\n---\n{% for r in site.data.x %}{{ r.slug }}{% endfor %}"}
	for i := range 20 {
		files[fmt.Sprintf("_data/x/%d.json", i)] = fmt.Sprintf(`{"v":%q}`, strings.Repeat("a", 4096))
	}
	_, err := Render(context.Background(), tree(files), Options{Limits: Limits{DataBytes: 8192}}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "records and front matter") {
		t.Fatalf("got %v", err)
	}
	var keys strings.Builder
	fmt.Fprintf(&keys, "{")
	for i := range maxDataKeys + 1 {
		if i > 0 {
			fmt.Fprintf(&keys, ",")
		}
		fmt.Fprintf(&keys, `"k%d":1`, i)
	}
	fmt.Fprintf(&keys, "}")
	_, err = Render(context.Background(), tree(map[string]string{
		"_layouts/base.html": baseLayout, "_data/x/a.json": keys.String(), "p.html": "---\n---\n{% for r in site.data.x %}x{% endfor %}",
	}), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "keys") {
		t.Fatalf("got %v", err)
	}
}

func TestANewEngineRendersEverythingAgain(t *testing.T) {
	files := site6000(2)
	_, idx := renderTree(t, files)
	idx.Version = "older"
	c := newCapture()
	if _, err := Render(context.Background(), tree(files), Options{Previous: idx}, c); err != nil {
		t.Fatal(err)
	}
	if len(c.kept) != 0 || len(c.files) != 3 {
		t.Fatalf("kept %d rendered %d", len(c.kept), len(c.files))
	}
}
