// Package apikeyissue is the one place a database-managed API key comes into
// being (#1759).
//
// Two surfaces issue keys: the admin route, where an operator names a key and
// may bind it to somebody's account, and the portal route, where a person
// issues one for themselves. What they share is everything that decides whether
// the key will work -- that a bound account resolves to a person the platform
// can speak for, that the name is free, that the key exists in the store before
// it authenticates anywhere, and that peer replicas are told. Forking that
// would mean one surface could hand out a key the other's rules would have
// refused.
//
// What the surfaces keep for themselves is what they present: their own request
// shapes, their own refusal text and status codes, and their own naming (the
// portal composes a name scoped to its owner). This package raises typed
// refusals and never writes a response.
package apikeyissue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/internal/platform/apikeystore"
	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// Refusals an issuer raises. Each surface maps these to its own status code and
// wording, so the rule lives here and the phrasing lives with the reader.
var (
	// ErrNameTaken means a key already carries the name asked for.
	ErrNameTaken = errors.New("a key with that name already exists")
	// ErrUnknownPerson means the account a key was to be bound to is not one
	// the platform can speak for: no directory row, or nobody it has seen sign
	// in, so there is no subject to authenticate as and no roles to carry.
	ErrUnknownPerson = errors.New("the platform has no record of that person signing in")
	// ErrNoStore means the deployment has nowhere to put a key, so none can be
	// issued. A database-less deployment reaches this rather than minting a
	// value that would authenticate nowhere.
	ErrNoStore = errors.New("this deployment stores no api keys")
	// ErrReservedName means the name asked for is one the platform composes
	// for a key somebody issued for themselves.
	ErrReservedName = errors.New("that name belongs to the platform")
)

// SelfIssuedPrefix opens the name every key a person issued for themselves is
// stored under. Key names are unique across a deployment, so a person's chosen
// name is scoped to them; this is the one spelling of that scope, so the
// surface that composes such a name and the surface that must refuse to
// impersonate one cannot disagree about what it looks like.
const SelfIssuedPrefix = "user:"

// Manager mints key values and refreshes the keys held in memory. Implemented
// by *auth.APIKeyAuthenticator.
type Manager interface {
	GenerateKey(def auth.APIKey) (string, error)
	SyncHashedKeys(ctx context.Context) error
}

// Store persists the key. Implemented by the platform's API key store.
type Store interface {
	Create(ctx context.Context, def apikeystore.Definition) error
}

// Issuer issues keys against one deployment's key store.
type Issuer struct {
	// Manager mints the value and holds the copy in memory.
	Manager Manager
	// Store is where a key comes into being: it authenticates once it is here.
	Store Store
	// Principals resolves the account a bound key names. Nil means this
	// deployment cannot bind a key to anybody, and every bound request is
	// refused with ErrUnknownPerson rather than issued unresolvable.
	Principals auth.PrincipalSource
	// Announce tells peer replicas a key was written, so their in-memory copy
	// is refreshed. Optional: a key is live the moment it is stored, so this
	// only shortens how long a peer's listing is stale.
	Announce func()
}

// Request is a key to issue.
type Request struct {
	// Name is the key's identity in the store, unique across the deployment.
	Name string
	// Description is what the key is for, as its holder wrote it.
	Description string
	// Email is the address a service key carries, already defaulted by the
	// caller. It is ignored for a bound key, which always carries its
	// person's address.
	Email string
	// UserEmail binds the key to a person's account. Empty issues the
	// standalone service key a key has always been.
	UserEmail string
	// Roles is the key's own role set. A service key must carry one. A bound
	// key with none follows the person it is bound to, on every request; a
	// bound key with one carries that set and nothing else, which is not
	// checked against what that person holds.
	Roles []string
	// ExpiresAt ends the key, or nil for a key that never expires.
	ExpiresAt *time.Time
	// CreatedBy records who issued it.
	CreatedBy string
}

// Issued is the key handed back. Key is the only time the value exists outside
// the holder's keeping: the store has its hash and nothing else.
type Issued struct {
	Key   string
	Email string
	Roles []string
	// Person is the account the key resolved to, or nil for a service key. Its
	// roles are what a bound key with no roles of its own will carry.
	Person *auth.BoundPrincipal
}

