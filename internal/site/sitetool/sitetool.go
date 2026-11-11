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
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
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
)

// Commands is every command, in the order the schema lists them.
var Commands = []string{
	CmdCreate, CmdGet, CmdList, CmdUpdate, CmdDelete, CmdEnvironment, CmdPublish, CmdPromote, CmdRollback,
	CmdReleases, CmdDeployments, CmdAddDomain, CmdRemoveDomain, CmdDomains, CmdCrawl, CmdGrant, CmdRevoke,
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
	Command     string `json:"command"`
	Site        string `json:"site,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	OwnerEmail  string `json:"owner_email,omitempty"`
	// InternalView, Public and Main are pointers so false can be told from
	// unset.
	InternalView *bool `json:"internal_view,omitempty"`
	Public       *bool `json:"public,omitempty"`
	Main         *bool `json:"main,omitempty"`
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

// Tool is the registered tool.
type Tool struct {
	Service   *sitesvc.Service
	Sources   Sources
	Crawler   Crawler
	PortalURL string
}

const description = "Static sites served by the platform. AGENTS WORK IN dev, PEOPLE USE WHAT WAS PROMOTED: every publish, " +
	"patch and in-place edit lands in dev, the working copy, and nothing people rely on is served from it. You change dev, a " +
	"person reviews it, and promote moves the exact release to an environment people use (stage, prod). Those environments " +
	"are read-only: they move only by promote and rollback, with approval where required, and publish, patch or an edit " +
	"aimed at one is refused. Never try to change prod directly. " +
	"ADDRESSES: every environment answers at <site>--<env>.<pages domain>; the main address, <site>.<pages domain>, serves " +
	"one environment the owner chooses (dev on a new site) or none; add_domain adds a hostname to any environment but dev. " +
	"ACCESS: each environment is private or public. Private (every new environment, and dev always) asks for the platform's " +
	"sign-in and opens only for administrators, the owner, the people who manage the site and whoever it is shared with; " +
	"public opens for anyone, and only the owner or an administrator makes an environment public (through Approvals where " +
	"it requires approval). grant shares: level view with email (a person) or persona (everyone holding it, read on every " +
	"request) and environments (the private environments it opens; none is every one); level manage (internal people only, " +
	"site-wide) publishes, promotes, rolls back, adds domains and shares. Inside a private environment the site reads " +
	"/_viewer for who signed in, and when the environment is a part of an application (add_part kind site, part_id " +
	"\"<site>/<environment>\") its /endpoints/<name> run as that person, so the site decides its own finer rules. " +
	"COMMANDS: publish writes a new release into dev (from files, an archive managed resource, a resource folder, or from_bucket, a prefix of a bucket on an S3 connection; a " +
	"_redirects file becomes its redirect map, a 404.html its not-found page). To change a site, do not resend it: " +
	"manage_asset reads and edits dev's files in place with mcp:site:<site>/dev/<path> (outline, locate, get_content, stats, " +
	"diff, and patch, whose version is the release number), and mcp:site:<site>/dev names the whole tree; publish with base " +
	"overlays the release dev serves now. A site with a _layouts folder is rendered at publish (Liquid layouts, includes, " +
	"_data records, front matter). promote points an environment at another's release, rollback at one it served before; " +
	"neither copies a file. set_environment creates or changes an environment (public, internal_view, requires_approval, " +
	"keep_releases, main true to give it the main address); update with media mounts resource folders at URL prefixes (images and " +
	"files outside releases, public on a public environment); RELEASES ARE KEPT PER ENVIRONMENT: each keeps its last keep_releases " +
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
	keyFolder = "folder"
	keyPrefix = "prefix"
	keyScope  = "scope"
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
			"public":            map[string]any{keyType: typBool, keyDesc: "set_environment, or add_domain creating an environment: true opens it to anyone (owner or administrator only; through Approvals where it requires approval), false makes it private. A new environment is private unless this is true; dev is always private."},
			"main":              map[string]any{keyType: typBool, keyDesc: "set_environment: true gives this environment the main address, <site>.<pages domain> (owner or administrator)."},
			"main_environment":  str("update: the environment the main address serves; \"\" gives it to none, and it answers not found (owner or administrator)."),
			"email":             str("grant, revoke: the person, by the email they sign in with."),
			"persona":           str("grant, revoke: a persona in place of email; everyone holding it, read on every request. Always level view."),
			"environments":      map[string]any{keyType: typArray, keyItems: map[string]any{keyType: typString}, keyDesc: "grant with level view: the private environments it opens; none is every private environment."},
			"level":             map[string]any{keyType: typString, keyEnum: []string{site.LevelManage, site.LevelView}, keyDesc: "grant: manage publishes, promotes, rolls back, adds domains and shares (internal people only, site-wide); view opens the private environments it names and reads their current files, and changes nothing."},
			"environment":       str("publish: always dev (it may be left out); any other environment is refused, and moves by promote. rollback, set_environment, deployments: the environment. add_domain: the environment the hostname serves, created private when it does not exist; never dev."),
			"requires_approval": map[string]any{keyType: typBool, keyDesc: "set_environment, or add_domain creating an environment: promotions here, and making it public, go through the Approvals queue."},
			"keep_releases": map[string]any{keyType: []string{"integer", typString}, keyDesc: "set_environment, or add_domain creating an environment: " +
				"how many releases it keeps, the last N distinct releases it pointed at (0 keeps every one), or \"default\" for the deployment's default. " +
				"A daily sweep removes every release no environment keeps (one an application release pins or a pending approval names stays), " +
				"and the files only those releases held: a removed release cannot be rolled back to. Only the owner or an administrator changes it."},
			"from":    str("promote: the environment whose release moves."),
			"to":      str("promote: the environment that moves."),
			"release": num("promote: a release number instead of from. rollback: the release to return to (default the one before)."),
			"base":    num("publish: overlay this release instead of replacing the file set. It must be the release dev serves now, so read it first; files you do not send carry over."),
			"note":    str("publish: what the release is for."),
			"reason":  str("promote: why, for the approver."),
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
			keyScope:      str("publish with folder: the library's scope (user, persona, global; default user)."),
			"scope_id":    str("publish with folder: the persona name or user subject."),
			"hostname":    str("add_domain, remove_domain: the hostname."),
			"application": str("add_domain: the application whose endpoints and pages the hostname serves, in place of site and environment."),
			"url":         str("crawl: the site to copy."),
			"form_map": map[string]any{
				keyType: typObject, "additionalProperties": map[string]any{keyType: typString},
				keyDesc: "crawl: for a page path, the platform form endpoint that replaces its forms.",
			},
		},
	}
}

// Register adds manage_site to the server.
func (t *Tool) Register(server *mcp.Server) {
	if t == nil || t.Service == nil || server == nil {
		return
	}
	toolkit.AddTool(server, &mcp.Tool{
		Name: ToolName, Title: "Manage Sites", Description: description, InputSchema: schema(),
		Annotations: toolkit.WriteAnnotations(true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		return t.Handle(ctx, in)
	})
}

func actorOf(ctx context.Context) sitesvc.Actor {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return sitesvc.Actor{}
	}
	verified, stated := pc.UserClaims[browsersession.ClaimEmailVerified].(bool)
	return sitesvc.Actor{Email: pc.UserEmail, Admin: pc.IsAdmin, Persona: pc.PersonaName, EmailUnverified: stated && !verified}
}

func insideRun(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && pc.Source == middleware.SourceScript
}

// Handle runs one command.
func (t *Tool) Handle(ctx context.Context, in Input) (*mcp.CallToolResult, any, error) {
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
	v, err := h(ctx, actorOf(ctx), in)
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
		CmdList: func(ctx context.Context, _ sitesvc.Actor, _ Input) (any, error) {
			list, err := s.Store.List(ctx)
			total := len(list)
			return map[string]any{"sites": list, "total": total}, err //nolint:wrapcheck // the caller's message
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
			return s.Promote(ctx, a, sitesvc.PromoteRequest{Site: in.Site, From: in.From, To: in.To, Release: in.Release, Reason: in.Reason})
		},
		CmdRollback: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Rollback(ctx, a, in.Site, in.Environment, in.Release)
		},
		CmdReleases:    t.releases,
		CmdDeployments: t.deployments,
		CmdAddDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			keep, err := site.ParseKeep(in.KeepReleases)
			if err != nil {
				return nil, &sitesvc.RefusedError{Msg: err.Error()}
			}
			return s.AddDomain(ctx, a, sitesvc.DomainRequest{
				Hostname: in.Hostname, Site: in.Site, Environment: in.Environment, Application: in.Application,
				RequiresApproval: in.RequiresApproval != nil && *in.RequiresApproval, Public: in.Public != nil && *in.Public, Keep: keep.Keep,
			})
		},
		CmdRemoveDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{"removed": in.Hostname}, s.RemoveDomain(ctx, a, in.Hostname)
		},
		CmdDomains: func(ctx context.Context, _ sitesvc.Actor, _ Input) (any, error) {
			list, err := s.Store.Domains(ctx)
			return map[string]any{"domains": list}, err //nolint:wrapcheck // the caller's message
		},
		CmdCrawl: t.crawl,
		CmdGrant: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Grant(ctx, a, in.Site, sitesvc.GrantRequest{Email: in.Email, Persona: in.Persona, Level: in.Level, Environments: in.Environments})
		},
		CmdRevoke: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{"revoked": cmp.Or(in.Email, in.Persona)}, s.Revoke(ctx, a, in.Site, in.Email, in.Persona)
		},
	}
}

// setEnvironment creates or changes an environment and, with main true, gives
// it the main address.
func (t *Tool) setEnvironment(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
	keep, err := site.ParseKeep(in.KeepReleases)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	res, err := t.Service.SetEnvironment(ctx, a, in.Site, in.Environment, sitesvc.EnvironmentChange{
		RequiresApproval: in.RequiresApproval, Public: in.Public, InternalView: in.InternalView, Reason: in.Reason,
		Keep: keep.Keep, KeepDefault: keep.Default,
	})
	if err != nil || in.Main == nil || !*in.Main {
		return res, err //nolint:wrapcheck // the caller's message
	}
	if res.Site, err = t.Service.SetMainEnvironment(ctx, a, in.Site, in.Environment); err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return res, nil
}

// get is a site with where each environment answers and, for someone who
// manages it, who has access.
func (t *Tool) get(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	st, err := t.Service.Find(ctx, in.Site)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	addresses := make(map[string]string, len(st.Environments))
	for _, e := range st.Environments {
		addresses[e.Name] = t.Service.Address(ctx, st, e.Name)
	}
	out := map[string]any{keySite: st, "addresses": addresses, "pages_addresses": t.Service.PagesAddresses(st)}
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
