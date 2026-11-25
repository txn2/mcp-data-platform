// Package epaddr is where an endpoint answers (#2822): the one computation of
// an endpoint's URLs, which manage_script handler_list's url and an
// application's environment cards both read, so the two cannot disagree.
//
// An application endpoint answers in every environment of its application:
// on the platform's own host at /endpoints/<name> for the last environment and
// at /environments/<env>/endpoints/<name> for any other (#2696); on the
// application's page origin and the domains mapped to the application, for the
// last environment, at /endpoints/<name> and, for the endpoint named like the
// application, at the root (#2559); and on the address of each site
// environment that is a part of the application at /endpoints/<name>, running
// the application environment of the site environment's name (#2679, #2709).
package epaddr

import (
	"context"
	"regexp"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
)

// Endpoint is one endpoint an application environment serves, and every URL
// it answers at there.
type Endpoint struct {
	Name     string   `json:"name" example:"research"`
	Script   string   `json:"script" example:"research-api"`
	Methods  []string `json:"methods" example:"GET"`
	AuthMode string   `json:"auth_mode" example:"public"`
	Path     string   `json:"path" example:"/{route...}"`
	URLs     []string `json:"urls"`
}

// Hosts is every origin an application's endpoints answer on.
type Hosts struct {
	// BaseURL is the platform's public address, "" for none.
	BaseURL string
	// App is the application's name: the endpoint of that name answers at
	// the root of its page origins.
	App string
	// PageOrigins are the application's page origin, when the deployment has
	// a pages domain, and the origins of the domains mapped to it. They run
	// the last environment.
	PageOrigins []string
	// Sites are the origins of the application's site environment parts, by
	// the site environment's name, which is the application environment they
	// run.
	Sites map[string][]string
}

// PlatformURL is where a binding answers on the platform's own host in an
// application environment: env "" is the last, served at /endpoints/<name>.
// manage_script handler_list reports this as url.
func PlatformURL(baseURL string, b *epbinding.Binding, env string) string {
	if env == "" {
		return baseURL + b.URLPath()
	}
	return baseURL + epbinding.EnvironmentPrefix + env + b.URLPath()
}

// catchAll is a path template matching every path, which at an origin's root
// is the origin itself.
var catchAll = regexp.MustCompile(`^/\{[a-z][a-z0-9_]*\.{3}\}$`)

// rootURL is where the endpoint named like the application answers at the
// root of origin.
func rootURL(origin, path string) string {
	if path == "" || catchAll.MatchString(path) {
		return origin
	}
	return origin + path
}

// URLs is every URL a binding answers at in environment env; last says it is
// the application's last environment.
func URLs(b *epbinding.Binding, env string, last bool, h Hosts) []string {
	platformEnv := env
	if last {
		platformEnv = ""
	}
	out := []string{PlatformURL(h.BaseURL, b, platformEnv)}
	if last {
		for _, origin := range h.PageOrigins {
			if b.Name == h.App {
				out = append(out, rootURL(origin, b.Path))
			}
			out = append(out, origin+b.URLPath())
		}
	}
	for _, origin := range h.Sites[env] {
		out = append(out, origin+b.URLPath())
	}
	return dedupe(out)
}

// dedupe drops repeats, keeping the first of each.
func dedupe(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// Source is what the endpoints of an application are read from.
type Source struct {
	// ByScript is a script's bindings.
	ByScript func(ctx context.Context, scriptID string) ([]epbinding.Binding, error)
	// ScriptName is a script's name, "" when unknown.
	ScriptName func(ctx context.Context, scriptID string) string
	// Hosts is the origins the application answers on.
	Hosts func(ctx context.Context, a *application.Application) Hosts
}

// ForApplication is, by environment name, the endpoints each environment of
// a serves and the URLs each answers at there; every environment is present,
// with an empty list when the application holds no endpoint. A script whose
// bindings cannot be read is left out.
func ForApplication(ctx context.Context, s Source, a *application.Application) map[string][]Endpoint {
	out := make(map[string][]Endpoint, len(a.Environments))
	for _, env := range a.Environments {
		out[env.Name] = []Endpoint{}
	}
	if len(a.Environments) == 0 || s.ByScript == nil {
		return out
	}
	var hosts Hosts
	if s.Hosts != nil {
		hosts = s.Hosts(ctx, a)
	}
	hosts.App = a.Name
	last := a.Last().Name
	for _, p := range a.Parts {
		if p.Kind != application.KindScript {
			continue
		}
		bindings, err := s.ByScript(ctx, p.ID)
		if err != nil {
			continue
		}
		script := s.scriptName(ctx, p.ID)
		for i := range bindings {
			listed := listing{binding: &bindings[i], script: script, last: last, hosts: hosts}
			listed.add(out, a.Environments)
		}
	}
	return out
}

// listing is one binding as the environments list it.
type listing struct {
	binding *epbinding.Binding
	script  string
	last    string
	hosts   Hosts
}

// add lists the binding in every environment that answers it: the last, and
// each other that runs a release.
func (l listing) add(out map[string][]Endpoint, envs []application.Environment) {
	b, last, script, hosts := l.binding, l.last, l.script, l.hosts
	for _, env := range envs {
		if env.Name != last && env.Release == 0 {
			continue
		}
		out[env.Name] = append(out[env.Name], Endpoint{
			Name: b.Name, Script: script, Methods: b.Methods, AuthMode: b.AuthMode, Path: b.Path,
			URLs: URLs(b, env.Name, env.Name == last, hosts),
		})
	}
}

// scriptName is a script's name, its id when the name is unknown.
func (s Source) scriptName(ctx context.Context, id string) string {
	if s.ScriptName != nil {
		if name := s.ScriptName(ctx, id); name != "" {
			return name
		}
	}
	return id
}
