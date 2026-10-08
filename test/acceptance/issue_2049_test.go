//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// #2049: a managed script hashes with the hash module, through manage_script
// run_draft on the running platform, including a 1 MB value in one call.
//
// Wire forms: manage_script's command, name and source are strings, each
// sent once in the one form its schema admits.

const hash2049 = `
def main():
    """Prints digests, an HMAC and the digest of a 1 MB string."""
    print(hash.md5(""))
    print(hash.sha256("abc"))
    print(hash.hmac_sha256("Jefe", "what do ya want for nothing?"))
    page = "x" * (1024 * 1024)
    print(len(hash.md5(page)))
    print(hash("a"))
`

func TestIssue2049_HashModuleDigestsInAScriptAndAMegabyteInOneCall(t *testing.T) {
	c := connect(t)
	log := draftLog2048(t, c, hash2049)
	want := strings.Join([]string{
		"d41d8cd98f00b204e9800998ecf8427e",
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
		"32",
		"97",
	}, "\n") + "\n"
	if log != want {
		t.Errorf("the draft printed %q, want %q", log, want)
	}
}
