package script

import (
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/user"
)

// ErrNameTaken marks a transfer refused because the receiving owner already
// keeps a script under that name. Names are unique within an owner, so a
// transfer can fail on the receiving side alone; surfaces map it to a conflict
// rather than to an internal failure.
var ErrNameTaken = errors.New("the new owner already has a script with this name")

// Transfer moves this script to a new owner, normalizing the address the way
// every other identity in the platform is normalized so a transfer to
// "Jane@Example.com" and a login as "jane@example.com" name one person.
//
// Ownership is the whole of script visibility, so a transfer hands over
// everything at once: what the new owner sees, edits, runs, and schedules. The
// named use is moving a script to an administrator, which is how a script comes
// to run under an administrator's authority — the run presents the roles
// captured on the version it executes, and the store records the transfer as a
// version authored by the administrator making it.
//
// It refuses a transfer to the current owner rather than treating it as a
// no-op: the caller asked for a change, and silently recording a version that
// changes nothing would put a hand-over in the history that never happened.
func (s *Script) Transfer(newOwnerEmail string) error {
	if s == nil {
		return errors.New("no script to transfer")
	}
	normalized, err := user.NormalizeEmail(newOwnerEmail)
	if err != nil {
		return fmt.Errorf("the new owner is not a usable address: %w", err)
	}
	if strings.EqualFold(normalized, s.OwnerEmail) {
		return fmt.Errorf("script %q already belongs to %s", s.Name, normalized)
	}
	s.OwnerEmail = normalized
	return nil
}

// OutputDisposition is what a transfer does with the assets and collections the
// script's runs have already produced. A transfer moves the automation; the
// files its runs wrote record the previous owner's address, and nothing about
// the move rewrites them on its own (#1588). The caller states which of the two
// they mean, and the store reports what it did.
type OutputDisposition string

const (
	// OutputsMove moves the outputs with the script: every live asset and
	// collection the script CREATED comes to record the new owner's address,
	// so the person who now owns the script also owns what it refreshes.
	OutputsMove OutputDisposition = "move"
	// OutputsKeep leaves the outputs where they are. The script's runs go on
	// writing new versions into them, and the new owner cannot open, share or
	// delete them.
	OutputsKeep OutputDisposition = "keep"
)

// ParseOutputDisposition reads a disposition off the wire. The empty string is
// "unstated", which a surface accepts for a script that has produced nothing
// and refuses for one that has: a move of somebody's files must be asked for,
// and so must leaving them behind.
func ParseOutputDisposition(s string) (OutputDisposition, error) {
	switch d := OutputDisposition(strings.ToLower(strings.TrimSpace(s))); d {
	case "", OutputsMove, OutputsKeep:
		return d, nil
	default:
		return "", fmt.Errorf("outputs must be %q or %q, not %q", OutputsMove, OutputsKeep, s)
	}
}

// TransferRequest is one move of a script to a new owner.
type TransferRequest struct {
	// ID is the script being moved.
	ID string
	// NewOwnerEmail is the address the script goes to, normalized by the
	// transfer itself.
	NewOwnerEmail string
	// Outputs is what happens to the files the script's runs have written.
	// Anything but OutputsMove leaves them alone.
	Outputs OutputDisposition
}

// Transferred is what a transfer did beyond moving the script: how many of its
// outputs came to record the new owner. Both counts are zero when the outputs
// were kept.
type Transferred struct {
	AssetsMoved      int
	CollectionsMoved int
}
