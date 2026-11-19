// Package appdiagram derives how an application's parts fit together (#2540):
// which webhook source starts which script, which script reads and writes
// which table, loads which library, writes which asset or notifies which
// channel, which asset calls which endpoint or embeds which form, and what the
// parts reach outside the application. It is derived the way a script's Flow
// diagram is, from the parts' sources and stored settings at one version of
// the application, never from a run, so it needs no run to exist and cannot
// disagree with the code. A name a source computes when it runs is drawn as
// computed, never matched to a part by a guess.
//
// It also names what it finds wrong: a script loading one of the
// application's libraries at a version other than the one the version of the
// application pins, a table of the application's schema its scripts read and
// no part writes, and a script using a schema the application does not hold.
package appdiagram

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow/sqlread"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The kinds of node.
const (
	NodeScript  = "script"
	NodeLibrary = "library"
	NodeAsset   = "asset"
	NodeSource  = "source"
	NodeSchema  = "schema"
	NodeTable   = "table"
	NodeChannel = "channel"
	NodeOutput  = "output"
	NodeEnd     = "endpoint"
)

// The kinds of link.
const (
	LinkEvent    = "event"
	LinkReads    = "reads"
	LinkWrites   = "writes"
	LinkLoads    = "loads"
	LinkExports  = "exports"
	LinkRefresh  = "refreshes"
	LinkNotifies = "notifies"
	LinkPosts    = "posts"
	LinkCalls    = "calls"
	LinkEmbeds   = "embeds"
	LinkSubmits  = "submits"
)

// The groups a node is drawn in.
const (
	GroupApplication = "application"
	GroupSchema      = "schema"
	GroupOutside     = "outside"
)

// The kinds of finding.
const (
	FindingLibraryVersion = "library_version"
	FindingUnwrittenTable = "unwritten_table"
	// FindingRuntimeWrites names, once, the scripts that write a table named
	// only when they run, which may be a table reported unwritten (#2699).
	FindingRuntimeWrites = "runtime_writes"
	FindingForeignSchema = "foreign_schema"
)

// The marks a compared diagram carries.
const (
	ChangeAdded     = "added"
	ChangeRemoved   = "removed"
	ChangeChanged   = "changed"
	ChangeUnchanged = "unchanged"
)

// Node is a part, or what a part reaches.
type Node struct {
	ID    string `json:"id" example:"script:5b0c..."`
	Kind  string `json:"kind" example:"script"`
	Label string `json:"label" example:"Nightly order sync"`
	Group string `json:"group" example:"application"`
	// Part is true for a part of the application, PartID its id.
	Part   bool   `json:"part"`
	PartID string `json:"part_id,omitempty"`
	// Version is the part's version this diagram is drawn at, 0 for none.
	Version int `json:"version,omitempty"`
	// Computed is true for a name the source computes when it runs.
	Computed bool `json:"computed"`
	// Detail is what the node's card adds: a script's schedule and its
	// endpoint, a table's connection.
	Detail []string `json:"detail"`
	// Change and WasVersion mark a compared diagram's node.
	Change     string `json:"change,omitempty"`
	WasVersion int    `json:"was_version,omitempty"`
}

// Link is one way a part reaches another, or something outside: always from
// the part that acts (a script reads and writes a table, an asset calls an
// endpoint, a source starts a script).
type Link struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind" example:"reads"`
	// Detail is the version a load names, the endpoint an asset calls.
	Detail string `json:"detail,omitempty"`
	// Change and WasDetail mark a compared diagram's link: a link whose
	// detail moved (a load's version) is changed, with what it said.
	Change    string `json:"change,omitempty"`
	WasDetail string `json:"was_detail,omitempty"`
}

// Group is a box the nodes are drawn in.
type Group struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Parent string `json:"parent,omitempty"`
}

// Finding is one thing the diagram finds wrong.
type Finding struct {
	Kind string `json:"kind" example:"library_version"`
	// Node is the node it is about.
	Node    string `json:"node"`
	Message string `json:"message"`
	// Blocks is true for a finding a release refuses.
	Blocks bool `json:"blocks"`
}

// Diagram is one version of an application drawn.
type Diagram struct {
	// Version is what was drawn: "current", "latest" or a release number.
	Version      string    `json:"version"`
	ComparedWith string    `json:"compared_with,omitempty"`
	Nodes        []Node    `json:"nodes"`
	Links        []Link    `json:"links"`
	Groups       []Group   `json:"groups"`
	Findings     []Finding `json:"findings"`
}

