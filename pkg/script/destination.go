package script

import (
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

// DestinationResources is the name of the managed-resource destination: a file
// in the platform's resource library, addressed by the path the script writes as
// the key (#1663).
//
// It is built in like the portal, and for the same reason: the platform owns
// where its own library is, and there is nothing for configuration to point
// somewhere else. What it adds over the portal is the address. A portal output's
// identity is the output NAME, so a script's outputs are its own; a resource
// output's identity is a PATH, which a person, a second script, and a table
// registration can all name.
const DestinationResources = "resources"

// Destination kinds. The kind decides what the platform does with the bytes,
// and the set is closed: a destination is a place the platform implements a
// write for, not an open transport.
const (
	// DestinationKindPortal versions a portal asset.
	DestinationKindPortal = "portal"

	// DestinationKindS3 writes an object to a bucket over a named platform S3
	// connection. It is the only way an output leaves the platform.
	DestinationKindS3 = "s3"

	// DestinationKindResource versions the managed resource at a path in the
	// platform's own library, creating it the first time (#1663).
	DestinationKindResource = "resource"
)

// DestinationKinds is the full set of destination kinds.
var DestinationKinds = []string{DestinationKindPortal, DestinationKindS3, DestinationKindResource}

// Key limits. maxObjectKeyLength is S3's own limit on a full key; a
// destination's prefix is bounded well inside it so the key a script writes
// underneath always has room.
const (
	maxObjectKeyLength = 1024
	maxPrefixLength    = 512
)

// Destination is one place a script may write: named by the script, resolved
// by the platform against the deployment's configuration at run time. The
// portal is built in; every other destination is declared in the scripts
// configuration (scripts.destinations), so repointing one — changing its
// connection, bucket, or prefix — takes effect on the next run.
//
// A script supplies no endpoint, no credential, and no bucket. It names a
// destination and everything below comes from configuration, which is why
// there is no arbitrary egress to have: the only network a script reaches is
// the operator-configured connection set, and the write is authorized against
// the run's persona by the middleware like any other call.
type Destination struct {
	// Name is what a script writes as destination="...", unique within the
	// configured set.
	Name string `json:"name" yaml:"name" example:"acme-drop"`

	// Kind is one of DestinationKinds. Configuration declares only bucket
	// destinations, so it defaults to s3 there.
	Kind string `json:"kind" yaml:"kind" example:"s3"`

	// Connection is the named platform S3 connection the object is written
	// over, empty for the portal. It is the name the authorization middleware
	// checks when the write is issued, so a destination whose connection the
	// run's persona cannot reach is refused by the authority of record.
	Connection string `json:"connection,omitempty" yaml:"connection" example:"acme-s3"`

	// Bucket is the bucket objects land in, empty for the portal.
	Bucket string `json:"bucket,omitempty" yaml:"bucket" example:"acme-exports"`

	// Prefix is the key prefix every object written here sits under, empty for
	// the portal and optional for a bucket. It is the destination's boundary:
	// the script chooses a key beneath it and can never write outside it.
	Prefix string `json:"prefix,omitempty" yaml:"prefix" example:"weekly"`
}

// PortalDestination returns the canonical portal destination.
func PortalDestination() Destination {
	return Destination{Name: DestinationPortal, Kind: DestinationKindPortal}
}

// IsPortal reports whether the destination is the platform's own asset store.
func (d Destination) IsPortal() bool { return d.Kind == DestinationKindPortal }

// ResourcesDestination returns the canonical managed-resource destination.
func ResourcesDestination() Destination {
	return Destination{Name: DestinationResources, Kind: DestinationKindResource}
}

// IsResource reports whether the destination is the platform's own managed
// resource library.
func (d Destination) IsResource() bool { return d.Kind == DestinationKindResource }

// IsBuiltIn reports whether the platform owns this destination's address, which
// is what makes it available without configuration and what makes declaring it
// meaningless.
func (d Destination) IsBuiltIn() bool { return d.IsPortal() || d.IsResource() }

// Label renders a destination for an error message or a log line: the name a
// script writes, and the address it resolves to.
func (d Destination) Label() string {
	if d.IsBuiltIn() {
		return d.Name
	}
	return fmt.Sprintf("%s (%s %s %s/%s)", d.Name, d.Kind, d.Connection, d.Bucket, d.Prefix)
}

// Normalized returns the destination with its fields trimmed and its prefix in
// one canonical form, so two declarations that meant the same place read as
// the same place.
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
		return errors.New("a destination must be named, because the name is what a script writes")
	}
	if !slices.Contains(DestinationKinds, d.Kind) {
		return fmt.Errorf("destination %q has unknown kind %q: the platform implements %v",
			d.Name, d.Kind, DestinationKinds)
	}
	if d.IsBuiltIn() {
		return d.validateBuiltIn()
	}
	return d.validateBucket()
}

