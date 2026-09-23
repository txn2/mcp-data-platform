// Package scriptgrant decides who other than a script's owner may ask for a
// run of it, and binds the parameters a script takes from the caller rather
// than from the request (#1846).
//
// A grant names a persona, a role or an API key. It lets that principal run
// the script over HTTP and read the runs it started, which carry the outputs;
// it never opens the source, the version history, the state or any edit. The
// run still executes as the script principal with its author's roles, so a
// grant decides who may ask, never what the run may reach.
//
// A parameter declared with bind "caller.<claim>" takes the caller's claim,
// which for an API key is one of its attributes. That is what lets one script
// serve every tenant of an embedding application without any caller being
// able to name another tenant in a request body.
package scriptgrant

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Principal kinds a grant names.
const (
	KindPersona = "persona"
	KindRole    = "role"
	KindAPIKey  = "api_key"
)

// apiKeyUserPrefix is how an API key's caller is identified (pkg/auth): the
// key's name after it is what an api_key grant names.
const apiKeyUserPrefix = "apikey:"

// maxPrincipalLen bounds a principal name.
const maxPrincipalLen = 200

// Grant lets one principal run one script.
type Grant struct {
	ScriptID  string    `json:"script_id" example:"3f2b6c1e-8d4a-4b8e-9f1a-2c3d4e5f6a7b"`
	Kind      string    `json:"principal_kind" example:"api_key"`
	Principal string    `json:"principal" example:"reporting-app"`
	GrantedBy string    `json:"granted_by" example:"jane@example.com"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks a grant names a known kind of principal, and one.
func (g Grant) Validate() error {
	switch g.Kind {
	case KindPersona, KindRole, KindAPIKey:
	default:
		return fmt.Errorf("principal_kind is persona, role or api_key, not %q", g.Kind)
	}
	if strings.TrimSpace(g.Principal) == "" || len(g.Principal) > maxPrincipalLen {
		return fmt.Errorf("principal is a %s name of 1 to %d characters", g.Kind, maxPrincipalLen)
	}
	return nil
}

// Caller is who is asking, in the terms a grant names.
type Caller struct {
	UserID  string
	Persona string
	Roles   []string
}

// apiKeyName is the name of the API key the caller authenticated with, or ""
// for a caller who did not use one.
func (c Caller) apiKeyName() string {
	name, ok := strings.CutPrefix(c.UserID, apiKeyUserPrefix)
	if !ok {
		return ""
	}
	return name
}

// Store keeps the grants.
type Store interface {
	// List returns a script's grants, oldest first.
	List(ctx context.Context, scriptID string) ([]Grant, error)
	// Add records a grant; granting what is already granted changes nothing.
	Add(ctx context.Context, g Grant) error
	// Remove withdraws a grant, reporting whether there was one.
	Remove(ctx context.Context, g Grant) (bool, error)
	// Allows reports whether any grant on the script names the caller.
	Allows(ctx context.Context, scriptID string, c Caller) (bool, error)
	// GrantedScriptIDs lists the scripts granted to anything the caller is.
	GrantedScriptIDs(ctx context.Context, c Caller) ([]string, error)
}

// ErrBoundInRequest refuses a request that sends a value for a caller-bound
// parameter. It is refused rather than overridden, so a client that believes
// it chose the value learns that it did not.
var ErrBoundInRequest = errors.New("this parameter's value is the caller's")

// ErrClaimMissing refuses a caller who does not carry a claim a parameter is
// bound to: without it there is no value the script may be run with.
var ErrClaimMissing = errors.New("the caller does not carry the claim this script is bound to")

// BindCaller binds a run's parameters, taking each caller-bound parameter from
// claims and every other one from values, through script.BindParams. The
// bound set is what the run records, the caller's values included.
func BindCaller(defs []script.Param, values, claims map[string]any) (map[string]any, error) {
	merged := maps.Clone(values)
	if merged == nil {
		merged = map[string]any{}
	}
	for _, p := range defs {
		if p.Bind == "" {
			continue
		}
		if _, sent := values[p.Name]; sent {
			return nil, fmt.Errorf("parameter %q: %w (it is bound to %s); leave it out of the request", p.Name, ErrBoundInRequest, p.Bind)
		}
		v, ok := claim(claims, strings.TrimPrefix(p.Bind, "caller."))
		if !ok {
			return nil, fmt.Errorf("parameter %q reads %s: %w", p.Name, p.Bind, ErrClaimMissing)
		}
		merged[p.Name] = v
	}
	bound, err := script.BindParams(defs, merged)
	if err != nil {
		return nil, fmt.Errorf("binding parameters: %w", err)
	}
	return bound, nil
}

// claim reads a dotted claim path, answering only a string or a number: a
// claim that is an object or a list is not a parameter value.
func claim(claims map[string]any, path string) (any, bool) {
	var cur any = claims
	for part := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, v != ""
	case float64, int, int64:
		return v, true
	default:
		return nil, false
	}
}