// Issue mints, stores and announces a key, or refuses it.
//
// The binding is resolved before the value is minted, through the same resolver
// the authenticator will use, so a key is never handed to somebody for an
// account that will not resolve when they present it.
func (i *Issuer) Issue(ctx context.Context, req Request) (*Issued, error) {
	if i == nil || i.Manager == nil {
		return nil, ErrNoStore
	}
	// A key issued against nobody may not wear the name of a key somebody
	// issued for themselves: that name would sit in the reserved namespace
	// without the ownership that makes it theirs, and it would take a name its
	// apparent owner cannot see, revoke, or use.
	if req.UserEmail == "" && strings.HasPrefix(req.Name, SelfIssuedPrefix) {
		return nil, ErrReservedName
	}

	person, err := i.principal(ctx, req.UserEmail)
	if err != nil {
		return nil, err
	}
	def := keyDefinition(req, person)

	value, err := i.mint(def)
	if err != nil {
		return nil, err
	}
	if err := i.store(ctx, def, value, req.CreatedBy); err != nil {
		return nil, err
	}

	i.settle(ctx)
	return &Issued{Key: value, Email: def.Email, Roles: def.Roles, Person: person}, nil
}

// principal is the person a request names, or nil when it names nobody.
func (i *Issuer) principal(ctx context.Context, email string) (*auth.BoundPrincipal, error) {
	if email == "" {
		return nil, nil //nolint:nilnil // a request that binds to nobody names nobody, which is not a failure
	}
	return i.resolve(ctx, email)
}

// keyDefinition is the key a request asks for, as the authenticator will hold
// it.
func keyDefinition(req Request, person *auth.BoundPrincipal) auth.APIKey {
	return auth.APIKey{
		Name:        req.Name,
		Email:       keyEmail(req, person),
		Description: req.Description,
		Roles:       req.Roles,
		ExpiresAt:   req.ExpiresAt,
		UserEmail:   boundAddress(person),
	}
}

// mint produces the key's value. The name is checked against the inventory
// before anything is written, so a name already taken is answered as that
// rather than as a storage failure; minting holds nothing, and the key exists
// once it is stored (#1715).
func (i *Issuer) mint(def auth.APIKey) (string, error) {
	value, err := i.Manager.GenerateKey(def)
	if errors.Is(err, auth.ErrKeyNameTaken) {
		return "", ErrNameTaken
	}
	if err != nil {
		// Minting also fails when the system is short of entropy, which is not
		// the requester's doing: telling them the name they chose is taken
		// would send them off renaming a key that would fail under any name.
		return "", fmt.Errorf("minting the key: %w", err)
	}
	if i.Store == nil {
		return "", ErrNoStore
	}
	return value, nil
}

// store puts the key's hash where every replica reads it, which is what makes
// the key authenticate.
func (i *Issuer) store(ctx context.Context, def auth.APIKey, value, createdBy string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(value), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing the key: %w", err)
	}
	err = i.Store.Create(ctx, apikeystore.Definition{
		Name:        def.Name,
		KeyHash:     string(hash),
		Email:       def.Email,
		Description: def.Description,
		Roles:       def.Roles,
		ExpiresAt:   def.ExpiresAt,
		UserEmail:   def.UserEmail,
		CreatedBy:   createdBy,
	})
	if errors.Is(err, apikeystore.ErrExists) {
		return ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("storing the key: %w", err)
	}
	return nil
}

// resolve returns the person a bound request names, refusing when the platform
// cannot speak for them.
func (i *Issuer) resolve(ctx context.Context, email string) (*auth.BoundPrincipal, error) {
	if i.Principals == nil {
		return nil, ErrUnknownPerson
	}
	person, err := i.Principals.BoundPrincipal(ctx, email)
	if errors.Is(err, auth.ErrNoBoundPrincipal) {
		return nil, ErrUnknownPerson
	}
	if err != nil {
		return nil, fmt.Errorf("resolving the account the key is bound to: %w", err)
	}
	return person, nil
}

// settle brings this replica's copy up to the store it just wrote, and tells
// the others. Neither is what makes the key work -- it authenticates from the
// store -- so a failure is not the caller's problem and is not reported.
func (i *Issuer) settle(ctx context.Context) {
	_ = i.Manager.SyncHashedKeys(ctx) //nolint:errcheck // the key is live in the store either way
	if i.Announce != nil {
		i.Announce()
	}
}

// keyEmail is the address the key carries: the person's for a bound key, and
// whatever the requester supplied otherwise. A bound key never carries an
// address other than its person's, so a listing cannot show one thing and the
// session present another.
func keyEmail(req Request, person *auth.BoundPrincipal) string {
	if person != nil {
		return person.Email
	}
	return req.Email
}

// boundAddress is the account a resolved person is addressed by.
func boundAddress(person *auth.BoundPrincipal) string {
	if person == nil {
		return ""
	}
	return person.Email
}
