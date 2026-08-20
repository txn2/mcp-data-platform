package script

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Capability names. A capability is one host binding a script may call, and
// the set is closed: a review is only meaningful while every capability a
// script can reach can be listed, which stops being true the moment the set
// grows a wildcard or an open-ended tool surface. The engine that implements
// these bindings (internal/platform/scriptrun) refers to these names rather
// than defining its own, so the vocabulary a grant is written in and the
// vocabulary the interpreter enforces are one list.
const (
	CapabilityQuery  = "platform.query"
	CapabilityExport = "platform.export"
	// CapabilityPublishData is the narrow write: replace the data region of a
	// portal document this script already owns, and touch nothing else in it.
	// It is deliberately separate from CapabilityExport, whose document arm
	// writes arbitrary markup — a reviewer granting this one is approving a
	// statement about ONE region of one asset, not a page.
	CapabilityPublishData = "platform.publish_data"
)

// Capabilities is the full host surface, in the order help, validate, and the
// review surfaces report it.
var Capabilities = []string{CapabilityQuery, CapabilityExport, CapabilityPublishData}

// Grants is the capability set bound to one approved version: the authority
// the run presents, the connections it may reach, the host bindings it may
// call, and where its outputs may land.
//
// Grants are NOT persona-shaped. A persona is an org role that drifts as the
// organization changes; a script's needs are static properties of reviewed
// code, so they are recorded per version and re-approved when they change.
// There are no wildcards anywhere in this type on purpose: a reviewer must be
// able to read the grant and know exactly what was approved.
//
// Roles are not caller input. The approval action copies them from the
// version's author, so approving cannot hand a script authority its author did
// not hold; see Version.AuthorRoles.
type Grants struct {
	// Roles is the authority the run presents to the authorization middleware,
	// copied from the approved version's author. The middleware resolves them
	// to a persona exactly as it does for a human caller, and that persona —
	// not this struct — is the authority of record.
	Roles []string `json:"roles"`

	// Connections is the set of named connections the script may query. A
	// query that names no connection is refused rather than defaulted: the
	// grant cannot verify a connection the call did not name.
	Connections []string `json:"connections"`

	// Capabilities is the set of host bindings the script may call.
	Capabilities []string `json:"capabilities"`

	// Destinations is the set of places the script may write output, each one a
	// resolved address rather than a label. An empty list means the script may
	// compute but not persist.
	Destinations []Destination `json:"destinations"`
}

// ErrNoGrants marks an execution attempt against a version carrying no grant
// record, which is a version that was never approved.
var ErrNoGrants = errors.New("this script version carries no approved capability grant")

// ErrInvalidGrant marks an approval refused because the capability set it
// carries is not one the platform can bind. It is a sentinel rather than a
// message shape so a REST surface can answer 400 instead of 500 by asking what
// the error IS, not what it looks like.
var ErrInvalidGrant = errors.New("invalid grant")

// AllowsCapability reports whether the grant permits one host binding.
func (g Grants) AllowsCapability(name string) bool {
	return slices.Contains(g.Capabilities, name)
}

// AllowsConnection reports whether the grant permits one named connection. An
// empty name is never allowed: the platform would resolve it to a default the
// approval never named.
func (g Grants) AllowsConnection(name string) bool {
	return name != "" && slices.Contains(g.Connections, name)
}

// CheckConnectionParams refuses a bound value for a connection-typed parameter
// that the grant does not permit (#1361).
//
// The run would refuse it anyway: a connection reaches platform.query as an
// argument, and the host checks every one against this same list. What this
// buys is WHERE the refusal lands. Without it a mistyped connection name is
// accepted by every surface that takes it, queued, executed, and reported as a
// failed run to somebody who is no longer looking; with it the surface that
// asked for the value answers, naming what this script was approved to reach.
//
// It applies only to a run confined by a grant. A draft executes under its
// author's own identity with no grant layer, so there is nothing here to check
// it against and the author's persona is the boundary.
func CheckConnectionParams(defs []Param, bound map[string]any, g Grants) error {
	for _, p := range defs {
		if p.Type != ParamTypeConnection {
			continue
		}
		name, _ := bound[p.Name].(string)
		if g.AllowsConnection(name) {
			continue
		}
		return fmt.Errorf(
			"parameter %q names connection %q, which this script was not approved to reach; it may reach %s",
			p.Name, name, connectionList(g.Connections))
	}
	return nil
}

