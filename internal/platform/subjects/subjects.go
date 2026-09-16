// Package subjects records the subject each address authenticates as, and folds
// the address-keyed managed-resource library into the subject-keyed one once
// the pair is known (#1677).
//
// A user library is keyed by subject: a session files there by its own subject,
// and so does the portal's upload. A managed-script run acts for its version's
// author and, knowing that person only by address, filed the same path in a
// second library keyed by the address. This is how the run learns the subject
// its author's own session uses: the pair is recorded whenever a person
// authenticates, at the same chokepoint the known-users directory observes,
// and the run reads it when it opens its session.
//
// Learning the pair is also the moment the two libraries are put back together.
// Everything filed under the address is refiled under the subject, keeping its
// id and aliasing the address it vacates, so a scheduled script keeps its
// rolling file and every reference to it keeps resolving. The fold runs when a
// person authenticates, so the files a run wrote before this existed are where
// their session looks, and before a run executes, so the run's next write
// versions the file rather than starting a second one.
package subjects

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// DefaultObserveTTL is the minimum interval between two observations of one
// pair, so the record and the fold are not repeated on every authenticated
// request. It matches the users directory's throttle.
const DefaultObserveTTL = 5 * time.Minute

// observeTimeout bounds the background work one observation does.
const observeTimeout = 30 * time.Second

// maxSeenEntries triggers a prune of expired throttle entries once the map
// grows past it, so the throttle stays bounded by the active set.
const maxSeenEntries = 4096

// Store persists the pair. The PostgreSQL implementation is
// internal/platform/subjects/postgres.
type Store interface {
	// Record notes that address most recently authenticated as subject.
	// fromPerson says whether the principal was the person themselves (an
	// identity-provider session) rather than a key they hold: a key's pair
	// never overwrites a person's (#1759).
	Record(ctx context.Context, address, subject string, fromPerson bool) error
	// Lookup returns the subject recorded for address, or "" when the platform
	// has not seen it authenticate. It answers whatever wrote the row.
	Lookup(ctx context.Context, address string) (string, error)
	// LookupPerson returns the subject recorded for address by the person
	// themselves, or "" when the only pair on record was written by a key.
	// A credential that authenticates AS somebody resolves through this, so a
	// key can never decide who somebody is.
	LookupPerson(ctx context.Context, address string) (string, error)
}

// Book is the recorded pairs plus the fold they enable. It is nil-safe: a
// deployment with no database holds a nil Book and every method is a no-op.
type Book struct {
	store Store
	ttl   time.Duration

	mu   sync.Mutex
	seen map[string]time.Time

	foldMu sync.RWMutex
	fold   *resource.Deps
}

// New builds a Book over store.
func New(store Store) *Book {
	if store == nil {
		return nil
	}
	return &Book{store: store, ttl: DefaultObserveTTL, seen: make(map[string]time.Time)}
}

// BindResources supplies what a fold refiles through: the record store, the URI
// scheme, the move trail and the MCP registry callbacks. It is a setter because
// the managed-resource layer is assembled after the authenticator this Book
// observes on. Unbound, a Book records pairs and folds nothing.
func (b *Book) BindResources(deps resource.Deps) {
	if b == nil {
		return
	}
	b.foldMu.Lock()
	defer b.foldMu.Unlock()
	if deps.Store == nil {
		b.fold = nil
		return
	}
	b.fold = &deps
}

// Observe records the pair an authenticated person presents and folds their
// address-keyed library. It is throttled per pair and runs on a background
// goroutine: recording who authenticated must never block or fail the
// authentication path. Errors are logged, never returned.
//
// Only a principal that is a person, or stands for one, is recorded: an
// identity-provider subject, or an API key, which a person holds and which a
// session presents as its own subject. A script run acts for somebody else and
// is what this exists to serve; an anonymous or auth-disabled session names
// nobody.
func (b *Book) Observe(info *middleware.UserInfo) {
	if b == nil || info == nil || !isPerson(info) {
		return
	}
	address := strings.TrimSpace(info.Email)
	key := normalize(address)
	if key == "" || info.UserID == "" || !b.shouldWrite(key+"\x00"+info.UserID) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), observeTimeout)
		defer cancel()
		if err := b.store.Record(ctx, key, info.UserID, isSelf(info)); err != nil {
			slog.Warn("identity subjects: recording failed", "error", logsan.SanitizeForLog(err.Error()))
			return
		}
		b.foldInto(ctx, address, info.UserID)
	}()
}

