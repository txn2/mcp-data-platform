package structure_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/go/packages"
)

// loadTypedFirstParty loads every first-party package under root with its
// syntax and type information, type-checking them itself rather than through
// packages.Load's NeedTypes (#2462).
//
// packages.Load type-checks packages in concurrent goroutines that share one
// universe of imported packages, and Go 1.27's go/types reads a lazily
// expanded *types.Named in (*Checker).isComplete without the lock its
// (*Named).unpack writes under (golang/go#81122). Two checkers touching the
// same imported type is a data race the race detector reports, failing the
// structure gates on pull requests whose diff says nothing about them. The
// loader's concurrency is sized from GOMAXPROCS when go/packages initializes,
// so a test cannot turn it down.
//
// Here go list supplies the files and every package's export data, and the
// first-party packages are checked by parallel workers that each import
// through an export-data importer of their own. No *types.Package, and so no
// *types.Named, is reachable from two workers, which is what keeps the
// checkers apart while keeping the load parallel. The gates read only
// package-local type information and compare objects by package path, which
// separate universes keep exact.
func loadTypedFirstParty(root string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedExportFile | packages.NeedModule,
		Tests: false,
		Dir:   root,
	}
	roots, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("listing packages: %w", err)
	}
	exports := make(map[string]string)
	packages.Visit(roots, nil, func(p *packages.Package) {
		if p.ExportFile != "" {
			exports[p.ID] = p.ExportFile
		}
	})
	var firstParty []*packages.Package
	for _, p := range roots {
		if isFirstParty(p.PkgPath) && len(p.CompiledGoFiles) > 0 {
			firstParty = append(firstParty, p)
		}
	}
	checkAll(firstParty, exports)
	return firstParty, nil
}

// checkAll type-checks pkgs across GOMAXPROCS workers, each with its own file
// set and imported universe.
func checkAll(pkgs []*packages.Package, exports map[string]string) {
	work := make(chan *packages.Package)
	var wg sync.WaitGroup
	for range max(1, runtime.GOMAXPROCS(0)) {
		wg.Go(func() {
			w := &checkWorker{fset: token.NewFileSet(), imported: make(map[string]*types.Package), exports: exports}
			for p := range work {
				w.check(p)
			}
		})
	}
	for _, p := range pkgs {
		work <- p
	}
	close(work)
	wg.Wait()
}

// checkWorker type-checks packages one at a time against the universe it
// imported itself.
type checkWorker struct {
	fset     *token.FileSet
	imported map[string]*types.Package
	exports  map[string]string
}

// check parses and type-checks p, filling the fields packages.Load's
// NeedSyntax, NeedTypes and NeedTypesInfo would, and records each failure in
// p.Errors.
func (w *checkWorker) check(p *packages.Package) {
	p.Fset = w.fset
	for _, name := range p.CompiledGoFiles {
		f, err := parser.ParseFile(w.fset, name, nil, parser.AllErrors|parser.ParseComments)
		if err != nil {
			p.Errors = append(p.Errors, packages.Error{Pos: name, Msg: err.Error(), Kind: packages.ParseError})
		}
		if f != nil {
			p.Syntax = append(p.Syntax, f)
		}
	}
	p.TypesInfo = &types.Info{
		Types:        make(map[ast.Expr]types.TypeAndValue),
		Defs:         make(map[*ast.Ident]types.Object),
		Uses:         make(map[*ast.Ident]types.Object),
		Implicits:    make(map[ast.Node]types.Object),
		Selections:   make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:       make(map[ast.Node]*types.Scope),
		Instances:    make(map[*ast.Ident]types.Instance),
		FileVersions: make(map[*ast.File]string),
	}
	conf := types.Config{
		Importer: importerFor(w, p),
		Sizes:    types.SizesFor("gc", runtime.GOARCH),
		Error: func(err error) {
			p.Errors = append(p.Errors, packages.Error{Msg: err.Error(), Kind: packages.TypeError})
		},
	}
	if p.Module != nil && p.Module.GoVersion != "" {
		conf.GoVersion = "go" + p.Module.GoVersion
	}
	p.Types, _ = conf.Check(p.PkgPath, w.fset, p.Syntax, p.TypesInfo)
}

// importerFor resolves p's import paths through p.Imports, which go list
// already mapped (vendoring, replacements), to the export data of the package
// each names.
func importerFor(w *checkWorker, p *packages.Package) types.Importer {
	return importerFunc(func(path string) (*types.Package, error) {
		if path == "unsafe" {
			return types.Unsafe, nil
		}
		dep, ok := p.Imports[path]
		if !ok {
			return nil, fmt.Errorf("package %s imports %q, which go list did not resolve", p.PkgPath, path)
		}
		if done, ok := w.imported[dep.PkgPath]; ok && done.Complete() {
			return done, nil
		}
		file, ok := w.exports[dep.ID]
		if !ok {
			return nil, fmt.Errorf("no export data for %q", dep.PkgPath)
		}
		return w.read(file, dep.PkgPath)
	})
}

// read imports one package's export data into the worker's universe.
func (w *checkWorker) read(file, path string) (*types.Package, error) {
	f, err := os.Open(file) //nolint:gosec // a build-cache path go list reported
	if err != nil {
		return nil, fmt.Errorf("opening export data of %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	// Through Go 1.27, go list -export reports the compiler's archive, whose
	// export data NewReader extracts; go/packages reads it the same way. From
	// Go 1.28 the file is cmd/export's output, which NewReader passes through.
	r, err := gcexportdata.NewReader(f) //nolint:staticcheck // the only reader of a Go 1.27 archive
	if err != nil {
		return nil, fmt.Errorf("reading export data of %q: %w", path, err)
	}
	pkg, err := gcexportdata.Read(r, w.fset, w.imported, path)
	if err != nil {
		return nil, fmt.Errorf("decoding export data of %q: %w", path, err)
	}
	return pkg, nil
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// errNoFirstParty is the load that found nothing to check.
var errNoFirstParty = errors.New("no first-party package was loaded")

// firstPartyWithSyntax keeps the packages that parsed to at least one file.
func firstPartyWithSyntax(pkgs []*packages.Package) ([]*packages.Package, error) {
	var out []*packages.Package
	for _, p := range pkgs {
		if strings.HasPrefix(p.PkgPath, modulePath+"/") && len(p.Syntax) > 0 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errNoFirstParty
	}
	return out, nil
}
