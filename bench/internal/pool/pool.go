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

// Size is the number of identities the committed arm configs define, and the
// default for benchrun's -identity-keys. The flag is a claim ABOUT the configs:
// a runner only checks its own flag against what a run needs, so a flag larger
// than the real pool authenticates an attempt as an identity no config defines
// and fails partway through a paid run. TestArmConfigsDefineExactlySize keeps
// the two in step. Sized for the thirty-protocol lifecycle at k=5 (30 x 5 x 2 =
// 300 identities) with headroom; see bench/docs/knowledge-layer-protocol.md.
const Size = 320

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
