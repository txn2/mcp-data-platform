// Package pool centralizes the identity-pool naming the benchmark shares
// between the S1-S3 pipeline and the S5 lifecycle runner. Both rotate a base
// admin credential into per-attempt pool keys and derive each pool identity's
// email from its sequence number; keeping that derivation in one place stops
// the two runners from drifting (a credential the client authenticates with
// that no longer matches the identity the harness expects would silently break
// audit correlation and capture attribution).
package pool

import "fmt"

// NamePrefix is the identity-pool key NAME prefix in the arm configs
// (bench-agent-001..NNN). An API key authenticates as email name@apikey.local,
// so a pool identity's email is derivable from its sequence number. The
// credential rotation couples to the same convention.
const NamePrefix = "bench-agent"

// Credential returns the bearer token for one attempt: the base credential when
// identity rotation is off (identityKeys == 0), or the zero-padded pool key
// "<base>-NNN" matching the arm configs' pool.
func Credential(base string, seq, identityKeys int) string {
	if identityKeys > 0 {
		return fmt.Sprintf("%s-%03d", base, seq)
	}
	return base
}

// Email returns the captured_by email for a pool identity sequence number
// (name@apikey.local, per pkg/auth's API-key default).
func Email(seq int) string {
	return fmt.Sprintf("%s-%03d@apikey.local", NamePrefix, seq)
}
