package site

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

// Address is where people reach an environment: the first of its domains,
// otherwise its address on the pages domain, "" for none.
func (a Addressing) Address(st *Site, environment string, domains []Domain) string {
	for _, d := range domains {
		if d.Environment == environment {
			return a.Origin(d.Hostname)
		}
	}
	return a.PagesAddress(st, environment)
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
