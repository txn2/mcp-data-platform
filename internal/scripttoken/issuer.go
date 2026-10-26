package scripttoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// KeyReader reads a stored secret's value for this use, refusing a secret
// whose allow_connections does not list SecretUse or whose allow_personas
// does not name persona (the administrator's is unrestricted).
type KeyReader func(ctx context.Context, name, persona string, admin bool) (string, error)

// Authority resolves the persona roles act as and whether it is the
// administrator's, the resolution a run's own session gets from the persona
// layer.
type Authority func(roles []string) (persona string, admin bool)

// Issuer answers a run's mints and verifies: the deployment's secrets and
// single-use record, and Bind's per-run identity.
type Issuer struct {
	Keys KeyReader
	Uses Uses
	// Authority resolves a run's roles to its persona; nil resolves none, so
	// only a key naming no personas can be used.
	Authority Authority
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// decrypter answers platform.decrypt (SetDecrypter); nil refuses it.
	decrypter atomic.Pointer[Decrypter]
}

// Bind is the Caller for one run: where it mints and verifies, and the roles
// the run presents, whose persona a key's allow_personas is checked against.
// A nil Issuer binds nothing, so a deployment without one leaves the members
// saying so.
func (i *Issuer) Bind(at Binding, roles []string) Caller {
	if i == nil {
		return nil
	}
	b := &bound{issuer: i, at: at}
	if i.Authority != nil {
		b.persona, b.admin = i.Authority(roles)
	}
	return b
}

type bound struct {
	issuer  *Issuer
	at      Binding
	persona string
	admin   bool
}

func (b *bound) now() time.Time {
	if b.issuer.Now != nil {
		return b.issuer.Now()
	}
	return time.Now()
}

// CallTool answers platform.mint_token, platform.verify_token and
// platform.decrypt.
func (b *bound) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if name == CapabilityDecrypt {
		return b.decrypt(ctx, args)
	}
	secret, _ := args["secret"].(string)
	value, err := b.issuer.Keys(ctx, secret, b.persona, b.admin)
	if err != nil {
		return nil, err
	}
	key := Key(value)
	switch name {
	case CapabilityMint:
		return b.mint(key, args)
	case CapabilityVerify:
		return b.verify(ctx, key, args)
	default:
		return nil, fmt.Errorf("not a token member: %s", name)
	}
}

func (b *bound) mint(key []byte, args map[string]any) (map[string]any, error) {
	var m struct {
		Claims    map[string]any `json:"claims"`
		TTL       int64          `json:"ttl"`
		Endpoints []string       `json:"endpoints"`
		SingleUse bool           `json:"single_use"`
	}
	if err := remarshal(args, &m); err != nil {
		return nil, err
	}
	tok, err := Sign(key, b.at, Mint{
		Claims: m.Claims, TTL: time.Duration(m.TTL) * time.Second, Endpoints: m.Endpoints, SingleUse: m.SingleUse,
	}, b.now())
	if err != nil {
		return nil, err
	}
	return map[string]any{"token": tok}, nil
}

func (b *bound) verify(ctx context.Context, key []byte, args map[string]any) (map[string]any, error) {
	token, _ := args["token"].(string)
	t, err := Verify(key, b.at, token, b.now())
	if errors.Is(err, ErrRefused) {
		return refused(Reason(err), err.Error()), nil
	}
	if err != nil {
		return nil, err
	}
	if t.SingleUse {
		if b.issuer.Uses == nil {
			return nil, errors.New("this deployment cannot record a single-use token's use")
		}
		first, err := b.issuer.Uses.Use(ctx, t.ID, t.Expires)
		if err != nil {
			return nil, err //nolint:wrapcheck // names the record it could not write
		}
		if !first {
			return refused(ReasonUsed, "token refused: the token was minted for one use and has been used"), nil
		}
	}
	return map[string]any{
		"ok": true, "claims": t.Claims, "expires_at": t.Expires.Format(time.RFC3339), "single_use": t.SingleUse,
	}, nil
}

func refused(reason, message string) map[string]any {
	return map[string]any{"ok": false, "reason": reason, "message": message}
}

// remarshal reads a call's arguments into the shape a member takes, as a
// recording's JSON round trip leaves them.
func remarshal(in map[string]any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding the arguments: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("reading the arguments: %w", err)
	}
	return nil
}
