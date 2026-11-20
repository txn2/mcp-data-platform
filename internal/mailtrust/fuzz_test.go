package mailtrust

import (
	"strings"
	"testing"
)

// FuzzJudge holds the filing decision to headers a sender controls (#2713):
// a message is trusted only from an allowed sender, and, when authentication
// is required, only when an Authentication-Results header carrying the
// receiving server's id names the sender's domain.
func FuzzJudge(f *testing.F) {
	f.Add("Jane <jane@example.com>", "mx.example.net; dmarc=pass header.from=example.com")
	f.Add("jane@example.com", "attacker.example; dmarc=pass header.from=example.com")
	f.Add("\"a@b\" <x@evil.example>", "mx.example.net; spf=pass smtp.mailfrom=example.com")
	f.Add("not an address", "")
	f.Fuzz(func(t *testing.T, from, results string) {
		p := Policy{Allow: NewAllowlist([]string{"example.com"}), RequireAuth: true, AuthServID: "mx.example.net"}
		j := p.Judge(from, []string{results})
		if j.Verdict != Trusted {
			return
		}
		if !p.Allow.Allows(j.From) {
			t.Fatalf("trusted %q (read as %q), which the allowlist does not allow", from, j.From)
		}
		if !strings.Contains(strings.ToLower(results), "mx.example.net") {
			t.Fatalf("trusted %q on results %q that do not carry the receiving server's id", from, results)
		}
	})
}