// connectionList renders a grant's connections for a refusal, naming the empty
// case rather than printing an empty list: a script granted no connection at
// all cannot be fixed by picking a different one.
func connectionList(names []string) string {
	if len(names) == 0 {
		return "no connections at all"
	}
	return strings.Join(names, ", ")
}

// AllowsDestination reports whether the grant permits writing to one named
// destination.
func (g Grants) AllowsDestination(name string) bool {
	_, ok := g.Destination(name)
	return ok
}

// Destination resolves one granted destination by the name a script writes.
// The resolved record — not the name — is what the write is issued against, so
// a script can only ever reach the address its approval pinned.
func (g Grants) Destination(name string) (Destination, bool) {
	for _, d := range g.Destinations {
		if d.Name == name {
			return d, true
		}
	}
	return Destination{}, false
}

// DestinationNames lists the granted destinations by name, for the messages
// that tell an author what this script was approved to write to.
func (g Grants) DestinationNames() []string {
	out := make([]string, 0, len(g.Destinations))
	for _, d := range g.Destinations {
		out = append(out, d.Name)
	}
	return out
}

// IsZero reports whether the grant is entirely empty, which distinguishes an
// unapproved version from one deliberately approved with nothing granted.
func (g Grants) IsZero() bool {
	return len(g.Roles) == 0 && len(g.Connections) == 0 &&
		len(g.Capabilities) == 0 && len(g.Destinations) == 0
}

// Validate checks a grant about to be bound to a version at approval: every
// capability and destination must be one the platform implements, no entry may
// be blank, and the authority must be non-empty.
//
// The roles check is not a formality. Roles resolve to a persona, and a caller
// presenting none resolves to the deny-all default persona, so a version
// approved without them would be approved into a script that fails on its
// first tool call. Refusing here reports that at approval, where a human can
// act on it, rather than at 3am on the first scheduled fire.
func (g Grants) Validate() error {
	if len(g.Roles) == 0 {
		return errors.New("the version's author held no roles, so an approved run would resolve to the deny-all persona and could call nothing")
	}
	if err := validateGrantList("capability", g.Capabilities, Capabilities); err != nil {
		return err
	}
	if err := g.validateDestinations(); err != nil {
		return err
	}
	if slices.Contains(g.Connections, "") {
		return errors.New("a granted connection cannot be blank")
	}
	return nil
}

// validateDestinations checks every granted destination and refuses a repeated
// name. Two destinations sharing a name would make the address a script reaches
// depend on list order, which is the one thing an approval must not leave open.
func (g Grants) validateDestinations() error {
	seen := make(map[string]bool, len(g.Destinations))
	for _, d := range g.Destinations {
		if err := d.Validate(); err != nil {
			return err
		}
		if seen[d.Name] {
			return fmt.Errorf("destination %q is granted twice; one name is one place", d.Name)
		}
		seen[d.Name] = true
	}
	return nil
}

// validateGrantList checks that every entry of a granted list is one of the
// known values, naming the offender and the closed set it must come from.
func validateGrantList(kind string, granted, known []string) error {
	for _, v := range granted {
		if !slices.Contains(known, v) {
			return fmt.Errorf("unknown %s %q: the platform implements %v", kind, v, known)
		}
	}
	return nil
}

// Missing is what a script's source references that its grant does not cover,
// on each axis a static validation can read.
type Missing struct {
	Capabilities []string
	Connections  []string
	Destinations []string
}

// Any reports whether the code reaches for anything it was not granted.
func (m Missing) Any() bool {
	return len(m.Capabilities) > 0 || len(m.Connections) > 0 || len(m.Destinations) > 0
}

// MissingFor reports what a script's source references that the grant does not
// cover, given the capability, connection, and destination names a static
// validation found. It is the referenced-versus-granted diff a reviewer reads
// before approving, and the same diff the approval action refuses on: approving
// a script whose code reaches for something it was not granted approves a run
// that fails.
func (g Grants) MissingFor(capabilities, connections, destinations []string) Missing {
	var missing Missing
	for _, c := range capabilities {
		if !g.AllowsCapability(c) {
			missing.Capabilities = append(missing.Capabilities, c)
		}
	}
	for _, c := range connections {
		if !g.AllowsConnection(c) {
			missing.Connections = append(missing.Connections, c)
		}
	}
	for _, d := range destinations {
		if !g.AllowsDestination(d) {
			missing.Destinations = append(missing.Destinations, d)
		}
	}
	return missing
}
