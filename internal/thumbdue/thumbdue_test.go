package thumbdue

import (
	"testing"

	"github.com/lib/pq"
)

// A rule's fingerprint is the same for the same SQL and values, however the
// values are passed, and differs when either changes.
func TestRule(t *testing.T) {
	a := Rule("x = ANY($1) AND y < $2", pq.Array([]string{"a", "b"}), 3)
	if b := Rule("x = ANY($1) AND y < $2", pq.Array([]string{"a", "b"}), 3); a != b {
		t.Error("the same rule fingerprints differently on a second call")
	}
	for _, other := range []string{
		Rule("x = ANY($1) AND y < $2", pq.Array([]string{"a", "c"}), 3),
		Rule("x = ANY($1) AND y < $2", pq.Array([]string{"a", "b"}), 4),
		Rule("x = ANY($1) AND y <= $2", pq.Array([]string{"a", "b"}), 3),
	} {
		if other == a {
			t.Error("a changed rule fingerprints the same")
		}
	}
}

func TestLockKeys(t *testing.T) {
	seen := map[int64]bool{}
	for _, table := range []Table{Resources, Assets, Collections} {
		k, err := table.lockKey()
		if err != nil || seen[k] {
			t.Errorf("%s: key %d, %v", table, k, err)
		}
		seen[k] = true
		if markQuery(table) == "" {
			t.Errorf("%s has no mark statement", table)
		}
	}
	if _, err := Table("users").lockKey(); err == nil {
		t.Error("a table the renderer does not draw from has a sweep lock")
	}
}
