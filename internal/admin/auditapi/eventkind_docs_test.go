package auditapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// eventKindParamPattern locates a swaggo annotation documenting the event_kind
// query parameter and captures the quoted description swag copies into the
// generated OpenAPI document. Deliberately loose about the description so a
// reworded one is still found and judged rather than silently skipped.
var eventKindParamPattern = regexp.MustCompile(`(?m)^// @Param\s+event_kind\s+query\s+string\s+\w+\s+"([^"]*)"`)

// exampleTagPattern captures the value of an `example` struct tag.
var exampleTagPattern = regexp.MustCompile(`example:"([^"]*)"`)

// enumeratedKindsMarker introduces the comma-separated list of kinds inside an
// event_kind parameter description. The list runs to the first period, so the
// prose that follows it -- which names a route whose own path segments would
// otherwise read as kind names -- is not mistaken for part of the list.
const enumeratedKindsMarker = "event kind: "

// annotatedFiles are the sources in this package carrying @Param event_kind
// annotations. Listed rather than globbed so a file that loses its annotations
// fails the gate instead of silently dropping out of the scan.
var annotatedFiles = []string{"events.go", "metrics.go"}

// TestEventKindParamNamesEveryEventType guards issue #1520: the event_kind
// query parameter documented two of the kinds the column holds, so an operator
// reading the parameter had no way to learn that resource_move or script_run
// were what to ask for, while the response schema enum on the same endpoints
// listed the full set. A filter naming an undocumented kind works, so the whole
// cost falls on discovery.
//
// The gate reads the audit.EventType constants out of pkg/audit rather than a
// list kept here, so adding a kind to the platform fails this test until every
// annotation names it. It also asserts the annotations name nothing the
// constants do not: a documented kind that no longer exists returns an empty
// result rather than an error, which reads as "no activity" instead of "wrong
// value".
func TestEventKindParamNamesEveryEventType(t *testing.T) {
	want := eventTypeConstants(t)
	require.NotEmpty(t, want, "found zero audit.EventType constants; the source scan is broken")

	for _, site := range eventKindParamDescriptions(t) {
		require.Equal(t, want, enumeratedKinds(site.description),
			"%s:%d documents event_kind as %q; it must enumerate exactly the audit.EventType constants %v",
			site.file, site.line, site.description, want)
	}
}

// TestAuditFiltersExampleNamesEveryEventType keeps the event_kinds example on
// the filters response in step with the same set. The filters endpoint is what
// the parameter documentation points a caller at for the kinds present in the
// data, so an example naming a smaller set reintroduces the discovery gap one
// hop further along.
func TestAuditFiltersExampleNamesEveryEventType(t *testing.T) {
	want := eventTypeConstants(t)
	example := eventKindsExampleTag(t)

	named := splitAndSort(example, ",")
	require.Equal(t, want, named,
		"the event_kinds example is %q; it must name exactly the audit.EventType constants %v", example, want)
}

// repoFile resolves a path relative to the repository root.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed; cannot locate repo root")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(append([]string{repoRoot}, parts...)...)
}

// eventTypeConstants returns the sorted values of every audit.EventType
// constant declared anywhere in pkg/audit, so a kind added in a file other than
// event.go is still held to the documentation gate.
func eventTypeConstants(t *testing.T) []string {
	t.Helper()
	dir := repoFile(t, "pkg", "audit")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading %s", dir)

	fset := token.NewFileSet()
	var kinds []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err, "parsing %s", path)
		kinds = append(kinds, eventTypeConstantsInFile(t, path, file)...)
	}
	sort.Strings(kinds)
	return kinds
}

// eventTypeConstantsInFile returns the EventType constant values declared in
// one file. A const block that declares at least one EventType is required to
// declare nothing else: an unrecognized spec beside them would be a kind the
// scan cannot see, which is exactly the drift this gate exists to catch.
func eventTypeConstantsInFile(t *testing.T, path string, file *ast.File) []string {
	t.Helper()
	var kinds []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		var found []string
		var unrecognized []string
		for _, spec := range gen.Specs {
			if value, ok := eventTypeConstValue(spec); ok {
				found = append(found, value)
				continue
			}
			unrecognized = append(unrecognized, specNames(spec)...)
		}
		if len(found) == 0 {
			continue
		}
		require.Empty(t, unrecognized,
			"%s declares %v in the same const block as the EventType constants; "+
				"the documentation gate cannot read them, so keep the block to `Name EventType = \"value\"` specs",
			path, unrecognized)
		kinds = append(kinds, found...)
	}
	return kinds
}

