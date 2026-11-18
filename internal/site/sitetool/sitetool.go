// Package sitetool is the manage_site MCP tool (#2518): a site's record, its
// environments, publishing a release, promoting and rolling back, mapping
// hostnames and the migration crawl, over the one service.
package sitetool

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/siteenv"
	"github.com/txn2/mcp-data-platform/internal/site/siterouting"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetraffic"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ToolName is the tool's name.
const ToolName = "manage_site"

// The commands.
const (
	CmdCreate       = "create"
	CmdGet          = "get"
	CmdList         = "list"
	CmdUpdate       = "update"
	CmdDelete       = "delete"
	CmdEnvironment  = "set_environment"
	CmdPublish      = "publish"
	CmdPromote      = "promote"
	CmdRollback     = "rollback"
	CmdReleases     = "releases"
	CmdDeployments  = "deployments"
	CmdAddDomain    = "add_domain"
	CmdRemoveDomain = "remove_domain"
	CmdDomains      = "domains"
	CmdCrawl        = "crawl"
	CmdGrant        = "grant"
	CmdRevoke       = "revoke"
	CmdSetDomain    = "set_domain"
	// CmdRemoveEnvironment removes an environment between dev and prod
	// (#2630).
	CmdRemoveEnvironment = "remove_environment"
)

// Commands is every command, in the order the schema lists them.
var Commands = []string{
	CmdCreate, CmdGet, CmdList, CmdUpdate, CmdDelete, CmdEnvironment, CmdPublish, CmdPromote, CmdRollback,
	CmdReleases, CmdDeployments, CmdAddDomain, CmdSetDomain, CmdRemoveDomain, CmdDomains, CmdCrawl, CmdGrant, CmdRevoke,
	CmdRemoveEnvironment,
}

// Reads is the commands that change nothing.
var Reads = []string{CmdGet, CmdList, CmdReleases, CmdDeployments, CmdDomains}

// allowedInRun is what a script run may do beyond reading: a build publishes
// to an environment that needs no approval. Anything that changes what a
// public address serves is a person's.
var allowedInRun = []string{CmdPublish}

// File is one file handed to publish.
type File struct {
	Path string `json:"path"`
	// Content is a pointer so an entry with none can be told from an empty
	// file: a forgotten content must not blank a page.
	Content *string `json:"content,omitempty"`
	// Encoding is utf-8 (default) or base64.
	Encoding string `json:"encoding,omitempty"`
	// Delete removes the path from the base.
	Delete bool `json:"delete,omitempty"`
}