// Part is one part as the diagram reads it at the version drawn.
type Part struct {
	application.Part
	// Version is the version drawn, 0 for a webhook source or the schema.
	Version int
}

// Binding is a script's endpoint binding as the diagram reads it.
type Binding struct {
	Name string
	Path string
}

// Reader reads what the diagram is derived from.
type Reader interface {
	Script(ctx context.Context, id string) (*script.Script, error)
	ScriptVersion(ctx context.Context, id string, version int) (*script.Version, error)
	// Schedules, Bindings and EventSources are a script's handlers (#2441):
	// its schedules, its endpoints and the webhook sources it runs for.
	Schedules(ctx context.Context, scriptID string) ([]script.Schedule, error)
	Bindings(ctx context.Context, scriptID string) ([]Binding, error)
	EventSources(ctx context.Context, scriptID string) []string
	// AssetName is an asset's name and the output identity a script writes
	// it under; ok false for an asset the caller cannot open.
	Asset(ctx context.Context, id string) (name, identity string, ok bool)
	// AssetContent is an asset version's content, empty for one not text.
	AssetContent(ctx context.Context, id string, version int) string
	// SchemaTables is the tables the schema's migrations create and the
	// ones they write rows to.
	SchemaTables(ctx context.Context, connection, schema string) SchemaTables
	// IsAppDB reports whether a connection is an application database.
	IsAppDB(ctx context.Context, connection string) bool
	// LibrarySource is one saved version of a library a script loads, so
	// the statements and helpers it defines are read where a script uses
	// them (#2699); false when it cannot be read.
	LibrarySource(ctx context.Context, name string, version int) (string, bool)
}

// SchemaTables is what a schema's migrations say about its tables, by bare
// name: the tables they leave created, and the ones they write rows to.
type SchemaTables struct {
	Created []string
	Filled  []string
}

// Input is what a diagram is drawn from.
type Input struct {
	App     *application.Application
	Parts   []Part
	Version string
}

// builder draws one diagram.
type builder struct {
	ctx      context.Context
	r        Reader
	app      *application.Application
	d        *Diagram
	nodes    map[string]int
	links    map[string]bool
	versions map[string]int // script part id -> version drawn
	libs     map[string]string
	reads    map[string][]string
	writes   map[string]bool
	// schema is what the application schema's migrations say (#2665).
	schema SchemaTables
	// unknownWriters are the scripts with a write step whose table is named
	// when it runs, on a connection that may be the schema's (#2615).
	unknownWriters []string
	scripts        map[string]string // endpoint name -> script node
	ids            map[string]string // asset output identity -> asset node
}

// Build draws in.
func Build(ctx context.Context, r Reader, in Input) *Diagram {
	b := &builder{
		ctx: ctx, r: r, app: in.App, nodes: map[string]int{}, links: map[string]bool{}, versions: map[string]int{},
		libs: map[string]string{}, reads: map[string][]string{}, writes: map[string]bool{}, scripts: map[string]string{},
		ids: map[string]string{},
		d: &Diagram{
			Version: in.Version, Nodes: []Node{}, Links: []Link{}, Findings: []Finding{},
			Groups: []Group{{ID: GroupApplication, Label: in.App.Name}, {ID: GroupOutside, Label: "Outside the application"}},
		},
	}
	b.partNodes(in.Parts)
	b.schemaTables()
	for _, p := range in.Parts {
		if p.Kind == application.KindScript {
			b.script(p)
		}
	}
	for _, p := range in.Parts {
		if p.Kind == application.KindAsset {
			b.asset(p)
		}
	}
	b.unwritten()
	sort.SliceStable(b.d.Findings, func(i, j int) bool { return b.d.Findings[i].Kind < b.d.Findings[j].Kind })
	return b.d
}

