// Package sitetool is the manage_site MCP tool (#2518): a site's record, its
// environments, publishing a release, promoting and rolling back, mapping
// hostnames and the migration crawl, over the one service.
package sitetool

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
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
	CmdVerifyDomain = "verify_domain"
	CmdRemoveDomain = "remove_domain"
	CmdDomains      = "domains"
	CmdHosts        = "hosts"
	CmdCrawl        = "crawl"
)

// Commands is every command, in the order the schema lists them.
var Commands = []string{
	CmdCreate, CmdGet, CmdList, CmdUpdate, CmdDelete, CmdEnvironment, CmdPublish, CmdPromote, CmdRollback,
	CmdReleases, CmdDeployments, CmdAddDomain, CmdVerifyDomain, CmdRemoveDomain, CmdDomains, CmdHosts, CmdCrawl,
}

// Reads is the commands that change nothing.
var Reads = []string{CmdGet, CmdList, CmdReleases, CmdDeployments, CmdDomains, CmdHosts}

// allowedInRun is what a script run may do beyond reading: a build publishes
// to an environment that needs no approval. Anything that changes what a
// public address serves is a person's.
var allowedInRun = []string{CmdPublish}

// File is one file handed to publish.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Encoding is utf-8 (default) or base64.
	Encoding string `json:"encoding,omitempty"`
}

// Input is the tool's arguments.
type Input struct {
	Command     string `json:"command"`
	Site        string `json:"site,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	OwnerEmail  string `json:"owner_email,omitempty"`
	Environment string `json:"environment,omitempty"`
	// RequiresApproval is a pointer so false can be told from unset.
	RequiresApproval *bool  `json:"requires_approval,omitempty"`
	From             string `json:"from,omitempty"`
	To               string `json:"to,omitempty"`
	Release          int    `json:"release,omitempty"`
	Note             string `json:"note,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Files            []File `json:"files,omitempty"`
	// Archive is a zip or gzipped tar managed resource, by id or URI.
	Archive string `json:"archive,omitempty"`
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

const description = "Static sites served by the platform on their own hostnames. A site has environments (dev, stage and " +
	"prod to start, any name), each pointing at one immutable release. publish writes a new release (from files, an archive " +
	"managed resource, or a resource folder; a _redirects file becomes its redirect map, a 404.html its not-found page) and " +
	"answers with a preview URL. promote points an environment at another's release, and rollback at one it served before: " +
	"neither copies a file, and both take effect at once. An environment that requires approval (prod) is reached only by " +
	"promote: an administrator's promote records its own approval, anyone else's files a request in manage_approval and goes " +
	"through when they promote again after it is approved. add_domain maps a hostname to a site environment or to an " +
	"application's endpoints; verify_domain routes it once a DNS TXT record or a served token proves control, and hosts " +
	"reports what the ingress needs (with a cert-manager Certificate). crawl copies an existing site into a release, keeping " +
	"its URLs and turning its redirects into the redirect map. A site environment is a part of an application (add_part kind " +
	"site, part_id \"<site>/<environment>\"), released and rolled back with it. Everyone reads a site; its owner and " +
	"administrators publish and promote; administrators map hostnames and delete. Read fetch " +
	"mcp:knowledge_page:platform-pages-and-sites first."

const (
	keyType   = "type"
	keyDesc   = "description"
	typString = "string"
	typObject = "object"
	keySite   = "site"
)

func schema() map[string]any {
	str := func(desc string) map[string]any { return map[string]any{keyType: typString, keyDesc: desc} }
	num := func(desc string) map[string]any { return map[string]any{keyType: "integer", keyDesc: desc} }
	return map[string]any{
		keyType: typObject, "required": []string{"command"},
		"properties": map[string]any{
			"command":           map[string]any{keyType: typString, "enum": Commands, keyDesc: "The operation."},
			keySite:             str("The site, by name or id (every command but create, list, domains, hosts)."),
			"name":              str("create: the site's name, a lowercase slug."),
			keyDesc:             str("create, update: what the site is."),
			"owner_email":       str("update: move the site to another owner."),
			"environment":       str("publish: where it lands (default dev). rollback, set_environment, add_domain, deployments: the environment."),
			"requires_approval": map[string]any{keyType: "boolean", keyDesc: "set_environment: promotions here go through the Approvals queue."},
			"from":              str("promote: the environment whose release moves."),
			"to":                str("promote: the environment that moves."),
			"release":           num("promote: a release number instead of from. rollback: the release to return to (default the one before)."),
			"note":              str("publish: what the release is for."),
			"reason":            str("promote: why, for the approver."),
			"files": map[string]any{
				keyType: "array", keyDesc: "publish: the files, up to the inline size a tool call carries.",
				"items": map[string]any{keyType: typObject, "required": []string{"path", "content"}, "properties": map[string]any{
					"path": str("The file's path in the site."), "content": str("Its content."),
					"encoding": map[string]any{keyType: typString, "enum": []string{"utf-8", "base64"}, keyDesc: "How content is written (default utf-8)."},
				}},
			},
			"archive":     str("publish: a zip or gzipped tar managed resource, by id or mcp:// URI."),
			"folder":      str("publish: a managed-resource folder path."),
			"scope":       str("publish with folder: the library's scope (user, persona, global; default user)."),
			"scope_id":    str("publish with folder: the persona name or user subject."),
			"hostname":    str("add_domain, verify_domain, remove_domain: the hostname."),
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
	return sitesvc.Actor{Email: pc.UserEmail, Admin: pc.IsAdmin, Persona: pc.PersonaName}
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
	v, err := h(ctx, actorOf(ctx), in)
	if err != nil {
		return errorResult(message(err))
	}
	return jsonResult(v)
}

type handler func(ctx context.Context, actor sitesvc.Actor, in Input) (any, error)

func (t *Tool) handlers() map[string]handler {
	s := t.Service
	return map[string]handler{
		CmdCreate: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Create(ctx, a, in.Name, in.Description)
		},
		CmdGet: func(ctx context.Context, _ sitesvc.Actor, in Input) (any, error) { return t.get(ctx, in.Site) },
		CmdList: func(ctx context.Context, _ sitesvc.Actor, _ Input) (any, error) {
			list, err := s.Store.List(ctx)
			total := len(list)
			return map[string]any{"sites": list, "total": total}, err //nolint:wrapcheck // the caller's message
		},
		CmdUpdate: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Update(ctx, a, in.Site, in.Description, in.OwnerEmail)
		},
		CmdDelete: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{"deleted": in.Site}, s.Delete(ctx, a, in.Site)
		},
		CmdEnvironment: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.SetEnvironment(ctx, a, in.Site, in.Environment, in.RequiresApproval != nil && *in.RequiresApproval)
		},
		CmdPublish: t.publish,
		CmdPromote: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Promote(ctx, a, sitesvc.PromoteRequest{Site: in.Site, From: in.From, To: in.To, Release: in.Release, Reason: in.Reason})
		},
		CmdRollback: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.Rollback(ctx, a, in.Site, in.Environment, in.Release)
		},
		CmdReleases:    t.releases,
		CmdDeployments: t.deployments,
		CmdAddDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.AddDomain(ctx, a, sitesvc.DomainRequest{Hostname: in.Hostname, Site: in.Site, Environment: in.Environment, Application: in.Application})
		},
		CmdVerifyDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return s.VerifyDomain(ctx, a, in.Hostname)
		},
		CmdRemoveDomain: func(ctx context.Context, a sitesvc.Actor, in Input) (any, error) {
			return map[string]any{"removed": in.Hostname}, s.RemoveDomain(ctx, a, in.Hostname)
		},
		CmdDomains: func(ctx context.Context, _ sitesvc.Actor, _ Input) (any, error) {
			list, err := s.Store.Domains(ctx)
			return map[string]any{"domains": list}, err //nolint:wrapcheck // the caller's message
		},
		CmdHosts: func(ctx context.Context, _ sitesvc.Actor, _ Input) (any, error) { return s.Hosts(ctx) },
		CmdCrawl: t.crawl,
	}
}

