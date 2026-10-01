package trino

import (
	"errors"
	"fmt"
	"sort"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	"github.com/txn2/mcp-trino/pkg/multiserver"
)

// ErrConnectionRefused wraps every connection the Trino client refuses to
// open, so a caller can tell a configuration the client will never accept
// from a transient failure.
var ErrConnectionRefused = errors.New("trino connection refused")

// Validate reports every connection the Trino client would refuse to open, all
// of them at once (#2014).
//
// The client validates a connection's settings when it is built, and since
// mcp-trino v1.6.0 that includes refusing a password over plain HTTP. The
// refusal used to surface only when the server was constructed, after the
// database had already been migrated, which left a deployment that could run
// neither the new release nor the one before it. Validate runs the client's own
// check over each connection's effective settings, the ones the client is
// built from (a connection inherits what it leaves unset from the default
// connection), so a caller can refuse the configuration before anything is
// changed.
func (mc MultiConfig) Validate() error {
	return joinRefusals(mc.Refused())
}

// Refused reports, by connection name, each connection the Trino client would
// refuse to open, judged on the settings the client is built from. Nil when it
// would open every one.
func (mc MultiConfig) Refused() map[string]error {
	if len(mc.Instances) == 0 {
		return nil
	}
	defaultName := mc.resolveDefault()
	defaultCfg, ok := mc.Instances[defaultName]
	if !ok {
		return map[string]error{defaultName: fmt.Errorf("default connection %q not found in instances", defaultName)}
	}
	return refusedClientConfigs(buildMultiserverConfig(defaultName, defaultCfg, mc.Instances))
}

// resolveDefault is the default connection's name: the one configured, or the
// first instance alphabetically when none is.
func (mc MultiConfig) resolveDefault() string {
	if mc.DefaultConnection != "" {
		return mc.DefaultConnection
	}
	names := make([]string, 0, len(mc.Instances))
	for name := range mc.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

// validateClientConfigs runs the client's validation over every connection of
// a multiserver configuration, and joins what it refused in name order.
func validateClientConfigs(ms multiserver.Config) error {
	return joinRefusals(refusedClientConfigs(ms))
}

// refusedClientConfigs is each connection of a multiserver configuration the
// client refuses, by name.
func refusedClientConfigs(ms multiserver.Config) map[string]error {
	var refused map[string]error
	for _, name := range ms.ConnectionNames() {
		cc, err := ms.ClientConfig(name)
		if err == nil {
			err = cc.Validate()
		}
		if err != nil {
			if refused == nil {
				refused = map[string]error{}
			}
			refused[name] = connectionRefused(name, cc, err)
		}
	}
	return refused
}

// joinRefusals joins refusals in connection-name order, nil when there are
// none.
func joinRefusals(refused map[string]error) error {
	names := make([]string, 0, len(refused))
	for name := range refused {
		names = append(names, name)
	}
	sort.Strings(names)
	errs := make([]error, 0, len(names))
	for _, name := range names {
		errs = append(errs, refused[name])
	}
	return errors.Join(errs...) //nolint:wrapcheck // each joined error is already wrapped by connectionRefused
}

// connectionRefused names the connection and, for a password over plain HTTP,
// the refusal an upgrade to mcp-trino v1.6.0 introduced, what to change.
func connectionRefused(name string, cc trinoclient.Config, err error) error {
	if cc.Password != "" && !cc.SSL {
		return fmt.Errorf("connection %q: %w: %w; a password is sent only over TLS, so point the connection at the "+
			"coordinator's HTTPS endpoint and set ssl: true, or remove the password", name, ErrConnectionRefused, err)
	}
	return fmt.Errorf("connection %q: %w: %w", name, ErrConnectionRefused, err)
}