// partNodes draws each part, and indexes the libraries by name, the
// endpoints by name and the assets by output identity.
func (b *builder) partNodes(parts []Part) {
	for _, p := range parts {
		switch p.Kind {
		case application.KindScript:
			b.scriptNode(p)
		case application.KindAsset:
			id := "asset:" + p.ID
			name, identity, ok := b.r.Asset(b.ctx, p.ID)
			if !ok {
				name = "Asset you can't open"
			}
			b.node(Node{ID: id, Kind: NodeAsset, Label: name, Group: GroupApplication, Part: true, PartID: p.ID, Version: p.Version})
			if identity != "" {
				b.ids[identity] = id
			}
		case application.KindSource:
			b.node(Node{ID: "source:" + p.ID, Kind: NodeSource, Label: p.ID, Group: GroupApplication, Part: true, PartID: p.ID})
		case application.KindSchema:
			b.d.Groups = append(b.d.Groups, Group{ID: GroupSchema, Label: p.ID, Parent: GroupApplication})
			b.node(Node{ID: "schema:" + p.ID, Kind: NodeSchema, Label: p.ID, Group: GroupSchema, Part: true, PartID: p.ID})
		}
	}
}

func (b *builder) scriptNode(p Part) {
	id := "script:" + p.ID
	n := Node{ID: id, Kind: NodeScript, Label: p.ID, Group: GroupApplication, Part: true, PartID: p.ID, Version: p.Version}
	b.versions[p.ID] = p.Version
	if sc, err := b.r.Script(b.ctx, p.ID); err == nil && sc != nil {
		n.Label = scriptName(sc)
		if sc.Library {
			n.Kind = NodeLibrary
			b.libs[sc.Name] = p.ID
		}
		if schedules, err := b.r.Schedules(b.ctx, p.ID); err == nil {
			for _, sched := range schedules {
				n.Detail = append(n.Detail, "schedule "+sched.CronSpec+" "+sched.Timezone)
			}
		}
	}
	if bindings, err := b.r.Bindings(b.ctx, p.ID); err == nil {
		for _, bd := range bindings {
			n.Detail = append(n.Detail, "endpoint /endpoints/"+bd.Name+bd.Path)
			b.scripts[bd.Name] = id
		}
	}
	b.node(n)
}

func scriptName(sc *script.Script) string {
	if sc.DisplayName != "" {
		return sc.DisplayName
	}
	return sc.Name
}

// node adds a node once.
func (b *builder) node(n Node) {
	if _, ok := b.nodes[n.ID]; ok {
		return
	}
	if n.Detail == nil {
		n.Detail = []string{}
	}
	b.nodes[n.ID] = len(b.d.Nodes)
	b.d.Nodes = append(b.d.Nodes, n)
}

// link adds a link once.
func (b *builder) link(l Link) {
	key := linkKey(l) + keySep + l.Detail
	if b.links[key] {
		return
	}
	b.links[key] = true
	b.d.Links = append(b.d.Links, l)
}

// finding adds a finding.
func (b *builder) finding(f Finding) { b.d.Findings = append(b.d.Findings, f) }

// script draws what one script part reaches.
func (b *builder) script(p Part) {
	id := "script:" + p.ID
	sc, err := b.r.Script(b.ctx, p.ID)
	if err != nil || sc == nil {
		return
	}
	for _, source := range b.r.EventSources(b.ctx, p.ID) {
		b.outsideSource(source)
		b.link(Link{From: "source:" + source, To: id, Kind: LinkEvent})
	}
	v, err := b.r.ScriptVersion(b.ctx, p.ID, p.Version)
	if err != nil || v == nil {
		return
	}
	b.form(id, v.Source)
	b.loads(id, sc, v.Source)
	for _, ref := range scriptflow.DataRefsWith(v.Source, b.libraries(v.Source)) {
		b.data(id, sc, ref)
	}
	for i, out := range scriptflow.OutputRefs(v.Source) {
		b.output(id, p.ID, i, out)
	}
}

// outsideSource draws a webhook source no part names.
func (b *builder) outsideSource(name string) {
	b.node(Node{ID: "source:" + name, Kind: NodeSource, Label: name, Group: GroupOutside})
}

// formSubmit is the function a form's script defines.
var formSubmit = regexp.MustCompile(`(?m)^def form_submit\(`)

// form links a form's script to the webhook source its submissions are kept
// in.
func (b *builder) form(id, source string) {
	if !formSubmit.MatchString(source) {
		return
	}
	for name, node := range b.scripts {
		if node == id {
			src := whsource.FormSourceName(name)
			b.outsideSource(src)
			b.link(Link{From: id, To: "source:" + src, Kind: LinkSubmits})
		}
	}
}

