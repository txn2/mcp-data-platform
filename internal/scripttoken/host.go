package scripttoken

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// The platform members a script mints and verifies with.
const (
	CapabilityMint   = "platform.mint_token"
	CapabilityVerify = "platform.verify_token"
)

// inBinding prefixes an error with the member that failed.
const inBinding = "in %s: %w"

// SecretUse is the name a token key's allow_connections lists: a token key
// is never sent through a connection, so the reserved name is what scopes a
// stored secret to this use, as botcheck.SecretUse does for a provider key.
const SecretUse = "script_token"

// Caller answers one mint or verify, named by its capability, with the
// arguments the script passed: an Issuer in a run, the recording in a test.
type Caller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
}

// Host is the two members as a run binds them, the way scriptpace.Clock binds
// remaining_ms: in a run each call goes to Live and is recorded, and in a
// test it is answered from the recording, so a test of a handler that mints
// or verifies needs neither the key nor the database.
type Host struct {
	// Ctx is the run's context.
	Ctx context.Context //nolint:containedctx // the run's lifetime, which both members read
	// Testing is a test or a replay: calls go to Replay.
	Testing bool
	// Replay answers a test's calls, from the recording or a declared answer.
	Replay Caller
	// Live answers a run's calls; nil where this deployment issues no tokens.
	Live Caller
	// Record records a run's call and its answer, nil when nothing records.
	Record func(tool string, args, out map[string]any, err error)
}

// Mint is platform.mint_token(claims, ttl, secret, endpoints=None,
// single_use=False): the token, as a string.
func (h Host) Mint(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		claims    *starlark.Dict
		ttl       int
		secret    string
		endpoints *starlark.List
		singleUse bool
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "claims", &claims, "ttl", &ttl, "secret", &secret,
		"endpoints?", &endpoints, "single_use?", &singleUse); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	payload, err := mintArgs(claims, ttl, secret, endpoints, singleUse)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	out, err := h.call(CapabilityMint, payload)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	tok, ok := out["token"].(string)
	if !ok {
		return nil, fmt.Errorf("in %s: the answer carries no token", b.Name())
	}
	return starlark.String(tok), nil
}

// Verify is platform.verify_token(token, secret): a dict whose "ok" says
// whether the token holds, with its "claims" when it does and the "reason"
// it was refused for when it does not, so a script answers a refusal itself.
func (h Host) Verify(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var token, secret string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "token", &token, "secret", &secret); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	if secret == "" {
		return nil, fmt.Errorf("in %s: secret is empty; name the stored secret the token was minted with", b.Name())
	}
	out, err := h.call(CapabilityVerify, map[string]any{"token": token, "secret": secret})
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	v, err := starlarkconv.ResultToStarlark(out)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	return v, nil
}

// mintArgs is a mint's arguments as a call carries and a recording keeps
// them: the secret by name, never its value.
func mintArgs(claims *starlark.Dict, ttl int, secret string, endpoints *starlark.List, singleUse bool) (map[string]any, error) {
	if secret == "" {
		return nil, errors.New("secret is empty; name the stored secret whose allow_connections lists " + SecretUse)
	}
	if ttl <= 0 || time.Duration(ttl)*time.Second > MaxTTL {
		return nil, fmt.Errorf("ttl is the seconds the token lives, more than 0 and at most %d", int(MaxTTL.Seconds()))
	}
	c, err := starlarkconv.Arguments(claims)
	if err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	names := []any{}
	if endpoints != nil {
		for i := range endpoints.Len() {
			s, ok := endpoints.Index(i).(starlark.String)
			if !ok {
				return nil, fmt.Errorf("endpoints must be a list of endpoint names, got a %s", endpoints.Index(i).Type())
			}
			names = append(names, string(s))
		}
	}
	return map[string]any{"claims": c, "ttl": ttl, "secret": secret, "endpoints": names, "single_use": singleUse}, nil
}

// call sends one mint or verify where the run's calls go.
func (h Host) call(tool string, args map[string]any) (map[string]any, error) {
	if h.Testing {
		if h.Replay == nil {
			return nil, errors.New("this test has no recording to answer it from")
		}
		return h.Replay.CallTool(h.Ctx, tool, args) //nolint:wrapcheck // the recording's answer, named by the binding
	}
	if h.Live == nil {
		return nil, errors.New("this deployment issues no tokens here")
	}
	out, err := h.Live.CallTool(h.Ctx, tool, args)
	if h.Record != nil {
		h.Record(tool, args, out, err)
	}
	return out, err //nolint:wrapcheck // the issuer's answer, named by the binding
}
