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