// loads links a script to each library version it loads, and finds a load
// of one of the application's libraries at a version other than the one
// drawn.
func (b *builder) loads(id string, sc *script.Script, source string) {
	for _, ref := range scriptlib.Refs(source) {
		detail := "v" + strconv.Itoa(ref.Version)
		libID, inApp := b.libs[ref.Name]
		if !inApp {
			node := "library:" + ref.Name
			b.node(Node{ID: node, Kind: NodeLibrary, Label: ref.Name, Group: GroupOutside})
			b.link(Link{From: id, To: node, Kind: LinkLoads, Detail: detail})
			continue
		}
		b.link(Link{From: id, To: "script:" + libID, Kind: LinkLoads, Detail: detail})
		if pinned := b.versions[libID]; pinned > 0 && pinned != ref.Version {
			b.finding(Finding{
				Kind: FindingLibraryVersion, Node: id, Blocks: true,
				Message: fmt.Sprintf("script %s loads library %s at version %d, and this version of the application has it at %d; load(\"lib:%s@%d\", ...)",
					sc.Name, ref.Name, ref.Version, pinned, ref.Name, pinned),
			})
		}
	}
}

// libraries is the source of each library version source loads that can be
// read.
func (b *builder) libraries(source string) scriptflow.Libraries {
	out := scriptflow.Libraries{}
	for _, ref := range scriptlib.Refs(source) {
		if text, ok := b.r.LibrarySource(b.ctx, ref.Name, ref.Version); ok {
			out[ref] = text
		}
	}
	return out
}

// data links a script to the tables one statement step reads and writes. A
// step whose table is named only when it runs links to one card per script
// and connection, however many such steps the script has (#2699).
func (b *builder) data(id string, sc *script.Script, ref scriptflow.DataRef) {
	if ref.Computed {
		node := "computed:" + id + ":" + ref.Connection
		b.node(Node{ID: node, Kind: NodeTable, Label: "table named when it runs", Group: GroupOutside, Computed: true, Detail: connDetail(ref.Connection)})
		kind := LinkReads
		if ref.Write || len(ref.Writes) > 0 {
			kind = LinkWrites
			b.unknownWrite(sc, ref.Connection)
		}
		b.link(Link{From: id, To: node, Kind: kind})
		return
	}
	for _, t := range ref.Reads {
		node := b.table(sc, ref.Connection, t)
		b.reads[node] = append(b.reads[node], id)
		b.link(Link{From: id, To: node, Kind: LinkReads})
	}
	for _, t := range ref.Writes {
		node := b.table(sc, ref.Connection, t)
		b.writes[node] = true
		b.link(Link{From: id, To: node, Kind: LinkWrites})
	}
}

// unknownWrite records a script that writes a table named when it runs, when
// that table could be in the schema: its connection is the schema's, the
// default, or computed.
func (b *builder) unknownWrite(sc *script.Script, connection string) {
	if b.app.Schema == "" || (connection != "" && connection != b.app.Connection) || slices.Contains(b.unknownWriters, sc.Name) {
		return
	}
	b.unknownWriters = append(b.unknownWriters, sc.Name)
}

func connDetail(connection string) []string {
	if connection == "" {
		return []string{"default connection"}
	}
	return []string{"connection " + connection}
}

// table is the node of a table a script names: in the application's schema
// when it is there, outside the application otherwise. A table in a schema
// of an application database the application does not hold is a finding.
func (b *builder) table(sc *script.Script, connection, table string) string {
	schema := sqlread.SchemaOf(table)
	name := table
	if _, after, ok := strings.CutLast(table, "."); ok {
		name = after
	}
	if b.app.Schema != "" && connection == b.app.Connection && b.ownSchema(table, schema) {
		id := b.schemaTableID(name)
		b.node(Node{ID: id, Kind: NodeTable, Label: name, Group: GroupSchema})
		return id
	}
	id := "table:" + connection + "/" + table
	b.node(Node{ID: id, Kind: NodeTable, Label: table, Group: GroupOutside, Detail: connDetail(connection)})
	if connection != "" && b.r.IsAppDB(b.ctx, connection) {
		b.finding(Finding{
			Kind: FindingForeignSchema, Node: "script:" + sc.ID,
			Message: fmt.Sprintf("script %s uses %s, in schema %s on connection %s, which this application does not hold",
				sc.Name, table, schema, connection),
		})
	}
	return id
}

