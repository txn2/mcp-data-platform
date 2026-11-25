package epaddr

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
)

const (
	base   = "https://platform.example.com"
	origin = "https://shop.pages.example.net"
)

func TestPlatformURL(t *testing.T) {
	b := &epbinding.Binding{Name: "research", Path: "/{route...}"}
	assert.Equal(t, base+"/endpoints/research/{route...}", PlatformURL(base, b, ""), "the last environment")
	assert.Equal(t, base+"/environments/dev/endpoints/research/{route...}", PlatformURL(base, b, "dev"))
}

// TestURLs covers every place an endpoint answers in an environment.
func TestURLs(t *testing.T) {
	hosts := Hosts{
		BaseURL: base, App: "shop",
		PageOrigins: []string{origin, "https://api.example.com"},
		Sites: map[string][]string{
			"prod": {"https://www.example.com", "https://web.pages.example.net"},
			"dev":  {"https://web--dev.pages.example.net"},
		},
	}
	cases := []struct {
		name  string
		b     epbinding.Binding
		env   string
		last  bool
		hosts Hosts
		want  []string
	}{
		{
			name: "the last environment: platform, page origins and the site part of its name",
			b:    epbinding.Binding{Name: "research", Path: "/{id}"}, env: "prod", last: true, hosts: hosts,
			want: []string{
				base + "/endpoints/research/{id}",
				origin + "/endpoints/research/{id}",
				"https://api.example.com/endpoints/research/{id}",
				"https://www.example.com/endpoints/research/{id}",
				"https://web.pages.example.net/endpoints/research/{id}",
			},
		},
		{
			name: "another environment: its platform path and its own site part, no page origin",
			b:    epbinding.Binding{Name: "research"}, env: "dev", last: false, hosts: hosts,
			want: []string{base + "/environments/dev/endpoints/research", "https://web--dev.pages.example.net/endpoints/research"},
		},
		{
			name: "the endpoint named like the application answers at the root of its page origins",
			b:    epbinding.Binding{Name: "shop", Path: "/{route...}"}, env: "prod", last: true,
			hosts: Hosts{BaseURL: base, App: "shop", PageOrigins: []string{origin}},
			want:  []string{base + "/endpoints/shop/{route...}", origin, origin + "/endpoints/shop/{route...}"},
		},
		{
			name: "a root path template that is not a catch-all keeps its path",
			b:    epbinding.Binding{Name: "shop", Path: "/{id}"}, env: "prod", last: true,
			hosts: Hosts{BaseURL: base, App: "shop", PageOrigins: []string{origin}},
			want:  []string{base + "/endpoints/shop/{id}", origin + "/{id}", origin + "/endpoints/shop/{id}"},
		},
		{
			name: "no pages domain: the platform host alone",
			b:    epbinding.Binding{Name: "research"}, env: "prod", last: true, hosts: Hosts{BaseURL: base, App: "shop"},
			want: []string{base + "/endpoints/research"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, URLs(&c.b, c.env, c.last, c.hosts))
		})
	}
}

func TestURLsDropRepeats(t *testing.T) {
	h := Hosts{BaseURL: base, App: "shop", PageOrigins: []string{origin, origin}}
	got := URLs(&epbinding.Binding{Name: "research"}, "prod", true, h)
	assert.Equal(t, []string{base + "/endpoints/research", origin + "/endpoints/research"}, got)
}

func app(envs ...string) *application.Application {
	a := &application.Application{Name: "shop", Parts: []application.Part{
		{Kind: application.KindScript, ID: "s1"},
		{Kind: application.KindAsset, ID: "a1"},
		{Kind: application.KindScript, ID: "broken"},
	}}
	for _, e := range envs {
		a.Environments = append(a.Environments, application.Environment{Name: e, Release: 1})
	}
	return a
}

// TestForApplication lists each environment's endpoints with the URLs it
// answers at, every environment present, a script whose bindings cannot be
// read left out.
func TestForApplication(t *testing.T) {
	src := Source{
		ByScript: func(_ context.Context, id string) ([]epbinding.Binding, error) {
			if id == "broken" {
				return nil, errors.New("down")
			}
			return []epbinding.Binding{{Name: "research", Methods: []string{"GET"}, AuthMode: "public", Path: "/{route...}"}}, nil
		},
		ScriptName: func(_ context.Context, id string) string { return map[string]string{"s1": "research-api"}[id] },
		Hosts:      func(context.Context, *application.Application) Hosts { return Hosts{BaseURL: base} },
	}
	got := ForApplication(context.Background(), src, app("dev", "prod"))
	assert.Len(t, got, 2)
	assert.Equal(t, []Endpoint{{
		Name: "research", Script: "research-api", Methods: []string{"GET"}, AuthMode: "public", Path: "/{route...}",
		URLs: []string{base + "/environments/dev/endpoints/research/{route...}"},
	}}, got["dev"])
	assert.Equal(t, []string{base + "/endpoints/research/{route...}"}, got["prod"][0].URLs)

	// dev before its first release answers no endpoint, so it lists none.
	unreleased := app("dev", "prod")
	unreleased.Environments[0].Release = 0
	assert.Equal(t, []Endpoint{}, ForApplication(context.Background(), src, unreleased)["dev"])

	noName := src
	noName.ScriptName = func(context.Context, string) string { return "" }
	assert.Equal(t, "s1", ForApplication(context.Background(), noName, app("prod"))["prod"][0].Script, "the script's id when its name is unknown")
}

func TestForApplicationWithNothingToList(t *testing.T) {
	assert.Equal(t, map[string][]Endpoint{}, ForApplication(context.Background(), Source{}, app()))
	got := ForApplication(context.Background(), Source{}, app("dev", "prod"))
	assert.Equal(t, map[string][]Endpoint{"dev": {}, "prod": {}}, got, "every environment present, with none")
	src := Source{ByScript: func(context.Context, string) ([]epbinding.Binding, error) { return nil, nil }}
	assert.Equal(t, []Endpoint{}, ForApplication(context.Background(), src, app("prod"))["prod"])
}
