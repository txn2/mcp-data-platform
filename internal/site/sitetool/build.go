package sitetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitetypecheck"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild/pkgmap"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild/sourcemap"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
)

// The commands that read a site's build (#2783, #2784, #2785).
const (
	CmdImports     = "imports"
	CmdBuildReport = "build_report"
	// CmdTypecheck queues a release's type check again (#2793).
	CmdTypecheck   = "typecheck"
	CmdSymbolicate = "symbolicate"
)

// The fields a result carries a site's build in.
const (
	fieldBuilds  = "builds"
	fieldGuide   = "site_guide"
	fieldRelease = "release"
)

// maxGuideBytes is how much of a site's guide a result carries.
const maxGuideBytes = 32 << 10

// maxStackBytes bounds a stack trace symbolicate reads.
const maxStackBytes = 256 << 10

// Guide is a site's AGENTS.md as a result carries it (#2782).
type Guide struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Truncated is true when the file goes on past what is shown; read the rest
	// with manage_asset get_content.
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note"`
}

const guideNote = "This is the site's guide (AGENTS.md at its root, never served). Read it before changing the site, " +
	"and keep it current when you change how the site is built, what it loads, or where its settings live."

// readGuide is a site's guide in its working environment, nil when it has
// none or the caller may not edit it.
func readGuide(ctx context.Context, svc *sitesvc.Service, actor sitesvc.Actor, siteRef string) *Guide {
	b, err := svc.ReadBuild(ctx, actor, siteRef, "", 0)
	if err != nil {
		return nil
	}
	data, err := fs.ReadFile(b.Source, sitebuild.GuideFile)
	if err != nil {
		return nil
	}
	g := &Guide{Path: sitebuild.GuideFile, Note: guideNote}
	if len(data) > maxGuideBytes {
		cut := maxGuideBytes
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		data, g.Truncated = data[:cut], true
		g.Note += " It continues past what is shown: read the whole file with manage_asset get_content."
	}
	g.Content = string(data)
	return g
}

// guideTTL is how long a session is remembered as shown a site's guide.
const guideTTL = 24 * time.Hour

// shown is which sessions were shown which site's guide: the first read, patch
// or publish on a site in a session carries it, later ones do not. The
// deployment's is the replica-shared session store (UseGuideStore), since a
// session's calls reach any replica.
var shown searchgate.Store = searchgate.NewMemoryStore(guideTTL)

// UseGuideStore sets where sessions shown a guide are remembered.
func UseGuideStore(store searchgate.Store) {
	if store != nil {
		shown = store
	}
}

// firstShowing reports whether key has not been shown, and remembers it.
func firstShowing(ctx context.Context, key string) bool {
	seen, err := shown.HasDiscovered(ctx, key)
	if err != nil || seen {
		return false
	}
	return shown.MarkDiscovered(ctx, key) == nil
}

// shownGuide is the site's guide when this is the session's first call on the
// site, nil otherwise or when it has none.
func shownGuide(ctx context.Context, svc *sitesvc.Service, actor sitesvc.Actor, siteRef string) *Guide {
	session := "anonymous"
	if pc := middleware.GetPlatformContext(ctx); pc != nil && pc.SessionID != "" {
		session = pc.SessionID
	}
	g := readGuide(ctx, svc, actor, siteRef)
	if g == nil || !firstShowing(ctx, "site_guide:"+session+":"+siteRef) {
		return nil
	}
	return g
}

// withGuide is a result with the site's guide beside it.
func withGuide(v any, g *Guide) any {
	if g == nil {
		return v
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	out := map[string]any{}
	if json.Unmarshal(raw, &out) != nil {
		return map[string]any{"result": v, fieldGuide: g}
	}
	out[fieldGuide] = g
	return out
}

// imports proposes the imports map a site's package.json and lockfile give
// (#2785): every bare specifier its entries import, at the locked version,
// with each package's locked peers pinned through deps.
func (t *Tool) imports(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	b, err := t.Service.ReadBuild(ctx, actor, in.Site, in.Environment, in.Release)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	data, err := fs.ReadFile(b.Source, sitebuild.ControlFile)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("release %d has no %s; add one naming its entries first", b.Release.Number, sitebuild.ControlFile)}
	}
	cfg, err := sitebuild.Parse(data)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: sitebuild.ControlFile + ": " + err.Error()}
	}
	specs, err := sitebuild.Specifiers(ctx, b.Source, cfg)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	lock, err := pkgmap.ReadLock(b.Source)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	if lock.File == "" {
		return nil, &sitesvc.RefusedError{Msg: "the release has no package-lock.json, pnpm-lock.yaml or yarn.lock to read versions from"}
	}
	var peers pkgmap.Peers
	if t.Service.Fetch != nil {
		peers = pkgmap.Registry{Fetch: t.Service.Fetch.Fetch}
	}
	gen, err := pkgmap.Generate(ctx, specs, lock, peers)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	return map[string]any{
		fieldRelease: b.Release.Number, "imports": gen.Imports, "lockfile": gen.Lockfile, "missing": cfg.Unmapped(gen.Missing),
		"message": "A proposal: review it and write it into " + sitebuild.ControlFile + "'s imports (entries you already map are proposed too; " +
			"yours win if you keep both), or set \"imports_from\": \"package.json\" to generate it at every publish.",
	}, nil
}

