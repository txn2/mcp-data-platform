// This file adds the god-object budget gate for the pkg/platform.Platform type
// (#756). The package-size (LOC) gate is gameable in the direction that matters
// here: moving code out of platform.go into sibling files or helper subpackages
// shrinks the line count while the Platform struct keeps every field and every
// method. This gate caps the struct itself.
//
// It caps two structural metrics that directly measure the god-object:
//   - the number of fields on the Platform struct (what it HOLDS), and
//   - the number of methods with a *Platform receiver (the surface everything
//     reaches through).
//
// Both are ceilings to ratchet DOWN as subsystems are extracted into owners
// that hold their own fields and expose their own methods. Hitting the ceiling
// is the signal to move responsibility off Platform, never to raise the number.
//
// Run: go test -run TestPlatformGodObjectBudget .
package mcp_data_platform_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// maxPlatformFields caps the number of fields on the Platform struct.
	// Frozen at today's count; ratchet down as subsystems are grouped into
	// owner structs (a subsystem's N fields become one owner field). The
	// index-jobs queue extraction (#836) folded nine fields into one
	// indexqueue.Handle, ratcheting 82 → 74; the connection-OAuth token
	// lifecycle extraction (#838) folded four fields (connOAuthStore,
	// authEventStore, authEventWriter, connOAuthRefresher) into one
	// connauth.Handle, ratcheting 74 → 71; the portal-store-layer extraction
	// (#841) folded eight fields (portalAssetStore, portalShareStore,
	// portalVersionStore, portalCollectionStore, portalThreadStore,
	// portalKnowledgePageStore, portalToolkit, portalS3Client) into one
	// portalstore.Handle, ratcheting 71 → 64; the session / cross-replica-sync
	// extraction (#843) folded six fields (sessionStore, sessionCache,
	// broadcaster, reloadBroadcaster, reloadBus, reloadCancel) into one
	// sessionsync.Handle, ratcheting 64 → 59; the memory-layer extraction (#845)
	// folded four fields (memoryStore, memoryToolkit, stalenessWatcher,
	// memoryAdapter) into one memorylayer.Handle — embeddingProv stays on
	// Platform because it backs many other subsystems — ratcheting 59 → 56; the
	// knowledge-capture-layer extraction (#847) folded four fields
	// (knowledgeInsightStore, knowledgeChangesetStore, knowledgeToolkit,
	// knowledgeDataHubWriter) into one knowledgelayer.Handle — knowledgeRouter
	// (search federation) stays on Platform as a separate later seam — ratcheting
	// 56 → 53.
	maxPlatformFields = 53

	// maxPlatformMethods caps the number of methods with a *Platform receiver.
	// Frozen at today's count; ratchet down as accessors move onto the
	// subsystem owners they belong to.
	maxPlatformMethods = 265
)

// TestPlatformGodObjectBudget fails when the Platform struct grows more fields
// or the *Platform receiver gains more methods than the frozen ceilings. Unlike
// the LOC budget, these numbers only shrink through real decomposition (moving
// state and behavior onto subsystem owners), so they cannot be satisfied by
// shuffling lines between files.
func TestPlatformGodObjectBudget(t *testing.T) {
	fields, methods := countPlatformGodObject(t)
	t.Logf("Platform god-object: %d fields, %d methods (ceilings %d / %d)",
		fields, methods, maxPlatformFields, maxPlatformMethods)

	require.LessOrEqualf(t, fields, maxPlatformFields,
		"Platform struct has %d fields, exceeding the ceiling of %d — move state onto a subsystem owner; do not raise the ceiling",
		fields, maxPlatformFields)
	require.LessOrEqualf(t, methods, maxPlatformMethods,
		"*Platform has %d methods, exceeding the ceiling of %d — move behavior onto a subsystem owner; do not raise the ceiling",
		methods, maxPlatformMethods)
}

// countPlatformGodObject parses the non-test sources of pkg/platform and returns
// the Platform struct's field count and the number of methods declared on the
// *Platform receiver.
func countPlatformGodObject(t *testing.T) (fields, methods int) {
	t.Helper()
	root, err := filepath.Abs("pkg/platform")
	require.NoError(t, err)

	fset := token.NewFileSet()
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	foundStruct := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(root, name), nil, 0)
		require.NoErrorf(t, parseErr, "parsing %s", name)

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if isPlatformMethod(d) {
					methods++
				}
			case *ast.GenDecl:
				if n, ok := platformStructFieldCount(d); ok {
					fields = n
					foundStruct = true
				}
			}
		}
	}
	require.True(t, foundStruct, "did not find `type Platform struct` in pkg/platform")
	return fields, methods
}

// isPlatformMethod reports whether fn is a method on Platform, counting both
// pointer (*Platform) and value (Platform) receivers. Counting value receivers
// too closes an escape hatch: otherwise the method ceiling could be ducked
// under by rewriting `func (p *Platform)` as `func (p Platform)` without any
// real decomposition.
func isPlatformMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X // unwrap the pointer receiver to its base type
	}
	ident, ok := recv.(*ast.Ident)
	return ok && ident.Name == "Platform"
}

// platformStructFieldCount returns the field count of `type Platform struct`,
// counting each name in a grouped declaration (a, b int == 2) and each embedded
// field as one. The bool is false for any decl that is not the Platform struct.
func platformStructFieldCount(gen *ast.GenDecl) (int, bool) {
	if gen.Tok != token.TYPE {
		return 0, false
	}
	for _, spec := range gen.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Platform" {
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}
		count := 0
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 {
				count++ // embedded field
				continue
			}
			count += len(field.Names)
		}
		return count, true
	}
	return 0, false
}
