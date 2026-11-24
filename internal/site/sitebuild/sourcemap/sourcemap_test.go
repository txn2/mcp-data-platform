package sourcemap

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

func TestSymbolicatesACompiledEntry(t *testing.T) {
	doc := `{"entries":["app.tsx"],"sourcemap":"private","minify":true}`
	cfg, err := sitebuild.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	src := fstest.MapFS{
		sitebuild.ControlFile: {Data: []byte(doc)},
		"app.tsx":             {Data: []byte("import { boom } from \"./lib/boom\";\nconsole.log(1);\nboom();\n")},
		"lib/boom.ts":         {Data: []byte("export function boom(): void {\n  const x = 1;\n  throw new Error(\"boom \" + x);\n}\n")},
	}
	res, err := sitebuild.Compile(context.Background(), src, "app.tsx", sitebuild.Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	var js, mapData string
	for _, o := range res.Outputs {
		switch o.Path {
		case "app.js":
			js = string(o.Body)
		case "app.js.map":
			mapData = string(o.Body)
		}
	}
	m, err := Parse([]byte(mapData))
	if err != nil {
		t.Fatal(err)
	}
	col := strings.Index(js, "throw") + 1
	trace := "Error: boom 1\n    at boom (https://docs--dev.pages.example.net/app.js?v=3:1:" + strconv.Itoa(col) + ")\n    at f@/app.js:1:1"
	frames := Locations(trace)
	if len(frames) != 2 || frames[0].File != "app.js" || frames[0].Line != 1 || frames[1].File != "app.js" {
		t.Fatalf("frames %+v", frames)
	}
	p, ok := m.Lookup(frames[0].Line, frames[0].Column)
	if !ok || p.Source != "lib/boom.ts" || p.Line != 3 {
		t.Errorf("lookup %+v %v", p, ok)
	}
	if _, ok := m.Lookup(99, 1); ok {
		t.Error("a line past the end mapped")
	}
	for _, bad := range []string{`{`, `{"version":2}`, `{"version":3,"mappings":"!!"}`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
}
