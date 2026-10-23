package mailtrust

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllowlist(t *testing.T) {
	a := NewAllowlist([]string{" AP@Vendor.example ", "@partner.example", ""})
	assert.True(t, a.Allows("ap@vendor.example"))
	assert.False(t, a.Allows("sales@vendor.example"), "an address entry admits that address only")
	assert.True(t, a.Allows("Anyone@PARTNER.example"))
	assert.False(t, a.Allows("x@mail.partner.example"), "a domain entry does not admit subdomains")
	assert.False(t, NewAllowlist(nil).Allows("ap@vendor.example"), "an empty list admits no one")
}

const mx = "mx.company.example"

func TestPolicy_Judge(t *testing.T) {
	p := Policy{Allow: NewAllowlist([]string{"@vendor.example"}), RequireAuth: true, AuthServID: mx}
	cases := []struct {
		name    string
		from    string
		results []string
		want    Verdict
		auth    string
	}{
		{
			"dmarc pass", "Vendor AP <ap@vendor.example>",
			[]string{mx + "; dmarc=pass header.from=vendor.example"},
			Trusted, "dmarc=pass",
		},
		{
			"aligned dkim from a subdomain", "ap@vendor.example",
			[]string{mx + "; dkim=pass header.d=mail.vendor.example; spf=fail smtp.mailfrom=x@bulk.example"},
			Trusted, "dkim=pass (mail.vendor.example)",
		},
		{
			"aligned spf", "ap@vendor.example",
			[]string{mx + "; spf=pass smtp.mailfrom=bounce@vendor.example"},
			Trusted, "spf=pass (bounce@vendor.example)",
		},
		{
			"spf pass for another organization does not align", "ap@vendor.example",
			[]string{mx + "; spf=pass smtp.mailfrom=bounce@mailer.example"},
			NotAuthenticated, "none",
		},
		{
			"dmarc fail", "ap@vendor.example",
			[]string{mx + "; dmarc=fail header.from=vendor.example"},
			NotAuthenticated, "dmarc=fail",
		},
		{
			"a pass the sender wrote with another authserv-id is ignored", "ap@vendor.example",
			[]string{"attacker.example; dmarc=pass header.from=vendor.example", mx + "; dmarc=fail header.from=vendor.example"},
			NotAuthenticated, "dmarc=fail",
		},
		{"no results at all", "ap@vendor.example", nil, NotAuthenticated, "none"},
		{
			"sender not on the list", "ceo@company.example",
			[]string{mx + "; dmarc=pass header.from=company.example"},
			NotAllowed, "none",
		},
		{"two from addresses", "a@vendor.example, b@vendor.example", nil, BadFrom, "none"},
		{"no from", "", nil, BadFrom, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := p.Judge(tc.from, tc.results)
			assert.Equal(t, tc.want, j.Verdict)
			assert.Equal(t, tc.auth, j.Auth)
		})
	}
}

func TestPolicy_AuthNotRequired(t *testing.T) {
	p := Policy{Allow: NewAllowlist([]string{"ap@vendor.example"})}
	assert.Equal(t, Trusted, p.Judge("ap@vendor.example", nil).Verdict)
	assert.Equal(t, NotAllowed, p.Judge("x@vendor.example", nil).Verdict)
}

func TestPolicy_NoAuthServIDTrustsNoHeader(t *testing.T) {
	p := Policy{Allow: NewAllowlist([]string{"@vendor.example"}), RequireAuth: true}
	assert.Equal(t, NotAuthenticated, p.Judge("ap@vendor.example", []string{mx + "; dmarc=pass header.from=vendor.example"}).Verdict)
}

func TestSameOrganization(t *testing.T) {
	assert.True(t, sameOrganization("a.vendor.example", "vendor.example"))
	assert.True(t, sameOrganization("x.example.co.uk", "y.example.co.uk"))
	assert.False(t, sameOrganization("example.co.uk", "other.co.uk"))
	assert.False(t, sameOrganization("", "vendor.example"))
}