// ownSchema reports whether a table a script names on the application's
// connection is the application's own: written without a schema, it is read
// in the schema of the environment the run is in, whose search path is that
// schema (#2709); written with one, the schema is the application's or one of
// its environments'.
func (b *builder) ownSchema(table, schema string) bool {
	if !strings.Contains(table, ".") || schema == b.app.Schema {
		return true
	}
	for _, e := range b.app.Environments {
		if e.Schema != "" && e.Schema == schema {
			return true
		}
	}
	return false
}

// output links a script to what one output step names.
func (b *builder) output(id, scriptID string, i int, out scriptflow.OutputRef) {
	switch out.Kind {
	case scriptflow.KindNotify, scriptflow.KindPublish:
		kind := LinkNotifies
		if out.Kind == scriptflow.KindPublish {
			kind = LinkPosts
		}
		node := "channel:" + out.Name
		label := out.Name
		if out.Computed {
			node, label = fmt.Sprintf("computed:%s:out%d", id, i), "channel named when it runs"
		}
		b.node(Node{ID: node, Kind: NodeChannel, Label: label, Group: GroupOutside, Computed: out.Computed})
		b.link(Link{From: id, To: node, Kind: kind})
	default:
		b.portalOutput(id, scriptID, i, out)
	}
}

// portalOutput links an export or a data refresh to the asset it writes: the
// application's asset whose output identity is the script's for that name.
func (b *builder) portalOutput(id, scriptID string, i int, out scriptflow.OutputRef) {
	kind := LinkExports
	if out.Kind == scriptflow.KindPublishData {
		kind = LinkRefresh
	}
	if out.Computed {
		node := fmt.Sprintf("computed:%s:out%d", id, i)
		b.node(Node{ID: node, Kind: NodeOutput, Label: "output named when it runs", Group: GroupOutside, Computed: true})
		b.link(Link{From: id, To: node, Kind: kind})
		return
	}
	if asset, ok := b.ids[script.OutputIdentityKey(scriptID, out.Name)]; ok && out.Portal {
		b.link(Link{From: id, To: asset, Kind: kind})
		return
	}
	node := "output:" + scriptID + ":" + out.Name
	b.node(Node{ID: node, Kind: NodeOutput, Label: out.Name, Group: GroupOutside})
	b.link(Link{From: id, To: node, Kind: kind})
}

// Endpoint and form references an asset's content can make.
var (
	endpointRef = regexp.MustCompile(`(?:mcp:endpoint:|/endpoints/)([A-Za-z0-9][A-Za-z0-9_-]*)`)
	formRef     = regexp.MustCompile(`(?:mcp:form:|/forms/)([A-Za-z0-9][A-Za-z0-9_-]*)`)
)

// asset links an asset to the endpoints it calls and the forms it embeds,
// read from its content and matched to the application's scripts' bindings.
func (b *builder) asset(p Part) {
	if _, _, ok := b.r.Asset(b.ctx, p.ID); !ok {
		return
	}
	content := b.r.AssetContent(b.ctx, p.ID, p.Version)
	from := "asset:" + p.ID
	for _, ref := range []struct {
		re   *regexp.Regexp
		kind string
	}{{endpointRef, LinkCalls}, {formRef, LinkEmbeds}} {
		for _, m := range ref.re.FindAllStringSubmatch(content, -1) {
			b.endpointLink(from, m[1], ref.kind)
		}
	}
}

func (b *builder) endpointLink(from, name, kind string) {
	if to, ok := b.scripts[name]; ok {
		b.link(Link{From: from, To: to, Kind: kind, Detail: name})
		return
	}
	node := "endpoint:" + name
	b.node(Node{ID: node, Kind: NodeEnd, Label: "/endpoints/" + name, Group: GroupOutside})
	b.link(Link{From: from, To: node, Kind: kind, Detail: name})
}

// schemaTables draws every table the application schema's migrations
// create, used by a script or not, so a table nothing uses is seen (#2665).
func (b *builder) schemaTables() {
	if b.app.Schema == "" {
		return
	}
	b.schema = b.r.SchemaTables(b.ctx, b.app.Connection, b.app.Schema)
	for _, name := range b.schema.Created {
		b.node(Node{ID: b.schemaTableID(name), Kind: NodeTable, Label: name, Group: GroupSchema})
	}
}

// schemaTableID is the node of a table of the application's schema.
func (b *builder) schemaTableID(name string) string {
	return "table:" + b.app.Connection + "/" + b.app.Schema + "." + name
}