// Subject is the subject this address authenticates as in its own sessions, or
// "" when the platform has not seen that person sign in themselves.
//
// It reads only a pair the person recorded, never one a key they hold recorded,
// because a credential resolving through this authenticates AS them: a key that
// could write this row could decide who somebody is (#1759). Unlike ForRun it
// folds nothing -- a caller only asking who somebody is must not move their
// files as a side effect. A lookup that fails is reported, never reported as
// "nobody": a credential is refused on the difference.
func (b *Book) Subject(ctx context.Context, address string) (string, error) {
	if b == nil {
		return "", nil
	}
	key := normalize(address)
	if key == "" {
		return "", nil
	}
	subject, err := b.store.LookupPerson(ctx, key)
	if err != nil {
		return "", fmt.Errorf("reading the subject an address authenticates as: %w", err)
	}
	return subject, nil
}

// ForRun is the subject a run acting for address presents, with everything
// that address filed folded into that subject's library first. It answers ""
// when the platform has not seen the person authenticate, in which case the
// run files by address as it did before and nothing is folded.
func (b *Book) ForRun(ctx context.Context, address string) string {
	if b == nil {
		return ""
	}
	key := normalize(address)
	if key == "" {
		return ""
	}
	subject, err := b.store.Lookup(ctx, key)
	if err != nil {
		slog.Warn("identity subjects: lookup failed; the run files by address",
			"error", logsan.SanitizeForLog(err.Error()))
		return ""
	}
	if subject == "" {
		return ""
	}
	b.foldInto(ctx, strings.TrimSpace(address), subject)
	return subject
}

// foldInto refiles the address-keyed user library into the subject-keyed one.
// The address is the one the rows were keyed by, verbatim: a run keys its
// writes by the version author exactly as recorded.
func (b *Book) foldInto(ctx context.Context, address, subject string) {
	b.foldMu.RLock()
	deps := b.fold
	b.foldMu.RUnlock()
	if deps == nil || address == "" || subject == "" || address == subject {
		return
	}
	from := resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: address}
	to := resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: subject}
	actor := resource.Claims{Sub: subject, Email: address}
	fold, err := resource.FoldLibrary(ctx, *deps, from, to, actor)
	if err != nil {
		slog.Warn("identity subjects: folding the address-keyed library failed",
			"error", logsan.SanitizeForLog(err.Error()))
	}
	if fold == nil {
		return
	}
	for _, e := range fold.Moved {
		slog.Info("identity subjects: refiled a resource into the subject-keyed library",
			"resource_id", e.ID, "from", logsan.SanitizeForLog(e.FromURI), "to", logsan.SanitizeForLog(e.URI))
	}
	for _, e := range fold.Skipped {
		slog.Warn("identity subjects: a resource stays in the address-keyed library; its path is taken in the subject-keyed one",
			"resource_id", e.ID, "uri", logsan.SanitizeForLog(e.URI))
	}
}

// isPerson reports whether an authenticated identity is one a user library is
// keyed for.
// isSelf reports whether the principal IS the person at that address, rather
// than a credential they hold. An identity-provider session is; so is a key
// issued against the account, which presents that person's own subject. A
// service key is not, whatever address it carries.
func isSelf(info *middleware.UserInfo) bool {
	switch info.AuthType {
	case middleware.AuthTypeOIDC, middleware.AuthTypeOAuth:
		return true
	case middleware.AuthTypeAPIKey:
		// A bound key presents the person's own subject; a service key
		// presents its own.
		return !strings.HasPrefix(info.UserID, apiKeySubjectPrefix)
	default:
		return false
	}
}

// apiKeySubjectPrefix opens the subject a key bound to nobody presents.
const apiKeySubjectPrefix = "apikey:"

func isPerson(info *middleware.UserInfo) bool {
	switch info.AuthType {
	case middleware.AuthTypeOIDC, middleware.AuthTypeOAuth, middleware.AuthTypeAPIKey:
		return true
	default:
		return false
	}
}

// normalize is the lookup key for an address: trimmed and lowercased, which is
// how the users directory keys the same person.
func normalize(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// shouldWrite reports whether key has not been observed within the TTL,
// recording the attempt when it has not.
func (b *Book) shouldWrite(key string) bool {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if last, ok := b.seen[key]; ok && now.Sub(last) < b.ttl {
		return false
	}
	if len(b.seen) >= maxSeenEntries {
		for k, t := range b.seen {
			if now.Sub(t) >= b.ttl {
				delete(b.seen, k)
			}
		}
	}
	b.seen[key] = now
	return true
}
