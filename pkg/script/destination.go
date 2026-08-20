package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DestinationPortal is the name of the portal destination: an asset owned by
// the platform, versioned by the platform, and reachable only through it. It is
// where an output lands when a script names no destination, which keeps the
// common case — a scheduled report refreshing the asset people already read —
// the shortest thing to write.
const DestinationPortal = "portal"

// Destination kinds. The kind decides what the platform does with the bytes,
// and the set is closed for the same reason the capability set is: a review is
// only meaningful while a reviewer can read what kind of place they are
// approving.
const (
	// DestinationKindPortal versions a portal asset.
	DestinationKindPortal = "portal"

	// DestinationKindS3 writes an object to a bucket over a named platform S3
	// connection. It is the only way an output leaves the platform.
	DestinationKindS3 = "s3"
)

// DestinationKinds is the full set of destination kinds.
var DestinationKinds = []string{DestinationKindPortal, DestinationKindS3}

// Key limits. maxObjectKeyLength is S3's own limit on a full key; a granted
// prefix is bounded well inside it so the key a script writes underneath always
// has room.
const (
	maxObjectKeyLength = 1024
	maxPrefixLength    = 512
)

// Destination is one place an approved version may write: named by the script,
// resolved by the platform.
//
// It carries the ADDRESS rather than only a label, because the address is what
// a reviewer is agreeing to. A grant naming just "acme-drop" would leave the
// meaning of that name in configuration the reviewer cannot see at approval
// time and an operator could repoint afterwards without anyone approving
// anything. Pinning the connection, the bucket, and the prefix onto the version
// makes an approval say what it did, and makes repointing it a re-approval.
//
// A script supplies no endpoint, no credential, and no bucket. It names a
// destination and everything below comes from what was approved, which is why
// there is no arbitrary egress to have: the only network a script reaches is
// the operator-configured connection set.
type Destination struct {
	// Name is what a script writes as destination="...", unique within a grant.
	Name string `json:"name" example:"acme-drop"`

	// Kind is one of DestinationKinds.
	Kind string `json:"kind" example:"s3"`

	// Connection is the named platform S3 connection the object is written
	// over, empty for the portal. It is also the name the authorization
	// middleware checks independently when the write is issued, so a
	// destination whose connection the script's persona cannot reach is
	// refused a second time, by the authority of record.
	Connection string `json:"connection,omitempty" example:"acme-s3"`

	// Bucket is the bucket objects land in, empty for the portal.
	Bucket string `json:"bucket,omitempty" example:"acme-exports"`

	// Prefix is the key prefix every object written here sits under, empty for
	// the portal and optional for a bucket. It is the boundary of the grant:
	// the script chooses a key beneath it and can never write outside it.
	Prefix string `json:"prefix,omitempty" example:"weekly"`
}

// PortalDestination returns the canonical portal destination.
func PortalDestination() Destination {
	return Destination{Name: DestinationPortal, Kind: DestinationKindPortal}
}

// UnmarshalJSON reads a destination, accepting the bare name a grant recorded
// before a destination had an address.
//
// The portal was the only destination that existed then, so the older form is
// unambiguous rather than merely tolerable: "portal" meant exactly what
// PortalDestination means now. Accepting it is what lets a replica running this
// code read a grant a replica running the previous code approved, which is the
// direction a rolling upgrade actually produces — the older code cannot read
// this addressed form at all, so a version approved mid-upgrade is unreadable
// on the replicas that have not moved yet, and stays that way until they do.
func (d *Destination) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		if name != DestinationPortal {
			return fmt.Errorf("destination %q carries no address: only %q was ever recorded by name alone", name, DestinationPortal)
		}
		*d = PortalDestination()
		return nil
	}
	// A named type is required here: unmarshalling into Destination would call
	// this method again, forever.
	type record Destination
	var out record
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("reading a granted destination: %w", err)
	}
	*d = Destination(out)
	return nil
}

// IsPortal reports whether the destination is the platform's own asset store.
func (d Destination) IsPortal() bool { return d.Kind == DestinationKindPortal }

// Label renders a destination for an error message or a log line: the name a
// script writes, and the address it resolves to.
func (d Destination) Label() string {
	if d.IsPortal() {
		return d.Name
	}
	return fmt.Sprintf("%s (%s %s %s/%s)", d.Name, d.Kind, d.Connection, d.Bucket, d.Prefix)
}

