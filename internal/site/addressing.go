package site

import "strings"

// Addressing is where a deployment's sites and page origins answer: the pages
// domain, and the scheme and port an address is written with. Every address a
// response names is worked out here, so the portal, manage_site and the
// application views name the same ones (#2607).
type Addressing struct {
	PagesDomain string
	Scheme      string
	Port        string
}

// Origin is the browser origin a hostname answers at.
func (a Addressing) Origin(host string) string {
	out := a.Scheme + "://" + host
	if a.Port != "" {
		out += ":" + a.Port
	}
	return out
}

// OwnAddress is where a site's main address answers, "" with no pages domain
// or when the main address is given to no environment.
func (a Addressing) OwnAddress(st *Site) string {
	if a.PagesDomain == "" || st.MainEnvironment == "" {
		return ""
	}
	return a.Origin(st.Name + "." + a.PagesDomain)
}

// PagesAddress is an environment's address on the pages domain: the main
// address for the environment holding it, otherwise <site>--<env>; "" with no
// pages domain, and "" for a <site>--<env> no certificate can be issued for
// (ReservedLabel, #2611).
func (a Addressing) PagesAddress(st *Site, environment string) string {
	if a.PagesDomain == "" {
		return ""
	}
	if environment == st.MainEnvironment {
		return a.Origin(st.Name + "." + a.PagesDomain)
	}
	label := EnvLabel(st.Name, environment)
	if ReservedLabel(label) {
		return ""
	}
	return a.Origin(label + "." + a.PagesDomain)
}

// PagesAddresses is every environment's address on the pages domain, by name.
func (a Addressing) PagesAddresses(st *Site) map[string]string {
	out := make(map[string]string, len(st.Environments))
	for _, e := range st.Environments {
		out[e.Name] = a.PagesAddress(st, e.Name)
	}
	return out
}

// Address is where people reach an environment: a public domain mapped to it
// first, then any other domain mapped to it, otherwise its address on the
// pages domain, "" for none (#2658). Domains of one kind are taken in hostname
// order, so the answer does not move between reads.
func (a Addressing) Address(st *Site, environment string, domains []Domain) string {
	if d, ok := leadingDomain(environment, domains); ok {
		return a.Origin(d.Hostname)
	}
	return a.PagesAddress(st, environment)
}

// ProductionAddress is the site's address people use: Address for the
// environment holding the main address, and how many other domains that
// environment answers at besides the one named (#2658). "" with no main
// environment.
func (a Addressing) ProductionAddress(st *Site, domains []Domain) (address string, more int) {
	if st.MainEnvironment == "" {
		return "", 0
	}
	n := 0
	for _, d := range domains {
		if d.Environment == st.MainEnvironment {
			n++
		}
	}
	if n > 0 {
		more = n - 1
	}
	return a.Address(st, st.MainEnvironment, domains), more
}

// leadingDomain is the domain an environment is reached at first: its first
// public domain by hostname, else its first domain by hostname.
func leadingDomain(environment string, domains []Domain) (Domain, bool) {
	var best Domain
	found := false
	for _, d := range domains {
		if d.Environment != environment {
			continue
		}
		if !found || (d.Public && !best.Public) || (d.Public == best.Public && d.Hostname < best.Hostname) {
			best, found = d, true
		}
	}
	return best, found
}

// Matches reports whether query names st: a case-insensitive part of its
// name, description, owner or one of its domains' hostnames (#2658). An
// empty query names every site.
func Matches(st *Site, domains []Domain, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, field := range []string{st.Name, st.Description, st.OwnerEmail} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	for _, d := range domains {
		if strings.Contains(strings.ToLower(d.Hostname), q) {
			return true
		}
	}
	return false
}

// Addresses is Address for each environment of st, by name.
func (a Addressing) Addresses(st *Site, domains []Domain) map[string]string {
	out := make(map[string]string, len(st.Environments))
	for _, e := range st.Environments {
		out[e.Name] = a.Address(st, e.Name, domains)
	}
	return out
}

// AddressNote is why a site's environments have no address on the pages
// domain, "" for a site whose addresses answer or a deployment with no pages
// domain (#2611).
func (a Addressing) AddressNote(st *Site) string {
	if a.PagesDomain == "" {
		return ""
	}
	return ReservedLabelNote(st.Name, a.PagesDomain)
}

// PageOrigin is where an application's pages answer, "" with no pages domain.
func (a Addressing) PageOrigin(applicationName string) string {
	if a.PagesDomain == "" {
		return ""
	}
	return a.Origin(applicationName + "." + a.PagesDomain)
}