// get is a site with where each environment answers.
func (t *Tool) get(ctx context.Context, ref string) (any, error) {
	st, err := t.Service.Find(ctx, ref)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	previews := make(map[string]string, len(st.Environments))
	for _, e := range st.Environments {
		previews[e.Name] = t.Service.PreviewURL(st.Name, e.Name)
	}
	return map[string]any{keySite: st, "preview_urls": previews}, nil
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

// publish reads the one source the call names and publishes it.
func (t *Tool) publish(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	files, source, err := t.filesOf(ctx, in)
	if err != nil {
		return nil, err
	}
	return t.Service.Publish(ctx, actor, sitesvc.PublishRequest{ //nolint:wrapcheck // the caller's message
		Site: in.Site, Environment: in.Environment, Source: source, Note: in.Note, Files: files,
	})
}

func (t *Tool) filesOf(ctx context.Context, in Input) ([]sitesvc.InputFile, string, error) {
	named := 0
	for _, set := range []bool{len(in.Files) > 0, in.Archive != "", in.Folder != ""} {
		if set {
			named++
		}
	}
	if named != 1 {
		return nil, "", &sitesvc.RefusedError{Msg: "publish names one source: files, archive or folder"}
	}
	switch {
	case in.Archive != "":
		if t.Sources == nil {
			return nil, "", &sitesvc.RefusedError{Msg: "this deployment has no managed resources to publish from"}
		}
		files, err := t.Sources.Archive(ctx, in.Archive)
		return files, in.Archive, err //nolint:wrapcheck // the caller's message
	case in.Folder != "":
		if t.Sources == nil {
			return nil, "", &sitesvc.RefusedError{Msg: "this deployment has no managed resources to publish from"}
		}
		files, err := t.Sources.Folder(ctx, in.Scope, in.ScopeID, in.Folder)
		return files, in.Folder, err //nolint:wrapcheck // the caller's message
	}
	files, err := decodeFiles(in.Files)
	return files, "files (" + strconv.Itoa(len(in.Files)) + ")", err
}

func decodeFiles(in []File) ([]sitesvc.InputFile, error) {
	out := make([]sitesvc.InputFile, 0, len(in))
	for _, f := range in {
		body := []byte(f.Content)
		if f.Encoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(f.Content)
			if err != nil {
				return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("file %q: content is not base64", f.Path)}
			}
			body = decoded
		}
		out = append(out, sitesvc.InputFile{Path: f.Path, Body: body})
	}
	return out, nil
}

// crawl copies a site and publishes it as a release.
func (t *Tool) crawl(ctx context.Context, actor sitesvc.Actor, in Input) (any, error) {
	if t.Crawler == nil {
		return nil, &sitesvc.RefusedError{Msg: "this deployment cannot crawl a site"}
	}
	got, err := t.Crawler.Crawl(ctx, sitecrawl.Request{Start: in.URL, FormMap: in.FormMap, PortalURL: t.PortalURL})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	pub, err := t.Service.Publish(ctx, actor, sitesvc.PublishRequest{
		Site: in.Site, Environment: in.Environment, Source: "crawl " + in.URL, Note: in.Note, Files: got.Files,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	skipped := got.Skipped
	if skipped == nil {
		skipped = []string{}
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