// Input is the tool's arguments.
type Input struct {
	Command string `json:"command"`
	// Query, Limit and Offset narrow and page list (#2658).
	Query       string `json:"query,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	Site        string `json:"site,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	OwnerEmail  string `json:"owner_email,omitempty"`
	// InternalView, Public and Main are pointers so false can be told from
	// unset.
	InternalView *bool `json:"internal_view,omitempty"`
	Public       *bool `json:"public,omitempty"`
	// Primary and Mode place a domain among its environment's domains
	// (#2687): the primary, and serve or redirect.
	Primary *bool  `json:"primary,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Main    *bool  `json:"main,omitempty"`
	// MainEnvironment moves the main address on update; "" gives it to none.
	MainEnvironment *string  `json:"main_environment,omitempty"`
	Email           string   `json:"email,omitempty"`
	Persona         string   `json:"persona,omitempty"`
	Environments    []string `json:"environments,omitempty"`
	Level           string   `json:"level,omitempty"`
	Environment     string   `json:"environment,omitempty"`
	// RequiresApproval is a pointer so false can be told from unset.
	RequiresApproval *bool `json:"requires_approval,omitempty"`
	// KeepReleases is an environment's release limit: a whole number, a string
	// of digits, or "default" (#2602).
	KeepReleases json.RawMessage `json:"keep_releases,omitempty"`
	// Media, on update, is every resource folder the site serves; nil leaves
	// them, [] removes them all (#2591).
	Media   []site.Mount `json:"media,omitempty"`
	From    string       `json:"from,omitempty"`
	To      string       `json:"to,omitempty"`
	Release int          `json:"release,omitempty"`
	// After places an environment in the site's order (#2617).
	After string `json:"after,omitempty"`
	// Base makes the files an overlay on that release.
	Base   int    `json:"base,omitempty"`
	Note   string `json:"note,omitempty"`
	Reason string `json:"reason,omitempty"`
	Files  []File `json:"files,omitempty"`
	// Archive is a zip or gzipped tar managed resource, by id or URI.
	Archive string `json:"archive,omitempty"`
	// FromBucket is the objects under a bucket prefix on an S3 connection the
	// caller may use (#2593).
	FromBucket *bucketsource.Source `json:"from_bucket,omitempty"`
	// Folder is a managed-resource folder, in the library Scope and ScopeID
	// name.
	Folder      string            `json:"folder,omitempty"`
	Scope       string            `json:"scope,omitempty"`
	ScopeID     string            `json:"scope_id,omitempty"`
	Hostname    string            `json:"hostname,omitempty"`
	Application string            `json:"application,omitempty"`
	URL         string            `json:"url,omitempty"`
	FormMap     map[string]string `json:"form_map,omitempty"`
	// Status, Message, BackAt, RedirectTo and KeepPath change what an
	// environment answers (#2631, #2632); BackAt is RFC 3339.
	Status     string `json:"status,omitempty"`
	Message    string `json:"message,omitempty"`
	BackAt     string `json:"back_at,omitempty"`
	RedirectTo string `json:"redirect_to,omitempty"`
	KeepPath   *bool  `json:"keep_path,omitempty"`
	// RenameTo renames the environment (#2630).
	RenameTo string `json:"rename_to,omitempty"`
	// AgentsMayPromote is read only to refuse it: an administrator sets it in
	// the portal (#2643).
	AgentsMayPromote *bool `json:"agents_may_promote,omitempty"`
}

// Sources reads the files a publish names by reference.
type Sources interface {
	Archive(ctx context.Context, ref string) ([]sitesvc.InputFile, error)
	Folder(ctx context.Context, scope, scopeID, path string) ([]sitesvc.InputFile, error)
}

// BucketSources reads the files a publish names by a bucket prefix (#2593):
// CheckBucket refuses a source the caller cannot read, without listing it.
type BucketSources interface {
	CheckBucket(ctx context.Context, src bucketsource.Source) error
	Bucket(ctx context.Context, src bucketsource.Source) ([]sitesvc.InputFile, error)
}

// Crawler copies a site into a release's files.
type Crawler interface {
	Crawl(ctx context.Context, req sitecrawl.Request) (*sitecrawl.Result, error)
}

// TrafficReader reads the traffic a site's environments answered (#2647).
type TrafficReader interface {
	ByEnvironment(ctx context.Context, kind, subject, grain string, since time.Time) (map[string]sitetraffic.Counts, []string, error)
}

// Tool is the registered tool.
type Tool struct {
	Service   *sitesvc.Service
	Sources   Sources
	Crawler   Crawler
	PortalURL string
	// Traffic reads each site's traffic for get; nil reports none.
	Traffic TrafficReader
	// Routing reads each hostname's Site routing status (#2659); nil shows
	// none.
	Routing *siterouting.Lookup
}

const description = "Static sites served by the platform. AGENTS WORK IN dev, PEOPLE USE WHAT WAS PROMOTED: every publish, " +
	"patch and in-place edit lands in dev, the working copy, and nothing people rely on is served from it. You change dev, a " +
	"person reviews it, and promote moves the exact release to an environment people use (stage, prod). Those environments " +
	"are read-only: they move only by promote and rollback, with approval where required, and publish, patch or an edit " +
	"aimed at one is refused. Never try to change prod directly. " +
	"PUBLISH MAKES A RELEASE, PROMOTE MOVES ONE: publish turns files into a new release in dev and is the only way a release is " +
	"made; promote makes no new release and sends no files, it moves the release the previous environment serves to the next " +
	"one unchanged, so what reaches prod is exactly what was checked in dev and stage; rollback returns an environment to a " +
	"release it served before. Explain it to a person in those words. " +
	"ENVIRONMENTS: a new site has dev and prod. dev is always first and prod always last; set_environment places a new " +
	"environment between them with after, naming the environment it follows (dev, or one between; never prod; when there " +
	"is only one place it goes there), and moves one later the same way. PROMOTION GOES ONE STEP: an environment takes a release only from the environment immediately before it, and " +
	"only the release that one serves; promote names the target (to), and from, when given, must be the one before it. " +
	"ADDRESSES: every environment answers at <site>--<env>.<pages domain>; the main address, <site>.<pages domain>, serves " +
	"one environment the owner chooses (prod on a new site) or none; add_domain adds a hostname to any environment but dev. " +
	"No domain is needed: an environment works at its pages addresses for people who sign in. " +
	"ACCESS: every address on the pages domain asks for the platform's sign-in and opens only for administrators, the owner, " +
	"the people who manage the site and whoever the environment is shared with. ONLY AN ADDED DOMAIN CAN BE PUBLIC: add_domain " +
	"with public true, or set_domain with public true, opens that one domain to anyone (owner or administrator only; through " +
	"Approvals where the environment requires approval); a domain is added private otherwise, and set_domain public false makes " +
	"it private again. ONE HOSTNAME WINS: set_domain primary true marks an environment's canonical domain, and set_domain mode redirect on another " +
	"(www.example.com, or the bare example.com: either direction) answers it with a permanent redirect to the primary, path and query kept. It belongs " +
	"to the environment's domains, so it takes effect at once and promote and rollback leave it alone; remove_domain refuses a primary others redirect to. " +
	"grant shares: level view with email (a person) or persona (everyone holding it, read on every " +
	"request) and environments (the environments it opens; none is every one); level manage (internal people only, " +
	"site-wide) publishes, promotes, rolls back, adds domains and shares. Inside a private environment the site reads " +
	"/_viewer for who signed in, and when the environment is a part of an application (add_part kind site, part_id " +
	"\"<site>/<environment>\") its /endpoints/<name> run as that person, so the site decides its own finer rules. " +
	"OPERATIONS: set_environment with status offline (message, back_at) answers 503 on every address of the environment, with " +
	"status redirect (redirect_to, keep_path) answers 302, and status online serves its release again; rename_to renames an " +
	"environment between dev and prod; remove_environment removes one with no domain, no main address, no application part and " +
	"no job running. AN AGENT STOPS AT dev: promote, rollback, and add_domain or set_domain with public true on any other " +
	"environment ask the person (their client shows the question) or file a request a person decides in the portal, and answer " +
	"APPROVAL_REQUIRED; give the person the dev address and stop. An environment an administrator marked agents_may_promote is " +
	"the exception. " +
	"COMMANDS: publish writes a new release into dev (from files, an archive managed resource, a resource folder, or from_bucket, a prefix of a bucket on an S3 connection; a " +
	"_redirects file becomes its redirect map, a _headers file the policies each path is served with (Content-Security-Policy, " +
	"Content-Security-Policy-Report-Only, Permissions-Policy, Cross-Origin-Opener/Embedder/Resource-Policy, X-Frame-Options; a release " +
	"declaring no framing rule is served with frame-ancestors 'self'), a 404.html its not-found page). To change a site, do not resend it: " +
	"manage_asset reads and edits dev's files in place with mcp:site:<site>/dev/<path> (outline, locate, get_content, stats, " +
	"diff, and patch, whose version is the release number), and mcp:site:<site>/dev names the whole tree; publish with base " +
	"overlays the release dev serves now. A site with a _layouts folder is rendered at publish (Liquid layouts, includes, " +
	"_data records, front matter). promote serves the release of the environment before the target, rollback returns one to a release it served before; " +
	"neither copies a file. set_environment creates, places or changes an environment (after, internal_view, requires_approval, " +
	"keep_releases, main true to give it the main address); update with media mounts resource folders at URL prefixes (images and " +
	"files outside releases, public at a public domain); RELEASES ARE KEPT PER ENVIRONMENT: each keeps its last keep_releases " +
	"releases (the deployment's default, 50 unless an administrator changed it), and a daily sweep removes the rest, so a rollback " +
	"reaches only a kept release; update with main_environment moves the main address (\"\" for none). An " +
	"environment that requires approval is reached only by promote: an administrator's promote records its own approval, " +
	"anyone else's files a request in manage_approval and goes through when they promote again after it is approved. crawl " +
	"copies an existing site into dev. \"default\" is the old name of dev and is read as dev for one release. Read fetch " +
	"mcp:knowledge_page:platform-pages-and-sites first."

const (
	keyType   = "type"
	keyDesc   = "description"
	keyEnum   = "enum"
	keyReq    = "required"
	keyProps  = "properties"
	keyItems  = "items"
	typString = "string"
	typObject = "object"
	typArray  = "array"
	typBool   = "boolean"
	keySite   = "site"
	// keyRemoved names what a remove command took away.
	keyRemoved = "removed"
	keyFolder  = "folder"
	keyPrefix  = "prefix"
	keyScope   = "scope"
)

func schema() map[string]any {
	str := func(desc string) map[string]any { return map[string]any{keyType: typString, keyDesc: desc} }
	num := func(desc string) map[string]any { return map[string]any{keyType: "integer", keyDesc: desc} }
	return map[string]any{
		keyType: typObject, keyReq: []string{"command"},
		keyProps: map[string]any{
			"command":     map[string]any{keyType: typString, keyEnum: Commands, keyDesc: "The operation."},
			keySite:       str("The site, by name or id (every command but create, list and domains)."),
			"name":        str("create: the site's name, a lowercase slug."),
			keyDesc:       str("create, update: what the site is."),
			"owner_email": str("update: move the site to another owner."),
			"query":       str("list: the sites a part of whose name, description, owner or domain hostname this is, any case; every site without it."),
			"limit":       num("list: how many sites to return, at most 200; every match without it."),
			"offset":      num("list: how many matching sites to skip."),
			"media": map[string]any{
				keyType: typArray,
				keyDesc: "update: EVERY managed-resource folder the site serves as media, replacing the list ([] removes them all). Each is " +
					"{prefix, scope, scope_id, folder}: prefix the URL path it answers at (\"/media/\"), scope global, persona or user " +
					"(scope_id the persona or the person's subject), folder the folder in that library. A file under a mount is served on every " +
					"environment after the release's own files, so a release file at the same path wins, and it is PUBLIC on a public " +
					"environment: mounting publishes the folder. You must manage the site and be able to read the folder. Media sits outside " +
					"releases (publish, promote and rollback leave it alone, and it counts toward no release limit), a file replaced with " +
					"manage_resource replace_content shows at once, HTML, SVG, JavaScript and XML in a mount are sent as downloads, and a file in " +
					"a mounted folder cannot be deleted unless forced.",
				keyItems: map[string]any{keyType: typObject, keyReq: []string{keyPrefix, keyScope, keyFolder}, keyProps: map[string]any{
					keyPrefix: str("The URL path, \"/media/\"."), keyScope: str("global, persona or user."),
					"scope_id": str("The persona's name or the person's subject; none for global."), keyFolder: str("The folder in the library."),
				}},
			},
			"internal_view":     map[string]any{keyType: typBool, keyDesc: "set_environment: open this private environment to everyone signed in whose persona is not external."},
			"public":            map[string]any{keyType: typBool, keyDesc: "add_domain, set_domain: true opens this one domain to anyone (owner or administrator only; through Approvals where the environment requires approval), false makes it private. A domain is added private unless this is true. Every address on the pages domain is always private, so set_environment refuses public."},
			"after":             str("set_environment: put the environment immediately after this one (dev, or one between dev and prod; never after prod). With a new environment, or to move one. add_domain creating an environment: the same."),
			"main":              map[string]any{keyType: typBool, keyDesc: "set_environment: true gives this environment the main address, <site>.<pages domain> (owner or administrator)."},
			"main_environment":  str("update: the environment the main address serves; \"\" gives it to none, and it answers not found (owner or administrator)."),
			"email":             str("grant, revoke: the person, by the email they sign in with."),
			"persona":           str("grant, revoke: a persona in place of email; everyone holding it, read on every request. Always level view."),
			"environments":      map[string]any{keyType: typArray, keyItems: map[string]any{keyType: typString}, keyDesc: "grant with level view: the private environments it opens; none is every private environment."},
			"level":             map[string]any{keyType: typString, keyEnum: []string{site.LevelManage, site.LevelView}, keyDesc: "grant: manage publishes, promotes, rolls back, adds domains and shares (internal people only, site-wide); view opens the private environments it names and reads their current files, and changes nothing."},
			"environment":       str("publish: always dev (it may be left out); any other environment is refused, and moves by promote. rollback, set_environment, deployments: the environment. add_domain: the environment the hostname serves, created private when it does not exist; never dev."),
			"requires_approval": map[string]any{keyType: typBool, keyDesc: "set_environment, or add_domain creating an environment: promotions here, and making one of its domains public, go through the Approvals queue."},
			"keep_releases": map[string]any{keyType: []string{"integer", typString}, keyDesc: "set_environment, or add_domain creating an environment: " +
				"how many releases it keeps, the last N distinct releases it pointed at (0 keeps every one), or \"default\" for the deployment's default. " +
				"A daily sweep removes every release no environment keeps (one an application release pins or a pending approval names stays), " +
				"and the files only those releases held: a removed release cannot be rolled back to. Only the owner or an administrator changes it."},
			"from":    str("promote: the environment whose release moves; it must be the one immediately before to, which is used when from is left out."),
			"to":      str("promote: the environment that moves."),
			"release": num("promote: the release, which must be the one the environment before to serves. rollback: the release to return to (default the one before)."),
			"base":    num("publish: overlay this release instead of replacing the file set. It must be the release dev serves now, so read it first; files you do not send carry over."),
			"note":    str("publish: what the release is for."),
			"reason":  str("promote, add_domain and set_domain making a domain public: why, for the approver."),
			"files": map[string]any{
				keyType: typArray, keyDesc: "publish: the files, up to the inline size a tool call carries.",
				keyItems: map[string]any{keyType: typObject, keyReq: []string{"path"}, keyProps: map[string]any{
					"path": str("The file's path in the site."), "content": str("Its content. Required unless delete is true."),
					"encoding": map[string]any{keyType: typString, keyEnum: []string{"utf-8", "base64"}, keyDesc: "How content is written (default utf-8)."},
					"delete":   map[string]any{keyType: typBool, keyDesc: "With base: remove this path from the release."},
				}},
			},
			"archive": str("publish: a zip or gzipped tar managed resource, by id or mcp:// URI."),
			keyFolder: str("publish: a managed-resource folder path."),
			"from_bucket": map[string]any{
				keyType: typObject, keyReq: []string{"connection", "bucket"},
				keyDesc: "publish: the objects under a prefix of a bucket on an S3 connection, each at its path under the prefix, so a " +
					"source tree of thousands of files arrives without a tool call carrying it. It is read as you: your persona must " +
					"hold s3_list and s3_object and the connection, and the bucket must be inside its bucket_prefix; the release " +
					"limits apply and are checked before an object is read.",
				keyProps: map[string]any{
					"connection": str("The S3 connection, by name; always named."),
					"bucket":     str("The bucket."),
					keyPrefix:    str("The folder in the bucket, \"exports/site/\"; empty for the whole bucket."),
				},
			},
			keyScope:   str("publish with folder: the library's scope (user, persona, global; default user)."),
			"scope_id": str("publish with folder: the persona name or user subject."),
			"hostname": str("add_domain, set_domain, remove_domain: the hostname."),
			"primary": map[string]any{keyType: typBool, keyDesc: "add_domain, set_domain: true makes this domain its environment's primary, the canonical hostname " +
				"the environment's redirect domains send visitors to; the previous primary stops being primary and keeps serving. A primary always serves."},
			"mode": map[string]any{keyType: typString, "enum": []string{"serve", "redirect"}, keyDesc: "add_domain, set_domain: serve (the default) serves the release " +
				"here; redirect answers every request with a permanent redirect (301, 308 for a method other than GET and HEAD) to the environment's primary, " +
				"path and query kept, and serves nothing. It needs a primary on the environment."},
			"application": str("add_domain: the application whose endpoints and pages the hostname serves, in place of site and environment."),
			"url":         str("crawl: the site to copy."),
			"status": map[string]any{
				keyType: typString, keyEnum: []string{site.StatusOnline, site.StatusOffline, site.StatusRedirect},
				keyDesc: "set_environment: what every address of the environment answers. offline answers 503 with message (and back_at, " +
					"what visitors are told), redirect answers 302 to redirect_to, online serves its release again. Never dev. The " +
					"release pointer is untouched, so promote and rollback keep working meanwhile. Anyone who manages the site sets it at " +
					"once; a redirect on an environment that requires approval, set by someone who is not an administrator, waits in Approvals.",
			},
			"message":     str("set_environment with status offline: what the offline page says, plain text, at most 1,000 characters."),
			"back_at":     str("set_environment with status offline: when visitors are told to expect it back, RFC 3339 (2026-11-14T14:00:00Z). Only what they are told: it stays offline until set online."),
			"redirect_to": str("set_environment with status redirect: an https:// address on another site; never this platform's own hosts."),
			"keep_path":   map[string]any{keyType: typBool, keyDesc: "set_environment with status redirect: append the request's path and query to redirect_to."},
			"rename_to":   str("set_environment: rename an environment between dev and prod, alone. Every reference moves; its old <site>--<env> address stops answering and mcp:site:<site>/<old> references stop resolving."),
			"form_map": map[string]any{
				keyType: typObject, "additionalProperties": map[string]any{keyType: typString},
				keyDesc: "crawl: for a page path, the platform form endpoint that replaces its forms.",
			},
		},
	}
}

// ops is the environment operations over the tool's service.
func (t *Tool) ops() siteenv.Ops { return siteenv.New(t.Service) }

// Register adds manage_site to the server.
func (t *Tool) Register(server *mcp.Server) {
	if t == nil || t.Service == nil || server == nil {
		return
	}
	toolkit.AddTool(server, &mcp.Tool{
		Name: ToolName, Title: "Manage Sites", Description: description, InputSchema: schema(),
		Annotations: toolkit.WriteAnnotations(true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		return t.HandleRequest(ctx, req, in)
	})
}

func actorOf(ctx context.Context) sitesvc.Actor {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return sitesvc.Actor{}
	}
	verified, stated := pc.UserClaims[browsersession.ClaimEmailVerified].(bool)
	// Every manage_site call is an agent acting for the person (#2643).
	return sitesvc.Actor{Email: pc.UserEmail, Admin: pc.IsAdmin, Persona: pc.PersonaName, EmailUnverified: stated && !verified, Agent: true}
}

func insideRun(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && pc.Source == middleware.SourceScript
}

// Handle runs one command with no request to ask the person through: a move
// that waits on them is filed in Approvals.
func (t *Tool) Handle(ctx context.Context, in Input) (*mcp.CallToolResult, any, error) {
	return t.HandleRequest(ctx, nil, in)
}

// HandleRequest runs one command. A move of an environment people use waits
// on the person (#2643): their client asks them, or the request is filed.
func (t *Tool) HandleRequest(ctx context.Context, req *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
	if !t.Service.Config.IsEnabled() {
		return errorResult("sites are turned off on this deployment (sites.enabled is false)")
	}
	if !slices.Contains(Reads, in.Command) && !slices.Contains(allowedInRun, in.Command) && insideRun(ctx) {
		return errorResult(ToolName + " " + in.Command + " cannot be called from inside a script run: what a public address serves is changed from a person's session.")
	}
	h, ok := t.handlers()[in.Command]
	if !ok {
		return errorResult(fmt.Sprintf("unknown command %q", in.Command))
	}
	aliased := canonicalize(&in)
	actor := actorOf(ctx)
	run := func(a sitesvc.Actor) (any, error) { return h(ctx, a, in) }
	if key, answer := answerOf(req); answer != nil {
		return answered(actor, key, answer, run), nil, nil
	}
	v, err := run(actor)
	if wait, ok := errors.AsType[*siteenv.PersonDecisionError](err); ok {
		return t.personDecides(ctx, req, actor, wait, run), nil, nil
	}
	if err != nil {
		return errorResult(message(err))
	}
	if aliased {
		return jsonResult(withNote(v))
	}
	return jsonResult(v)
}

type handler func(ctx context.Context, actor sitesvc.Actor, in Input) (any, error)

// canonicalize reads the old name of dev wherever an environment is named,
// and reports whether it did.
func canonicalize(in *Input) bool {
	aliased := false
	for _, p := range []*string{&in.Environment, &in.From, &in.To, in.MainEnvironment} {
		if p == nil {
			continue
		}
		var a bool
		*p, a = site.CanonicalEnvironment(*p)
		aliased = aliased || a
	}
	for i := range in.Environments {
		var a bool
		in.Environments[i], a = site.CanonicalEnvironment(in.Environments[i])
		aliased = aliased || a
	}
	return aliased
}

// withNote is a result with the alias note beside it.
func withNote(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	out := map[string]any{}
	if json.Unmarshal(raw, &out) != nil {
		return map[string]any{"result": v, "note": site.AliasNote}
	}
	out["note"] = site.AliasNote
	return out
}

func (t *Tool) handlers() map[string]handler {
	s := t.Service
	return map[string]handler{
		CmdCreate: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Create(ctx, a, in.Name, in.Description)
		},
		CmdGet: t.get,
		CmdList: func(ctx context.Context, _ sitesvc.Actor, in Input) (any, error) {
			return listSites(ctx, s, in)
		},
		CmdUpdate: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			u := sitesvc.UpdateRequest{}
			if in.MainEnvironment != nil && in.Description == "" && in.OwnerEmail == "" && in.Media == nil {
				return s.SetMainEnvironment(ctx, a, in.Site, *in.MainEnvironment)
			}
			if in.MainEnvironment != nil {
				if _, err := s.SetMainEnvironment(ctx, a, in.Site, *in.MainEnvironment); err != nil {
					return nil, err //nolint:wrapcheck // the caller's message
				}
			}
			if in.Description != "" {
				u.Description = &in.Description
			}
			if in.OwnerEmail != "" {
				u.Owner = &in.OwnerEmail
			}
			if in.Media != nil {
				u.Media = &in.Media
			}
			return s.Update(ctx, a, in.Site, u)
		},
		CmdDelete: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{"deleted": in.Site}, s.Delete(ctx, a, in.Site)
		},
		CmdEnvironment: t.setEnvironment,
		CmdPublish:     t.publish,
		CmdPromote: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return t.ops().Promote(ctx, a, sitesvc.PromoteRequest{Site: in.Site, From: in.From, To: in.To, Release: in.Release, Reason: in.Reason})
		},
		CmdRollback: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return t.ops().Rollback(ctx, a, in.Site, in.Environment, in.Release)
		},
		CmdReleases:    t.releases,
		CmdDeployments: t.deployments,
		CmdAddDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			keep, err := site.ParseKeep(in.KeepReleases)
			if err != nil {
				return nil, &sitesvc.RefusedError{Msg: err.Error()}
			}
			res, err := t.ops().AddDomainWithRole(ctx, a, sitesvc.DomainRequest{
				Hostname: in.Hostname, Site: in.Site, Environment: in.Environment, Application: in.Application, Reason: in.Reason,
				RequiresApproval: in.RequiresApproval, Public: in.Public != nil && *in.Public, Keep: keep.Keep,
				Place: placeOf(in),
			}, siteenv.DomainRole{Primary: in.Primary, Mode: in.Mode})
			if err == nil && res != nil && res.DomainResult != nil && res.Domain != nil {
				// A domain just added is pending until the routing script
				// answers for it (#2659).
				res.Routing = t.Routing.Of(ctx, a.Admin, []string{res.Hostname})[res.Hostname]
			}
			return res, err //nolint:wrapcheck // the caller's message
		},
		CmdSetDomain: t.setDomain,
		CmdRemoveDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{keyRemoved: in.Hostname}, t.ops().RemoveDomain(ctx, a, in.Hostname)
		},
		CmdDomains: func(ctx context.Context, a sitesvc.Actor, _ Input) (any, error) {
			list, err := s.Store.Domains(ctx)
			if err != nil {
				return nil, err //nolint:wrapcheck // the caller's message
			}
			return map[string]any{"domains": t.Routing.Domains(ctx, a.Admin, list)}, nil
		},
		CmdCrawl: t.crawl,
		CmdGrant: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Grant(ctx, a, in.Site, sitesvc.GrantRequest{Email: in.Email, Persona: in.Persona, Level: in.Level, Environments: in.Environments})
		},
		CmdRevoke: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{"revoked": cmp.Or(in.Email, in.Persona)}, s.Revoke(ctx, a, in.Site, in.Email, in.Persona)
		},
		CmdRemoveEnvironment: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			st, err := t.ops().RemoveEnvironment(ctx, a, in.Site, in.Environment)
			if err != nil {
				return nil, err //nolint:wrapcheck // the caller's message
			}
			return map[string]any{keyRemoved: in.Environment, keySite: st}, nil
		},
	}
}

// setEnvironment creates or changes an environment and, with main true, gives
// it the main address. rename_to renames it (#2630), alone; status changes
// what it answers (#2631, #2632), after any setting given with it.
func (t *Tool) setEnvironment(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
	if in.RenameTo != "" {
		if settingsGiven(in) || in.Status != "" {
			return nil, &sitesvc.RefusedError{Msg: "rename_to renames the environment and nothing else: change its settings or status in another call"}
		}
		st, err := t.ops().RenameEnvironment(ctx, a, in.Site, in.Environment, in.RenameTo)
		if err != nil {
			return nil, err //nolint:wrapcheck // the caller's message
		}
		return &sitesvc.EnvironmentResult{Site: st, Status: sitesvc.StatusDone}, nil
	}
	if in.Status != "" && !settingsGiven(in) {
		return t.setStatus(ctx, a, in)
	}
	keep, err := site.ParseKeep(in.KeepReleases)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	res, err := t.ops().SetEnvironment(ctx, a, in.Site, in.Environment, sitesvc.EnvironmentChange{
		RequiresApproval: in.RequiresApproval, Public: in.Public, InternalView: in.InternalView,
		AgentsMayPromote: in.AgentsMayPromote, Keep: keep.Keep, KeepDefault: keep.Default, Place: placeOf(in),
	})
	if err == nil && in.Status != "" {
		return t.setStatus(ctx, a, in)
	}
	if err != nil || in.Main == nil || !*in.Main {
		return res, err //nolint:wrapcheck // the caller's message
	}
	if res.Site, err = t.Service.SetMainEnvironment(ctx, a, in.Site, in.Environment); err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return res, nil
}

// settingsGiven reports whether a set_environment call changes a setting, not
// only the status.
func settingsGiven(in Input) bool {
	return in.RequiresApproval != nil || in.Public != nil || in.InternalView != nil || in.AgentsMayPromote != nil ||
		len(in.KeepReleases) > 0 || in.After != "" || in.Main != nil
}

// setStatus takes the environment offline, redirects it, or brings it online.
func (t *Tool) setStatus(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
	c := siteenv.StatusChange{Status: in.Status, Message: in.Message, RedirectTo: in.RedirectTo, KeepPath: in.KeepPath != nil && *in.KeepPath, Reason: in.Reason}
	if in.BackAt != "" {
		at, err := time.Parse(time.RFC3339, in.BackAt)
		if err != nil {
			return nil, &sitesvc.RefusedError{Msg: "back_at: a time in RFC 3339 form, as 2026-11-14T14:00:00Z"}
		}
		c.BackAt = &at
	}
	return t.ops().SetStatus(ctx, a, in.Site, in.Environment, c) //nolint:wrapcheck // the caller's message
}

// placeOf is the place a call names, nil for none.
func placeOf(in Input) *site.Place {
	if in.After == "" {
		return nil
	}
	return &site.Place{After: in.After}
}

// get is a site with where each environment answers, its domains with each
// one's access, and, for someone who manages it, who has access.
func (t *Tool) get(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	st, err := t.Service.Find(ctx, in.Site)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	domains, err := t.Service.Store.SiteDomains(ctx, st.ID)
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}
	at := t.Service.Addressing()
	routed, pagesRouting := t.Routing.Site(ctx, actor.Admin, st, domains)
	out := map[string]any{keySite: st, "domains": routed, "addresses": at.Addresses(st, domains), "pages_addresses": at.PagesAddresses(st)}
	if pagesRouting != nil {
		// The Site routing script's status for each pages address (#2659).
		out["pages_routing"] = pagesRouting
	}
	out["production_address"], _ = at.ProductionAddress(st, domains)
	if note := at.AddressNote(st); note != "" {
		out["address_note"] = note
	}
	if traffic := t.traffic(ctx, st); traffic != nil {
		out["traffic"] = traffic
	}
	perms, err := t.Service.PermissionsOf(ctx, actor, st)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	if perms.Manage {
		if out["grants"], err = t.Service.Store.Grants(ctx, st.ID); err != nil {
			return nil, fmt.Errorf("listing grants: %w", err)
		}
	}
	return out, nil
}

// TrafficSummary is what get reports of a site's last 24 hours (#2647):
// requests and latency for the whole site and for each environment, so an
// agent asked whether the site is slow answers from it.
type TrafficSummary struct {
	Range        string                        `json:"range"`
	Recording    bool                          `json:"recording"`
	Total        sitetraffic.Totals            `json:"total"`
	Environments map[string]sitetraffic.Totals `json:"environments"`
}

// traffic is the site's summary, nil where there is no reader; a failed read
// is logged and left out rather than failing the read of the site.
func (t *Tool) traffic(ctx context.Context, st *site.Site) *TrafficSummary {
	if t.Traffic == nil {
		return nil
	}
	day, _ := sitetraffic.RangeOf("24h")
	by, _, err := t.Traffic.ByEnvironment(ctx, sitetraffic.KindSite, st.ID, day.Grain, day.Since(time.Now()))
	if err != nil {
		slog.WarnContext(ctx, "sites: reading a site's traffic failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	out := &TrafficSummary{Range: day.Name, Recording: t.Service.Config.Traffic.IsEnabled(), Environments: map[string]sitetraffic.Totals{}}
	if t.Service.Live != nil {
		out.Recording = t.Service.Live.Get().Traffic.IsEnabled()
	}
	var all sitetraffic.Counts
	for env, c := range by {
		out.Environments[env] = sitetraffic.TotalsOf(c)
		all.Add(c)
	}
	out.Total = sitetraffic.TotalsOf(all)
	return out
}

func (t *Tool) releases(ctx context.Context, _ sitesvc.Actor, in Input) (any, error) {
	st, err := t.Service.Find(ctx, in.Site)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	list, err := t.Service.Store.Releases(ctx, st.ID)
	if err != nil {
		return nil, fmt.Errorf("listing releases: %w", err)
	}
	return map[string]any{keySite: st.Name, "releases": list}, nil
}

func (t *Tool) deployments(ctx context.Context, _ sitesvc.Actor, in Input) (any, error) {
	st, err := t.Service.Find(ctx, in.Site)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	list, err := t.Service.Store.Deployments(ctx, st.ID, in.Environment, 0)
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	return map[string]any{keySite: st.Name, "deployments": list}, nil
}

// publish reads the one source the call names and publishes it. A source the
// platform reads (an archive, a folder, a bucket prefix) is read only for a
// caller who may publish to the site: who may not learns nothing of it, and
// nothing is read on their behalf.
func (t *Tool) publish(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	if in.Archive != "" || in.Folder != "" || in.FromBucket != nil {
		if err := t.Service.MayPublish(ctx, actor, in.Site, in.Environment); err != nil {
			return nil, err //nolint:wrapcheck // the caller's message
		}
	}
	set, err := t.filesOf(ctx, in)
	if err != nil {
		return nil, err
	}
	return t.Service.Publish(ctx, actor, sitesvc.PublishRequest{ //nolint:wrapcheck // the caller's message
		Site: in.Site, Environment: in.Environment, Source: set.source, Note: in.Note, Files: set.files, Base: in.Base, Remove: set.removed,
	})
}

// fileSet is what a publish was handed.
type fileSet struct {
	files   []sitesvc.InputFile
	removed []string
	source  string
}

func (t *Tool) filesOf(ctx context.Context, in Input) (*fileSet, error) {
	named := 0
	for _, set := range []bool{len(in.Files) > 0, in.Archive != "", in.Folder != "", in.FromBucket != nil} {
		if set {
			named++
		}
	}
	if named != 1 {
		return nil, &sitesvc.RefusedError{Msg: "publish names one source: files, archive, folder or from_bucket"}
	}
	if in.FromBucket != nil {
		return t.bucketFiles(ctx, *in.FromBucket)
	}
	if in.Archive == "" && in.Folder == "" {
		files, removed, err := decodeFiles(in.Files)
		return &fileSet{files: files, removed: removed, source: "files (" + strconv.Itoa(len(in.Files)) + ")"}, err
	}
	if t.Sources == nil {
		return nil, &sitesvc.RefusedError{Msg: "this deployment has no managed resources to publish from"}
	}
	if in.Archive != "" {
		files, err := t.Sources.Archive(ctx, in.Archive)
		return &fileSet{files: files, source: in.Archive}, err //nolint:wrapcheck // the caller's message
	}
	files, err := t.Sources.Folder(ctx, in.Scope, in.ScopeID, in.Folder)
	return &fileSet{files: files, source: in.Folder}, err //nolint:wrapcheck // the caller's message
}

// bucketFiles reads a publish's files from a bucket prefix.
func (t *Tool) bucketFiles(ctx context.Context, src bucketsource.Source) (*fileSet, error) {
	bs, ok := t.Sources.(BucketSources)
	if !ok {
		return nil, &sitesvc.RefusedError{Msg: "this deployment cannot publish from a bucket"}
	}
	files, err := bs.Bucket(ctx, src)
	return &fileSet{files: files, source: src.String()}, err //nolint:wrapcheck // the caller's message
}

// decodeFiles reads a publish's file entries into the files it writes and the
// paths it removes.
func decodeFiles(in []File) (files []sitesvc.InputFile, removed []string, err error) {
	files = make([]sitesvc.InputFile, 0, len(in))
	for _, f := range in {
		switch {
		case f.Delete && f.Content != nil:
			return nil, nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("file %q: send content or delete, not both", f.Path)}
		case f.Delete:
			removed = append(removed, f.Path)
			continue
		case f.Content == nil:
			return nil, nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("file %q: content is required unless delete is true", f.Path)}
		}
		body := []byte(*f.Content)
		if f.Encoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(*f.Content)
			if err != nil {
				return nil, nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("file %q: content is not base64", f.Path)}
			}
			body = decoded
		}
		files = append(files, sitesvc.InputFile{Path: f.Path, Body: body})
	}
	return files, removed, nil
}

// crawl copies a site and publishes it as a release.
func (t *Tool) crawl(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	if t.Crawler == nil {
		return nil, &sitesvc.RefusedError{Msg: "this deployment cannot crawl a site"}
	}
	if err := t.Service.MayPublish(ctx, actor, in.Site, in.Environment); err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	got, err := t.Crawler.Crawl(ctx, sitecrawl.Request{Start: in.URL, FormMap: in.FormMap, PortalURL: t.PortalURL})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	pub, err := t.Service.Publish(ctx, actor, sitesvc.PublishRequest{
		Site: in.Site, Environment: in.Environment, Source: "crawl " + sitecrawl.SourceOf(in.URL), Note: in.Note, Files: got.Files,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	skipped := got.Skipped
	if skipped == nil {
		skipped = []site.Skipped{}
	}
	return map[string]any{"published": pub, "pages": got.Pages, "redirects": got.Redirects, "skipped": skipped}, nil
}

func message(err error) string {
	if refused, ok := errors.AsType[*sitesvc.RefusedError](err); ok {
		return refused.Msg
	}
	if errors.Is(err, site.ErrNotFound) {
		return "no such site: " + err.Error()
	}
	return err.Error()
}

func errorResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := wirejson.Marshal(v)
	if err != nil {
		return errorResult("failed to encode the result: " + err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}

// maxListLimit is the most sites one list answers.
const maxListLimit = 200

// listSites is the sites in.Query names, in.Limit at a time from in.Offset,
// each with its production address (#2658): the rule the portal's list reads.
func listSites(ctx context.Context, s *sitesvc.Service, in Input) (any, error) {
	if in.Limit < 0 || in.Limit > maxListLimit || in.Offset < 0 {
		return nil, fmt.Errorf("limit is 0 to %d and offset 0 or more", maxListLimit)
	}
	list, err := s.Store.List(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	domains, err := s.Store.Domains(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}
	bySite := map[string][]site.Domain{}
	for _, d := range domains {
		bySite[d.SiteID] = append(bySite[d.SiteID], d)
	}
	list = slices.DeleteFunc(list, func(st site.Site) bool { return !site.Matches(&st, bySite[st.ID], in.Query) })
	total := len(list)
	list = pageOf(list, in.Offset, in.Limit)
	at := s.Addressing()
	addresses := make(map[string]string, len(list))
	for i := range list {
		addresses[list[i].Name], _ = at.ProductionAddress(&list[i], bySite[list[i].ID])
	}
	return map[string]any{"sites": list, "total": total, "production_addresses": addresses}, nil
}

// pageOf is list from offset, at most limit of it when limit is set.
func pageOf(list []site.Site, offset, limit int) []site.Site {
	if offset >= len(list) {
		return []site.Site{}
	}
	list = list[offset:]
	if limit > 0 && limit < len(list) {
		list = list[:limit]
	}
	return list
}

// setDomain changes a domain's access, its place among its environment's
// domains (#2687), or both.
func (t *Tool) setDomain(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
	res, err := t.ops().SetDomain(ctx, a, in.Hostname, siteenv.DomainChange{
		Public: in.Public, Reason: in.Reason, Role: siteenv.DomainRole{Primary: in.Primary, Mode: in.Mode},
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return res, nil
}
