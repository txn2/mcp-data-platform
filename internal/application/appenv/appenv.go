// Package appenv is how a run finds the application environment it executes
// in (#2696): the version that environment's release pins and its variables.
package appenv

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// ErrNoEnvironment is an environment the application does not have.
var ErrNoEnvironment = errors.New("the application has no such environment")

// Script is what a run of a script in an environment executes
// with (#2696): the version the environment's release pins (0 when it pins
// none), the environment's name, and its variables. OK is false for a script
// no application holds, which runs as it always has.
type Script struct {
	OK          bool
	Environment string
	Version     int
	Variables   map[string]string
}

// Resolver resolves a script's environment ("" for the last).
type Resolver interface {
	ForScript(ctx context.Context, scriptID, env string) (*Script, error)
}

// RunVersionIn is the version a run of sc in env executes. The last
// environment, named "", is what the script's released version already
// holds, read with no lookup; any other is the version that environment's
// release pins, refused when sc belongs to no application, the application
// has no such environment, or its release pins no version of sc.
func RunVersionIn(ctx context.Context, r Resolver, sc *script.Script, env string) (int, error) {
	if env == "" {
		return sc.RunVersion(), nil
	}
	if r == nil {
		return 0, fmt.Errorf("environment %q: this deployment keeps no application environments", env)
	}
	res, err := r.ForScript(ctx, sc.ID, env)
	if errors.Is(err, ErrNoEnvironment) {
		return 0, fmt.Errorf("the application holding script %s has no environment %q", sc.Name, env)
	}
	if err != nil {
		return 0, err //nolint:wrapcheck // the resolver says what it read
	}
	if !res.OK {
		return 0, fmt.Errorf("script %s belongs to no application, so it has no environment %q", sc.Name, env)
	}
	if res.Version == 0 {
		return 0, fmt.Errorf("environment %s runs no release that holds script %s; release or promote one to it", res.Environment, sc.Name)
	}
	return res.Version, nil
}

// ForRun is the environment a run of scriptID in env ("" for the last)
// executes in and its variables, read when the run executes so a variable
// changed since it was queued is the one it sees. A script no application
// holds runs in none; a nil resolver resolves nothing.
func ForRun(ctx context.Context, r Resolver, scriptID, env string) (name string, vars map[string]string, err error) {
	if r == nil {
		return "", nil, nil
	}
	res, err := r.ForScript(ctx, scriptID, env)
	if err != nil || !res.OK {
		return "", nil, err //nolint:wrapcheck // read by identity
	}
	return res.Environment, res.Variables, nil
}
