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
// #756 EXIT POINT (closed by #854). The finite decomposition project is done:
// Platform is now a coordinator holding the irreducible shared foundations (db,
// config, toolkit registry, embedding provider, mcpServer, authenticator,
// persona registry, the provider handles, lifecycle/health) plus one handle per
// extracted subsystem. It does not shrink to zero, and chasing it there would
// dissolve the coordinator for no benefit. So these ceilings are now a PERMANENT
// STANDING INVARIANT — the guard against regrowth — not a target to keep driving
// down. Raising a ceiling is a regression to be justified in review; the only
// routine change is ratcheting further DOWN if a future extraction genuinely
// moves state or behavior onto a subsystem owner.
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
	// 56 → 53. The search-federation extraction (#849) is a method-surface seam,
	// not a field-cluster seam: it replaces the single knowledgeRouter field with
	// one searchfed.Handle field (field-for-field), so the field ceiling holds
	// flat at 53. The mechanical-cluster batch (#851) folded three clusters into
	// three owner handles: resource (resourceStore, resourceS3Client → one
	// resourcelayer.Handle), user (userStore, userDirectory → one userdir.Handle),
	// and brand (resolvedBrandLogoSVG, resolvedBrandURL, resolvedImplementorLogo →
	// one branding.Handle), ratcheting 53 → 49. The prompt-seam extraction (#853)
	// folded four fields (promptManager, promptStore, promptInfosMu, promptInfos)
	// into one promptlayer.Handle, ratcheting 49 → 46. The composition-root
	// seam (#854) folded main.go's four post-start Wire* calls into a single
	// Platform.WireRuntime entry point; it holds no state, so the field count
	// is unchanged. This is the #756 exit point (see the file header): the
	// field ceiling is now frozen as a standing anti-regrowth invariant.
	maxPlatformFields = 46

	// maxPlatformMethods caps the number of methods with a *Platform receiver.
	// Frozen at today's count; ratchet down as accessors move onto the
	// subsystem owners they belong to. The search-federation extraction (#849)
	// moved two provider-selection methods (storeSearchProviders,
	// appendFederationSearchProviders) into searchfed, ratcheting 265 → 263. The
	// ceiling then carried 8 slack above the 255 actual — a ratchet with slack is
	// not a ratchet, so it was pinned to the actual 255 (#851, Part 1) ahead of
	// the mechanical-cluster extraction that re-tightens it further. That batch
	// (#851, Part 2) then moved six methods off *Platform — resource's
	// managedResourceURIScheme / managedResourceS3Connection / resolveDefaultS3Instance,
	// user's observeAuthenticatedUser / observeBrowserLogin, and brand's
	// injectPortalLogo — into their owner packages, ratcheting 255 → 249. The
	// public accessors external callers use (ResourceStore, UserStore, BrandURL,
	// …) stay as one-line delegators, so they still count. The prompt-seam
	// extraction (#853) moved the ~40-method prompt cluster (static/workflow/
	// database registration, dynamic serving, the manage_prompt tool) onto
	// promptlayer.Handle, ratcheting 249 → 212; the four accessors external
	// callers use (PromptStore, AllPromptInfos, RegisterRuntimePrompt,
	// UnregisterRuntimePrompt) stay as one-line delegators, and the
	// middleware-chain wiring (addPromptVisibilityMiddleware) plus the
	// late-collaborator binding (bindPromptCollaborators) stay on Platform, so
	// they still count. The composition-root seam (#854) added one coordinator
	// method, WireRuntime — it folds main.go's four ordering-sensitive post-start
	// Wire* calls into one type-checked, test-covered entry point (the four Wire*
	// methods it calls stay, so this is a net +1) — ratcheting the ceiling 212 →
	// 213. This is the #756 exit point (see the file header): the method ceiling
	// is now frozen as a standing anti-regrowth invariant, not a figure to keep
	// driving down.
	maxPlatformMethods = 213
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
