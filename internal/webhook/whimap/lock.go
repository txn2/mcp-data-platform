package whimap

import (
	"context"
	"hash/fnv"
)

// Lock takes the one-replica lock on a source's poll, reporting whether it
// was free.
type Lock func(ctx context.Context, source string) (release func(), ok bool, err error)

// LockKeyspace is the advisory-lock space an imap source's poll is held in;
// the key within it is a hash of the source's name.
const LockKeyspace int32 = 4713211

// LockKey is the key a source's poll is locked under.
func LockKey(source string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	return int32(h.Sum32() & 0x7fffffff) // #nosec G115 -- masked to 31 bits
}
