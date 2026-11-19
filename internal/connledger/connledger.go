// Package connledger records the configuration each of a toolkit's run-time
// connections was added with, so a reconcile can tell an announcement of the
// configuration a replica already serves from a change (#2704).
package connledger

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
)

// Ledger records the configuration each of a toolkit's run-time
// connections was added with, as a digest of its canonical JSON: the toolkit
// records after a successful add and forgets on removal, and answers
// toolkit.ConfigHolder from it. A toolkit that fills secret references when it adds a
// connection records and asks with the filled configuration, so a rotated
// secret is a changed configuration. The digest is all that is kept, so no
// secret value is held here. The zero value is ready to use.
type Ledger struct {
	mu      sync.Mutex
	digests map[string][sha256.Size]byte
}

// Record notes config as the configuration name is served under. A
// configuration that cannot be encoded is forgotten instead, so it never
// matches.
func (l *Ledger) Record(name string, config map[string]any) {
	d, ok := configDigest(config)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !ok {
		delete(l.digests, name)
		return
	}
	if l.digests == nil {
		l.digests = map[string][sha256.Size]byte{}
	}
	l.digests[name] = d
}

// Forget drops what is recorded for name.
func (l *Ledger) Forget(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.digests, name)
}

// Holds reports whether name is recorded under exactly config.
func (l *Ledger) Holds(name string, config map[string]any) bool {
	d, ok := configDigest(config)
	if !ok {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	held, found := l.digests[name]
	return found && held == d
}

// configDigest is the digest of config's canonical JSON (encoding/json writes
// map keys sorted, so equal configurations encode alike).
func configDigest(config map[string]any) ([sha256.Size]byte, bool) {
	b, err := json.Marshal(config)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(b), true
}
