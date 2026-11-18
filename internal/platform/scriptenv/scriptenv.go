// Package scriptenv is what a managed script reads of the application
// environment its run executes in (#2696): platform.env("NAME"), one of the
// application's variables for that environment, and platform.environment(),
// the environment's name. A script no application holds reads None from
// both.
package scriptenv

import (
	"go.starlark.net/starlark"
)

const (
	// CapabilityEnv reads one of the application's variables for the run's
	// environment: its value, or default (None) when it is not set. A value
	// holding {{secret:<name>}} is read as written; the platform fills the
	// stored secret when a request carrying it is sent through a connection
	// the secret allows, so the script never holds the secret.
	CapabilityEnv = "platform.env"
	// CapabilityEnvironment is the name of the environment the run executes
	// in, None for a script no application holds.
	CapabilityEnvironment = "platform.environment"
)

// Env is platform.env(name, default=None) over vars.
func Env(vars map[string]string) *starlark.Builtin {
	return starlark.NewBuiltin(CapabilityEnv, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		var fallback starlark.Value = starlark.None
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name, "default?", &fallback); err != nil {
			return nil, err //nolint:wrapcheck // starlark's error names the builtin
		}
		if v, ok := vars[name]; ok {
			return starlark.String(v), nil
		}
		return fallback, nil
	})
}

// Environment is platform.environment() naming environment.
func Environment(environment string) *starlark.Builtin {
	return starlark.NewBuiltin(CapabilityEnvironment, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
			return nil, err //nolint:wrapcheck // starlark's error names the builtin
		}
		if environment == "" {
			return starlark.None, nil
		}
		return starlark.String(environment), nil
	})
}