// validateBuiltIn refuses a built-in destination carrying an address. The
// platform owns where its own assets and its own library live, so a connection
// or bucket here would be an address nothing reads.
func (d Destination) validateBuiltIn() error {
	name := DestinationPortal
	if d.IsResource() {
		name = DestinationResources
	}
	if d.Name != name {
		return fmt.Errorf("the %s destination must be named %q, not %q", d.Kind, name, d.Name)
	}
	if d.Connection != "" || d.Bucket != "" || d.Prefix != "" {
		return fmt.Errorf("the %q destination takes no connection, bucket, or prefix: the platform owns where its own %s are stored",
			name, builtInSubject(d.Kind))
	}
	return nil
}

// builtInSubject names what a built-in destination stores, for the refusal
// above.
func builtInSubject(kind string) string {
	if kind == DestinationKindResource {
		return "library files"
	}
	return "assets"
}

// validateBucket refuses an external destination that does not name a complete
// address. Every part is required in configuration rather than defaulted at
// write time, because a default is a place nobody decided on.
func (d Destination) validateBucket() error {
	// The name "portal" is reserved for the platform's own asset store. A
	// bucket wearing it would make every surface that resolves destinations by
	// name lie: an export naming no destination defaults to "portal", and a
	// data-region refresh writes only portal documents.
	if d.Name == DestinationPortal || d.Name == DestinationResources {
		return fmt.Errorf("the destination name %q is reserved for one of the platform's own stores; give the bucket destination its own name", d.Name)
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

// ValidateDeclaredDestinations checks the destination set a deployment
// declares in configuration: each must be a complete bucket address, one name
// is one place, and the built-in portal cannot be redeclared.
func ValidateDeclaredDestinations(destinations []Destination) error {
	seen := make(map[string]bool, len(destinations))
	for _, d := range destinations {
		if d.IsBuiltIn() {
			return fmt.Errorf("the %q destination is built in and cannot be declared", d.Name)
		}
		if err := d.Validate(); err != nil {
			return err
		}
		if seen[d.Name] {
			return fmt.Errorf("destination %q is declared twice; one name is one place", d.Name)
		}
		seen[d.Name] = true
	}
	return nil
}

// SplitLibraryKey reads the key a script writes to the managed-resource
// destination as the address it is: the folder chain inside the library, and the
// file's name within it (#1663).
//
// It is one string rather than two arguments because that is how the file is
// written everywhere else it is named: in the portal's own breadcrumb, in the
// mcp:// URI, and in the table registration that follows it. A script writing
// key="datasets/acme/orders.csv" and a person looking at
// datasets/acme/orders.csv are looking at the same words.
//
// The folder is required because a managed resource is filed in one, the same way
// an upload through the portal is. The refusal says so rather than inventing a
// folder, because a file landing somewhere the source does not name is the
// problem this whole destination exists to avoid.
//
// The segment rules are the object key's own, so one key grammar covers both
// destinations a script can address by key. What a library accepts beyond that --
// the folder depth, the characters a filename keeps -- is the library's to
// enforce at the write, and it reports its own refusals.
func SplitLibraryKey(key string) (path, filename string, err error) {
	if err := ValidateObjectKey(key); err != nil {
		return "", "", err
	}
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return "", "", fmt.Errorf("the key %q names a file but no folder; a file in the library is filed in one, so write it as \"datasets/%s\"", key, key)
	}
	return key[:i], key[i+1:], nil
}

// ValidateObjectKey checks a relative object key: the configured prefix of a
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
// of the prefix it writes under, or leave it on a path no consumer can
// name.
func validateKeySegments(key string) error {
	for segment := range strings.SplitSeq(key, "/") {
		switch segment {
		case "":
			return errors.New("the key cannot contain an empty path segment, so it cannot end with '/' or contain '//'")
		case ".", "..":
			return errors.New("the key cannot contain '.' or '..' segments: an output is written under the destination's prefix and never outside it")
		}
		if strings.TrimSpace(segment) != segment {
			return fmt.Errorf("the key segment %q has leading or trailing whitespace", segment)
		}
	}
	return nil
}