// specNames returns the identifiers a const spec declares.
func specNames(spec ast.Spec) []string {
	vs, ok := spec.(*ast.ValueSpec)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(vs.Names))
	for _, n := range vs.Names {
		names = append(names, n.Name)
	}
	return names
}

// eventTypeConstValue reports the string value of a const spec whose declared
// type is EventType, and whether the spec is one.
func eventTypeConstValue(spec ast.Spec) (string, bool) {
	vs, ok := spec.(*ast.ValueSpec)
	if !ok || len(vs.Values) != 1 {
		return "", false
	}
	ident, ok := vs.Type.(*ast.Ident)
	if !ok || ident.Name != "EventType" {
		return "", false
	}
	lit, ok := vs.Values[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// paramSite is one @Param event_kind annotation and where it was found.
type paramSite struct {
	file        string
	line        int
	description string
}

// eventKindParamDescriptions returns every @Param event_kind annotation in this
// package's annotated sources. Each listed file must contribute at least one:
// an endpoint that drops its annotation stops documenting the filter it still
// accepts, which is the gap this gate closes.
func eventKindParamDescriptions(t *testing.T) []paramSite {
	t.Helper()
	var sites []paramSite
	for _, name := range annotatedFiles {
		src, err := os.ReadFile(repoFile(t, "internal", "admin", "auditapi", name))
		require.NoError(t, err, "reading %s", name)
		found := 0
		for _, loc := range eventKindParamPattern.FindAllSubmatchIndex(src, -1) {
			found++
			sites = append(sites, paramSite{
				file:        name,
				line:        1 + strings.Count(string(src[:loc[0]]), "\n"),
				description: string(src[loc[2]:loc[3]]),
			})
		}
		require.NotZero(t, found, "%s carries no @Param event_kind annotation", name)
	}
	return sites
}

// eventKindsExampleTag returns the `example` struct-tag value on
// auditFiltersResponse.EventKinds, which swag copies into the filters
// endpoint's response schema.
func eventKindsExampleTag(t *testing.T) string {
	t.Helper()
	path := repoFile(t, "internal", "admin", "auditapi", "events.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err, "parsing %s", path)

	for _, field := range structFields(file, "auditFiltersResponse") {
		if field.Tag == nil || len(field.Names) != 1 || field.Names[0].Name != "EventKinds" {
			continue
		}
		if m := exampleTagPattern.FindStringSubmatch(field.Tag.Value); m != nil {
			return m[1]
		}
	}
	t.Fatalf("no auditFiltersResponse.EventKinds field with an example tag in %s", path)
	return ""
}

// structFields returns the fields of the named struct type declared in file,
// or nil when the file declares no such struct.
func structFields(file *ast.File, structName string) []*ast.Field {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != structName {
				continue
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				return st.Fields.List
			}
		}
	}
	return nil
}

// enumeratedKinds returns the sorted kinds a parameter description lists after
// enumeratedKindsMarker, up to the first period. Reading only that span keeps
// the sentence that follows -- which cites a route whose path contains a kind
// name -- from standing in for a kind the description never enumerated. Returns
// nil when the description carries no list, so the caller's comparison reports
// the description verbatim.
func enumeratedKinds(description string) []string {
	_, list, found := strings.Cut(description, enumeratedKindsMarker)
	if !found {
		return nil
	}
	if enumerated, _, ok := strings.Cut(list, "."); ok {
		list = enumerated
	}
	return splitAndSort(list, ",")
}

// splitAndSort splits a delimited list, trims each entry, and sorts the result.
func splitAndSort(list, sep string) []string {
	var out []string
	for part := range strings.SplitSeq(list, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}