// unwritten finds a table of the application's schema its scripts read that
// no script writes and no migration fills.
func (b *builder) unwritten() {
	if b.app.Schema == "" {
		return
	}
	filled := b.schema.Filled
	ids := make([]string, 0, len(b.reads))
	for id := range b.reads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	first := ""
	for _, id := range ids {
		n := b.d.Nodes[b.nodes[id]]
		if n.Group != GroupSchema || b.writes[id] || slices.Contains(filled, n.Label) {
			continue
		}
		if first == "" {
			first = id
		}
		b.finding(Finding{Kind: FindingUnwrittenTable, Node: id, Message: b.unwrittenMessage(n.Label)})
	}
	b.runtimeWriters(first)
}

// unwrittenMessage says a table has no writer the diagram knows of. With a
// script that writes a table named only when it runs, it does not claim no
// part writes it, since that script may (#2615).
func (b *builder) unwrittenMessage(table string) string {
	if len(b.unknownWriters) == 0 {
		return fmt.Sprintf("table %s.%s is read by the application's scripts and written by no part", b.app.Schema, table)
	}
	return fmt.Sprintf("table %s.%s is read by the application's scripts, and no part is known to write it; a script that writes a table named only when it runs may",
		b.app.Schema, table)
}

// runtimeWriters states once which scripts write a table named only when
// they run, when a table is reported unwritten (#2699): the list is not
// repeated on every table.
func (b *builder) runtimeWriters(firstUnwritten string) {
	if len(b.unknownWriters) == 0 || firstUnwritten == "" {
		return
	}
	sort.Strings(b.unknownWriters)
	msg := fmt.Sprintf("script %s writes a table named only when it runs, which may be a table of %s that no part is known to write",
		b.unknownWriters[0], b.app.Schema)
	if n := len(b.unknownWriters); n > 1 {
		msg = fmt.Sprintf("scripts %s and %s write tables named only when they run, which may be tables of %s that no part is known to write",
			strings.Join(b.unknownWriters[:n-1], ", "), b.unknownWriters[n-1], b.app.Schema)
	}
	b.finding(Finding{Kind: FindingRuntimeWrites, Node: firstUnwritten, Message: msg})
}

// Compare marks next against prev: each node and link added, removed (carried
// over from prev) or unchanged, a part whose version moved changed, and a
// link whose detail moved (a load's version) changed.
func Compare(next, prev *Diagram) *Diagram {
	next.ComparedWith = prev.Version
	compareNodes(next, prev)
	compareLinks(next, prev)
	return next
}

func compareNodes(next, prev *Diagram) {
	before := map[string]Node{}
	for _, n := range prev.Nodes {
		before[n.ID] = n
	}
	seen := map[string]bool{}
	for i := range next.Nodes {
		n := &next.Nodes[i]
		seen[n.ID] = true
		was, ok := before[n.ID]
		switch {
		case !ok:
			n.Change = ChangeAdded
		case was.Version != n.Version:
			n.Change, n.WasVersion = ChangeChanged, was.Version
		default:
			n.Change = ChangeUnchanged
		}
	}
	for _, n := range prev.Nodes {
		if !seen[n.ID] {
			n.Change = ChangeRemoved
			next.Nodes = append(next.Nodes, n)
		}
	}
}

// linkKey is what a link is matched across versions by.
func linkKey(l Link) string { return l.From + keySep + l.To + keySep + l.Kind }

// keySep joins the parts of a key; no id holds it.
const keySep = "\x00"

func compareLinks(next, prev *Diagram) {
	had := map[string]Link{}
	for _, l := range prev.Links {
		had[linkKey(l)] = l
	}
	has := map[string]bool{}
	for i := range next.Links {
		l := &next.Links[i]
		has[linkKey(*l)] = true
		was, ok := had[linkKey(*l)]
		switch {
		case !ok:
			l.Change = ChangeAdded
		case was.Detail != l.Detail:
			l.Change, l.WasDetail = ChangeChanged, was.Detail
		default:
			l.Change = ChangeUnchanged
		}
	}
	for _, l := range prev.Links {
		if !has[linkKey(l)] {
			l.Change = ChangeRemoved
			next.Links = append(next.Links, l)
		}
	}
}

// Problems is the findings as the lines manage_application get reports.
func Problems(d *Diagram) []string {
	out := make([]string, 0, len(d.Findings))
	for _, f := range d.Findings {
		out = append(out, f.Message)
	}
	return out
}
