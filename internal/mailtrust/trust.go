// Package mailtrust decides which messages of the platform's mailbox a tool
// may show (#2247). A message is shown only when its From address is on the
// connection's allowlist and, unless the operator turned it off, the
// receiving server recorded that the sender's domain authenticated it: an
// SPF or DKIM pass aligned with From, or DMARC. Anyone can send to the
// address and write any From header, so these are rules the platform
// enforces, not judgments left to a model.
package mailtrust

import (
	"net/mail"
	"strings"

	"github.com/emersion/go-msgauth/authres"
	"golang.org/x/net/publicsuffix"
)

// at separates an address's mailbox from its domain, and starts a domain
// entry on the allowlist.
const at = "@"

// authNone is what a message's authentication reads as when the receiving
// server recorded nothing usable.
const authNone = "none"

// Allowlist is the senders whose mail is visible: whole addresses
// ("ap@vendor.example") and domains ("@vendor.example"). A domain entry matches
// that domain only, not its subdomains; name a subdomain to admit it.
type Allowlist struct {
	addresses map[string]bool
	domains   map[string]bool
}

// NewAllowlist reads the connection's allow_senders entries. Entries are
// case-insensitive; blank entries are ignored.
func NewAllowlist(entries []string) Allowlist {
	a := Allowlist{addresses: map[string]bool{}, domains: map[string]bool{}}
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case e == "":
		case strings.HasPrefix(e, at):
			a.domains[strings.TrimPrefix(e, at)] = true
		default:
			a.addresses[e] = true
		}
	}
	return a
}

// Allows reports whether addr is on the list.
func (a Allowlist) Allows(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if a.addresses[addr] {
		return true
	}
	return a.domains[domainOf(addr)]
}

func domainOf(addr string) string {
	if _, domain, ok := strings.CutLast(addr, at); ok {
		return strings.ToLower(domain)
	}
	return ""
}

// Verdict is why a message is or is not shown.
type Verdict string

// The verdicts a message can get.
const (
	// Trusted: the sender is on the allowlist and, when required, the
	// receiving server authenticated their domain.
	Trusted Verdict = "trusted"
	// NotAllowed: the From address is not on the allowlist.
	NotAllowed Verdict = "sender_not_allowed"
	// NotAuthenticated: on the allowlist, but no aligned pass was recorded
	// by the receiving server, so the From address may be forged.
	NotAuthenticated Verdict = "sender_not_authenticated"
	// BadFrom: the From header is missing, does not parse, or names more
	// than one address.
	BadFrom Verdict = "from_unreadable"
)

// Policy decides which messages a connection shows.
type Policy struct {
	Allow Allowlist
	// RequireAuth requires an aligned SPF, DKIM or DMARC pass.
	RequireAuth bool
	// AuthServID is the receiving server's authserv-id: only an
	// Authentication-Results header carrying it is read. A sender can add
	// an Authentication-Results header of their own; they cannot make it
	// carry the receiving server's id past a server that strips forged ones
	// (RFC 8601 section 5), which is what the id is for.
	AuthServID string
}

// Judgment is the policy's reading of one message.
type Judgment struct {
	Verdict Verdict
	// From is the sender's address as the From header names it.
	From string
	// Auth is what the receiving server recorded, as "dmarc=pass",
	// "dkim=pass (example.com)" or "none".
	Auth string
}

// Judge reads one message's From and Authentication-Results headers. from is
// the raw From header; authResults are the Authentication-Results header
// values in the order they appear, topmost first.
func (p Policy) Judge(from string, authResults []string) Judgment {
	addrs, err := mail.ParseAddressList(from)
	if err != nil || len(addrs) != 1 {
		return Judgment{Verdict: BadFrom, Auth: authNone}
	}
	addr := strings.ToLower(addrs[0].Address)
	j := Judgment{From: addr, Auth: authNone}
	if !p.Allow.Allows(addr) {
		j.Verdict = NotAllowed
		return j
	}
	pass, how := p.aligned(domainOf(addr), authResults)
	j.Auth = how
	if p.RequireAuth && !pass {
		j.Verdict = NotAuthenticated
		return j
	}
	j.Verdict = Trusted
	return j
}

// aligned reports whether the receiving server's results show an
// authentication of the From domain: DMARC pass for it, or an SPF or DKIM pass
// for a domain in the same organization (relaxed alignment, RFC 7489 3.1).
func (p Policy) aligned(fromDomain string, authResults []string) (pass bool, how string) {
	header, ok := p.receivingHeader(authResults)
	if !ok {
		return false, authNone
	}
	_, results, err := authres.Parse(header)
	if err != nil {
		return false, "unreadable"
	}
	how = authNone
	for _, r := range results {
		pass, said := passFor(r, fromDomain)
		if pass {
			return true, said
		}
		if said != "" {
			how = said
		}
	}
	return false, how
}

// passFor reads one result: whether it authenticates fromDomain, and how to
// say what it recorded (empty for a result not worth reporting on a miss).
func passFor(r authres.Result, fromDomain string) (pass bool, how string) {
	switch r := r.(type) {
	case *authres.DMARCResult:
		return dmarcPass(r, fromDomain)
	case *authres.DKIMResult:
		if r.Value == authres.ResultPass && sameOrganization(r.Domain, fromDomain) {
			return true, "dkim=pass (" + r.Domain + ")"
		}
	case *authres.SPFResult:
		if r.Value == authres.ResultPass && sameOrganization(domainOf(r.From)+orBare(r.From), fromDomain) {
			return true, "spf=pass (" + r.From + ")"
		}
	}
	return false, ""
}

// dmarcPass reads a DMARC result, which names the From domain it judged.
func dmarcPass(r *authres.DMARCResult, fromDomain string) (pass bool, how string) {
	if r.Value == authres.ResultPass && (r.From == "" || strings.EqualFold(r.From, fromDomain)) {
		return true, "dmarc=pass"
	}
	return false, "dmarc=" + string(r.Value)
}

// orBare covers smtp.mailfrom written as a bare domain rather than an address.
func orBare(v string) string {
	if strings.Contains(v, at) {
		return ""
	}
	return v
}

// receivingHeader is the topmost Authentication-Results header carrying the
// receiving server's authserv-id.
func (p Policy) receivingHeader(authResults []string) (string, bool) {
	for _, h := range authResults {
		id, _, err := authres.Parse(h)
		if err != nil {
			continue
		}
		if strings.EqualFold(firstToken(id), p.AuthServID) && p.AuthServID != "" {
			return h, true
		}
	}
	return "", false
}

func firstToken(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// sameOrganization reports relaxed alignment: both domains share one
// registrable domain under the public suffix list.
func sameOrganization(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSuffix(a, ".")), strings.ToLower(strings.TrimSuffix(b, "."))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	oa, errA := publicsuffix.EffectiveTLDPlusOne(a)
	ob, errB := publicsuffix.EffectiveTLDPlusOne(b)
	return errA == nil && errB == nil && oa == ob
}