// buildReport is what a release's compile produced (#2783).
func (t *Tool) buildReport(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	b, err := t.Service.ReadBuild(ctx, actor, in.Site, in.Environment, in.Release)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	builds := []sitebuild.Report{}
	if b.Index != nil {
		builds = append(builds, b.Index.Builds...)
	}
	out := map[string]any{fieldRelease: b.Release.Number, fieldBuilds: builds}
	if len(builds) == 0 {
		out["message"] = "This release recorded no compile: it has no " + sitebuild.ControlFile + ", or was published before reports were kept."
	}
	if t.Typechecks != nil {
		if check, err := t.Typechecks.Latest(ctx, b.Release.SiteID, b.Release.Number); err == nil && check != nil {
			out[fieldTypecheck] = check
		}
	}
	return out, nil
}

// fieldTypecheck is the key a release's type check is reported under.
const fieldTypecheck = "typecheck"

// TypecheckQueue is how the tool queues and reads a release's type check
// (#2793).
type TypecheckQueue interface {
	Enqueue(ctx context.Context, req sitetypecheck.Request) (int64, error)
	Latest(ctx context.Context, siteID string, release int) (*sitetypecheck.Check, error)
}

// withTypecheck adds to a publish's answer the type check it queued (#2793),
// when the site's _build.json asks for one.
func (t *Tool) withTypecheck(ctx context.Context, v any, rel *site.Release) any {
	if t.Typechecks == nil || rel == nil {
		return v
	}
	check, err := t.Typechecks.Latest(ctx, rel.SiteID, rel.Number)
	if err != nil || check == nil {
		return v
	}
	out, ok := v.(map[string]any)
	if !ok {
		raw, err := json.Marshal(v)
		if err != nil || json.Unmarshal(raw, &out) != nil {
			return v
		}
	}
	out[fieldTypecheck] = sitetypecheck.Queued{ID: check.ID, Status: check.Status}
	return out
}

// typecheck queues a release's type check again (#2793).
func (t *Tool) typecheck(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	if t.Typechecks == nil {
		return nil, &sitesvc.RefusedError{Msg: "this deployment runs no type checks: it has no bucket to keep the compiler in, or sites.typecheck.enabled is false"}
	}
	number, queued, err := sitetypecheck.Recheck(ctx, t.Typechecks, t.Service, sitetypecheck.RecheckRequest{
		Actor: actor, Site: in.Site, Environment: in.Environment, Release: in.Release,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return map[string]any{
		fieldRelease:   number,
		fieldTypecheck: queued,
		"message":      "Queued. Checks run one at a time; read the result with build_report or on the release's page.",
	}, nil
}

// symbolicate maps a stack trace from a release's minified output to the
// original files, lines and columns, through the maps the release holds
// (#2784).
func (t *Tool) symbolicate(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	switch {
	case strings.TrimSpace(in.Stack) == "":
		return nil, &sitesvc.RefusedError{Msg: "symbolicate needs stack: the stack trace as the browser printed it"}
	case len(in.Stack) > maxStackBytes:
		return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("stack is %d bytes, more than the %d read", len(in.Stack), maxStackBytes)}
	}
	b, err := t.Service.ReadBuild(ctx, actor, in.Site, in.Environment, in.Release)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	maps := map[string]*sourcemap.Map{}
	frames := sourcemap.Locations(in.Stack)
	mapped := 0
	for i := range frames {
		f := &frames[i]
		m, note := mapOf(b.Output, f.File, maps)
		if m == nil {
			f.Note = note
			continue
		}
		if p, ok := m.Lookup(f.Line, f.Column); ok {
			f.Mapped = &p
			mapped++
		} else {
			f.Note = "the map says nothing about this line and column"
		}
	}
	return map[string]any{fieldRelease: b.Release.Number, "frames": frames, "mapped": mapped}, nil
}

// mapOf is the source map of one output of a release, read once.
func mapOf(output fs.FS, file string, cache map[string]*sourcemap.Map) (m *sourcemap.Map, note string) {
	if held, ok := cache[file]; ok {
		if held == nil {
			return nil, "the release holds no source map for " + file
		}
		return held, ""
	}
	data, err := fs.ReadFile(output, file+".map")
	if err != nil {
		cache[file] = nil
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "the release holds no source map for " + file + `; publish with "sourcemap": "private" (or true) in ` + sitebuild.ControlFile
		}
		return nil, "the source map could not be read: " + err.Error()
	}
	parsed, err := sourcemap.Parse(data)
	if err != nil {
		cache[file] = nil
		return nil, err.Error()
	}
	cache[file] = parsed
	return parsed, ""
}