// Normalized returns the destination with its fields trimmed and its prefix in
// one canonical form, so two approvals that meant the same place read as the
// same place in a diff rather than as a widening.
func (d Destination) Normalized() Destination {
	d.Name = strings.TrimSpace(d.Name)
	d.Kind = strings.TrimSpace(d.Kind)
	d.Connection = strings.TrimSpace(d.Connection)
	d.Bucket = strings.TrimSpace(d.Bucket)
	d.Prefix = strings.Trim(strings.TrimSpace(d.Prefix), "/")
	return d
}

// Validate checks that one destination names a place the platform can write.
func (d Destination) Validate() error {
	if d.Name == "" {
		return errors.New("a granted destination must be named, because the name is what a script writes")
	}
	if !slices.Contains(DestinationKinds, d.Kind) {
		return fmt.Errorf("destination %q has unknown kind %q: the platform implements %v",
			d.Name, d.Kind, DestinationKinds)
	}
	if d.IsPortal() {
		return d.validatePortal()
	}
	return d.validateBucket()
}

// validatePortal refuses a portal destination carrying an address. The platform
// owns where its own assets live, so a connection or bucket here would be a
// grant nothing reads, and a reviewer would have approved a route that does not
// exist.
func (d Destination) validatePortal() error {
	if d.Name != DestinationPortal {
		return fmt.Errorf("the portal destination must be named %q, not %q", DestinationPortal, d.Name)
	}
	if d.Connection != "" || d.Bucket != "" || d.Prefix != "" {
		return errors.New("the portal destination takes no connection, bucket, or prefix: the platform owns where its own assets are stored")
	}
	return nil
}

// validateBucket refuses an external destination that does not name a complete
// address. Every part is required at approval rather than defaulted at write
// time, because a default is a place nobody approved.
func (d Destination) validateBucket() error {
	// The name "portal" is reserved for the platform's own asset store. A
	// bucket wearing it would make every surface that resolves destinations by
	// name lie: an export naming no destination defaults to "portal", the
	// reviewer's diff would say "portal" and mean a bucket, and a data-region
	// refresh — which writes only portal documents — would resolve a grant that
	// contains no portal destination at all.
	if d.Name == DestinationPortal {
		return fmt.Errorf("the destination name %q is reserved for the platform's own asset store; give the bucket destination its own name", DestinationPortal)
	}
	if d.Connection == "" {
		return fmt.Errorf("destination %q must name the platform connection it writes over; a script never supplies one", d.Name)
	}
	if d.Bucket == "" {
		return fmt.Errorf("destination %q must name a bucket; a script never supplies one", d.Name)
	}
	if len(d.Prefix) > maxPrefixLength {
		return fmt.Errorf("destination %q has a %d-character prefix, over the %d-character limit",
			d.Name, len(d.Prefix), maxPrefixLength)
	}
	if d.Prefix != "" {
		if err := ValidateObjectKey(d.Prefix); err != nil {
			return fmt.Errorf("destination %q has an unusable prefix: %w", d.Name, err)
		}
	}
	return nil
}

// ValidateObjectKey checks a relative object key: the granted prefix of a
// destination, or the key a script writes beneath it.
//
// The rules refuse rather than rewrite. A key is part of the contract between a
// script and whatever consumes its output, so silently rewriting one would mean
// the object landing somewhere the source does not say — and a traversal that
// was cleaned away rather than reported is a refusal nobody was told about.
func ValidateObjectKey(key string) error {
	switch {
	case key == "":
		return errors.New("the key is empty")
	case len(key) > maxObjectKeyLength:
		return fmt.Errorf("the key is %d characters, over the %d-character limit", len(key), maxObjectKeyLength)
	case !utf8.ValidString(key):
		return errors.New("the key is not valid UTF-8")
	case strings.HasPrefix(key, "/"):
		return errors.New("the key must be relative to the destination's prefix, so it cannot start with '/'")
	case strings.Contains(key, `\`):
		return errors.New(`the key cannot contain '\'; object keys are separated by '/'`)
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return errors.New("the key cannot contain control characters")
		}
	}
	return validateKeySegments(key)
}

// validateKeySegments refuses the segment forms that would move an object out
// of the prefix it was granted under, or leave it on a path no consumer can
// name.
func validateKeySegments(key string) error {
	for segment := range strings.SplitSeq(key, "/") {
		switch segment {
		case "":
			return errors.New("the key cannot contain an empty path segment, so it cannot end with '/' or contain '//'")
		case ".", "..":
			return errors.New("the key cannot contain '.' or '..' segments: an output is written under the granted prefix and never outside it")
		}
		if strings.TrimSpace(segment) != segment {
			return fmt.Errorf("the key segment %q has leading or trailing whitespace", segment)
		}
	}
	return nil
}
