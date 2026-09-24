// Package scriptdest resolves the destination a managed script names for an
// output to the address the deployment's configuration declares for it. It is
// the one resolution the run and validate share, so a destination validate
// accepts is one a run can write to, refused in the same words when it is not.
package scriptdest

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Resolve turns a destination name into the address it stands for,
// against the set a deployment declares. The portal is built in; every other
// name comes from the scripts.destinations configuration.
//
// The run and validate both call it: validate reports a script that names a
// destination this deployment does not declare (#1415), and the two must
// refuse in the same words for the same reason. A script whose export was
// accepted by validate and then refused at run time had already executed its
// queries by the time it learned.
func Resolve(name string, declared []script.Destination) (script.Destination, error) {
	switch name {
	case script.DestinationPortal:
		return script.PortalDestination(), nil
	case script.DestinationResources:
		return script.ResourcesDestination(), nil
	}
	for _, d := range declared {
		if d.Name == name {
			return d, nil
		}
	}
	if len(declared) == 0 {
		return script.Destination{}, fmt.Errorf("destination %q is not configured: this deployment declares no bucket destinations, so %q and %q are the only places a script can write",
			name, script.DestinationPortal, script.DestinationResources)
	}
	return script.Destination{}, fmt.Errorf("destination %q is not configured; this deployment declares %s, and %q and %q are always available",
		name, strings.Join(destinationNames(declared), ", "), script.DestinationPortal, script.DestinationResources)
}

// destinationNames lists the configured destinations for a refusal.
func destinationNames(destinations []script.Destination) []string {
	out := make([]string, 0, len(destinations))
	for _, d := range destinations {
		out = append(out, d.Name)
	}
	return out
}
