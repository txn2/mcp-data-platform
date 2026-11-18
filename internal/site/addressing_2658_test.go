package site

import "testing"

// TestProductionAddress: the environment holding the main address is reached
// at its public domain first, then another domain, then its pages address;
// the rest of its domains are counted (#2658).
func TestProductionAddress(t *testing.T) {
	at := Addressing{PagesDomain: "pages.example.net", Scheme: "https"}
	st := &Site{Name: "docs", MainEnvironment: "prod", Environments: []Environment{{Name: "dev"}, {Name: "prod"}}}
	cases := []struct {
		label   string
		domains []Domain
		want    string
		more    int
	}{
		{"none", nil, "https://docs.pages.example.net", 0},
		{"only private", []Domain{{Hostname: "a.example.test", Environment: "prod"}}, "https://a.example.test", 0},
		{"public first", []Domain{
			{Hostname: "a.example.test", Environment: "prod"},
			{Hostname: "www.example.test", Environment: "prod", Public: true},
			{Hostname: "dev.example.test", Environment: "dev", Public: true},
		}, "https://www.example.test", 1},
		{"hostname order", []Domain{
			{Hostname: "z.example.test", Environment: "prod", Public: true},
			{Hostname: "b.example.test", Environment: "prod", Public: true},
		}, "https://b.example.test", 1}, // #2687: the primary is the address, and a redirect domain never is.
		{"primary first", []Domain{
			{Hostname: "a.example.test", Environment: "prod", Public: true},
			{Hostname: "www.example.test", Environment: "prod", Primary: true},
		}, "https://www.example.test", 1},
		{"never a redirect", []Domain{
			{Hostname: "a.example.test", Environment: "prod", Public: true, Mode: DomainRedirect},
			{Hostname: "b.example.test", Environment: "prod"},
		}, "https://b.example.test", 1},
		{"only a redirect", []Domain{
			{Hostname: "a.example.test", Environment: "prod", Public: true, Mode: DomainRedirect},
		}, "https://docs.pages.example.net", 0},
	}
	for _, c := range cases {
		got, more := at.ProductionAddress(st, c.domains)
		if got != c.want || more != c.more {
			t.Errorf("%s: ProductionAddress = %q, %d; want %q, %d", c.label, got, more, c.want, c.more)
		}
	}
	if got, _ := at.ProductionAddress(&Site{Name: "x"}, nil); got != "" {
		t.Errorf("a site whose main address serves no environment has none: %q", got)
	}
}

// TestMatches: a query names a site by part of its name, description, owner
// or any domain, whatever the case (#2658).
func TestMatches(t *testing.T) {
	st := &Site{Name: "plexus", Description: "Product docs", OwnerEmail: "jane@example.com"}
	domains := []Domain{{Hostname: "www.example.io"}}
	for q, want := range map[string]bool{
		"": true, "PLEX": true, "docs": true, "jane@": true, "example.io": true, "nothing": false,
	} {
		if got := Matches(st, domains, q); got != want {
			t.Errorf("Matches(%q) = %v; want %v", q, got, want)
		}
	}
}
